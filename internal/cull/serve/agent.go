package serve

// The agent side of serve: which agent sessions are connected, who owns each
// project's review, and delivery of Send to them. The shape (presence,
// long-poll wait with a wake on send) is copied from casebook's channel; cull
// does not import it.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tackle/internal/cull/store"
)

// presenceTTL is how long a session counts as present after its last check-in.
const presenceTTL = 90 * time.Second

// maxWait caps one long poll.
const maxWait = 60

type session struct {
	harness, label string
	seen           time.Time
}

// agents is the in-memory half: sessions and the wake-up for waiters. The
// zero value is ready to use.
type agents struct {
	mu       sync.Mutex
	sessions map[string]*session
	wake     chan struct{} // closed and replaced to wake every waiter
}

func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Server) unit() time.Duration {
	if s.waitUnit > 0 {
		return s.waitUnit
	}
	return time.Second
}

// touch registers or refreshes a session. Empty harness/label keep what was
// registered before.
func (s *Server) touch(id, harness, label string) {
	s.ag.mu.Lock()
	defer s.ag.mu.Unlock()
	if s.ag.sessions == nil {
		s.ag.sessions = map[string]*session{}
	}
	cur := s.ag.sessions[id]
	if cur == nil {
		cur = &session{}
		s.ag.sessions[id] = cur
	}
	if harness != "" {
		cur.harness = harness
	}
	if label != "" {
		cur.label = label
	}
	cur.seen = s.clock()
}

// present reports whether the session checked in within presenceTTL.
func (s *Server) present(id string) bool {
	if id == "" {
		return false
	}
	s.ag.mu.Lock()
	defer s.ag.mu.Unlock()
	cur := s.ag.sessions[id]
	return cur != nil && s.clock().Sub(cur.seen) <= presenceTTL
}

func (s *Server) labelOf(id string) string {
	s.ag.mu.Lock()
	defer s.ag.mu.Unlock()
	if cur := s.ag.sessions[id]; cur != nil && cur.label != "" {
		return cur.label
	}
	return id
}

// waker returns the channel that closes at the next wake.
func (s *Server) waker() <-chan struct{} {
	s.ag.mu.Lock()
	defer s.ag.mu.Unlock()
	if s.ag.wake == nil {
		s.ag.wake = make(chan struct{})
	}
	return s.ag.wake
}

func (s *Server) wakeAll() {
	s.ag.mu.Lock()
	defer s.ag.mu.Unlock()
	if s.ag.wake != nil {
		close(s.ag.wake)
	}
	s.ag.wake = make(chan struct{})
}

// agentProject resolves a root (a subdirectory maps to its project, as in
// cull check) and creates the project. ok is false after writing the error.
func (s *Server) agentProject(w http.ResponseWriter, r *http.Request, root string) (store.Project, bool) {
	if !filepath.IsAbs(root) {
		writeErr(w, http.StatusBadRequest, "root must be an absolute path")
		return store.Project{}, false
	}
	top, err := discover.Root(root)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return store.Project{}, false
	}
	p, err := s.st.Project(r.Context(), top)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return store.Project{}, false
	}
	return p, true
}

func (s *Server) agentPresence(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Session string `json:"session"`
		Harness string `json:"harness"`
		Label   string `json:"label"`
		Root    string `json:"root"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.Session == "" {
		writeErr(w, http.StatusBadRequest, "session is required")
		return
	}
	if !filepath.IsAbs(b.Root) {
		writeErr(w, http.StatusBadRequest, "root must be an absolute path")
		return
	}
	s.touch(b.Session, b.Harness, b.Label)
	s.wakeAll() // a present session may now be eligible to claim
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentReview(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Session string `json:"session"`
		Root    string `json:"root"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.Session == "" {
		writeErr(w, http.StatusBadRequest, "session is required")
		return
	}
	p, ok := s.agentProject(w, r, b.Root)
	if !ok {
		return
	}
	s.touch(b.Session, "", "")
	if err := s.st.SetOwner(r.Context(), p.ID, b.Session, s.labelOf(b.Session)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.wakeAll()
	writeJSON(w, http.StatusOK, map[string]any{"project": p.ID, "url": fmt.Sprintf("%s#/p/%d", s.pageURL, p.ID)})
}

// ownerPresent is the project's owner session, when it is present.
func (s *Server) ownerPresent(p store.Project) (string, bool) {
	if p.OwnerSession != "" && s.present(p.OwnerSession) {
		return p.OwnerSession, true
	}
	return "", false
}

// agentWait is a session's long poll for Court's Sends. root is the
// session's folder (its scope): the session covers the repository that
// contains that folder and every repository inside it, so an agent started
// at a workspace root hears Sends for each repository in the workspace.
func (s *Server) agentWait(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("session")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "session is required")
		return
	}
	scope := q.Get("root")
	if !filepath.IsAbs(scope) {
		writeErr(w, http.StatusBadRequest, "root must be an absolute path")
		return
	}
	secs := 30
	if v, err := strconv.Atoi(q.Get("timeout")); err == nil && v > 0 {
		secs = min(v, maxWait)
	}
	deadline := time.NewTimer(time.Duration(secs) * s.unit())
	defer deadline.Stop()
	tick := time.NewTicker(2 * s.unit()) // presence expires with no event to wake us
	defer tick.Stop()
	for {
		wake := s.waker()
		s.touch(id, "", "") // a connected long poll is a live session
		ps, err := s.covered(r, scope)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, p := range ps {
			if owner, present := s.ownerPresent(p); present && owner != id {
				continue // the present owner takes this project's Sends
			}
			sd, ok, err := s.st.ClaimSend(r.Context(), p.ID, id)
			if err != nil {
				if r.Context().Err() != nil {
					return
				}
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if ok {
				writeJSON(w, http.StatusOK, map[string]any{"id": sd.ID, "project": p.ID, "root": p.Root,
					"counts": sd.Counts, "text": SendText(sd, p.Root)})
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-wake:
		case <-tick.C:
		}
	}
}

// covered is every project a session whose folder is scope covers: the one
// containing scope, and every known project inside it.
func (s *Server) covered(r *http.Request, scope string) ([]store.Project, error) {
	top, err := discover.Root(scope)
	if err != nil {
		return nil, err
	}
	all, err := s.st.Projects(r.Context())
	if err != nil {
		return nil, err
	}
	in := realPath(scope)
	var out []store.Project
	for _, p := range all {
		pr := realPath(p.Root)
		if p.Root == top || pr == in || strings.HasPrefix(pr, in+string(filepath.Separator)) {
			out = append(out, p)
		}
	}
	return out, nil
}

// realPath resolves symlinks (macOS's /var is /private/var), or returns p.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.agentProject(w, r, r.URL.Query().Get("root"))
	if !ok {
		return
	}
	cur, err := s.st.ProjectByID(r.Context(), p.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	answers, err := s.st.Answers(r.Context(), p.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	open := 0
	if _, items, err := s.st.LatestRun(r.Context(), p.ID); err == nil {
		for _, it := range items {
			if _, a := answers[store.Key{ItemID: it.ID, Hash: it.Hash}]; !a {
				open++
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	answered, sent := 0, 0
	for _, a := range answers {
		if a.SentAt.IsZero() {
			answered++
		} else {
			sent++
		}
	}
	pend, err := s.st.Undelivered(r.Context(), p.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	und := make([]map[string]any, 0, len(pend))
	for _, sd := range pend {
		und = append(und, map[string]any{"id": sd.ID, "at": rfc(sd.CreatedAt), "counts": sd.Counts})
	}
	_, present := s.ownerPresent(cur)
	writeJSON(w, http.StatusOK, map[string]any{"project": p.ID, "root": p.Root, "open": open, "answered": answered,
		"sent": sent, "owner": cur.OwnerLabel, "owner_present": present, "undelivered": und})
}

// SendText is the message an agent reads when Court presses Send: the counts,
// his notes (each with its test or group), and what to do next. It is the one
// place that wording lives.
func SendText(sd store.Send, root string) string {
	c := sd.Counts
	var b strings.Builder
	fmt.Fprintf(&b, "Court sent his answers for %s: %d cut, %d keep, %d merge, %d separate.\n",
		filepath.Base(root), c.Cut, c.Keep, c.Merge, c.Separate)
	if len(sd.Notes) > 0 {
		b.WriteString("Notes:\n")
		for _, n := range sd.Notes {
			fmt.Fprintf(&b, "- %s: %s\n", n.Name, n.Note)
		}
	}
	fmt.Fprintf(&b, "Next: run cull_check with path %s, then cull_apply with path %s to remove the cuts.", root, root)
	if c.Merge > 0 {
		b.WriteString("\nThen rewrite each group he chose to merge as one table test and run cull_check_group on it.")
	}
	return b.String()
}
