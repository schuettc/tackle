package serve

// The agent side of serve: which agent sessions are connected, who the
// review goes to, and delivery of Send. The shape (presence, a long-poll
// wait woken on send) is copied from cull's serve; sift does not import it.
// sift has one review per machine, the latest round, so there is no project.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
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

// presentSessions lists the sessions present now.
func (s *Server) presentSessions() []string {
	s.ag.mu.Lock()
	defer s.ag.mu.Unlock()
	var out []string
	for id, cur := range s.ag.sessions {
		if s.clock().Sub(cur.seen) <= presenceTTL {
			out = append(out, id)
		}
	}
	return out
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

func (s *Server) agentPresence(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Session string `json:"session"`
		Harness string `json:"harness"`
		Label   string `json:"label"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.Session == "" {
		writeErr(w, http.StatusBadRequest, "session is required")
		return
	}
	s.touch(b.Session, b.Harness, b.Label)
	s.wakeAll() // a present session may now be eligible to claim
	w.WriteHeader(http.StatusNoContent)
}

// agentReview makes the session the one the latest round's review goes to,
// and returns the page's address and how many rows wait for the user.
func (s *Server) agentReview(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Session string `json:"session"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.Session == "" {
		writeErr(w, http.StatusBadRequest, "session is required")
		return
	}
	rd, rows, err := s.st.LatestRound(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no round yet: run sift check")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	prog, err := s.st.State(r.Context(), rd.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if prog.State == store.Recommending {
		writeErr(w, http.StatusConflict, fmt.Sprintf("round %d is still being recommended: %d of %d file(s) have a recommendation; recommend the rest (sift_next, sift_propose), then review",
			rd.ID, prog.Recommended, prog.Files))
		return
	}
	open, err := s.open(r.Context(), rd, rows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.touch(b.Session, "", "")
	if err := s.st.SetOwner(r.Context(), rd.ID, b.Session, s.labelOf(b.Session)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.wakeAll()
	writeJSON(w, http.StatusOK, map[string]any{"round": rd.ID, "url": s.pageURL, "open": open})
}

// open counts what waits for the user: in an audit round the files with a
// recommendation and no decision, in a backlog round the rows with none.
func (s *Server) open(ctx context.Context, rd store.Round, rows []row.Row) (int, error) {
	n := 0
	if store.PerItem(rd.Kind) {
		for _, rw := range rows {
			if rw.Decision == nil {
				n++
			}
		}
		return n, nil
	}
	items, err := s.st.Files(ctx, rd.ID)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if it.Rec != nil && it.Decision == nil {
			n++
		}
	}
	return n, nil
}

func (s *Server) agentWait(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("session")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "session is required")
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
		// Each send goes to the owner it was sent to while that owner is
		// present; the store picks and claims it in one statement.
		sd, ok, err := s.st.ClaimSend(r.Context(), id, s.presentSessions())
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if ok {
			writeJSON(w, http.StatusOK, map[string]any{"id": sd.ID, "round": sd.Round, "counts": sd.Counts, "text": SendText(sd)})
			return
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

func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"round": 0, "state": "", "files": 0, "recommended": 0, "open": 0, "decided": 0, "sent": 0, "owner": "", "owner_present": false}
	rd, rows, err := s.st.LatestRound(r.Context())
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	default:
		prog, err := s.st.State(r.Context(), rd.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		open, err := s.open(r.Context(), rd, rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		decided, sent := 0, 0
		count := func(made, wasSent bool) {
			switch {
			case made && wasSent:
				sent++
			case made:
				decided++
			}
		}
		if store.PerItem(rd.Kind) {
			for _, rw := range rows {
				count(rw.Decision != nil, rw.Decision != nil && rw.Decision.Sent)
			}
		} else {
			items, err := s.st.Files(r.Context(), rd.ID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			for _, it := range items {
				count(it.Decision != nil, it.Decision != nil && it.Decision.Sent)
			}
		}
		out["round"], out["state"], out["files"], out["recommended"] = rd.ID, prog.State, prog.Files, prog.Recommended
		out["open"], out["decided"], out["sent"] = open, decided, sent
		out["owner"], out["owner_present"] = rd.OwnerLabel, rd.OwnerSession != "" && s.present(rd.OwnerSession)
	}
	pend, err := s.st.Undelivered(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	und := make([]map[string]any, 0, len(pend))
	for _, sd := range pend {
		und = append(und, map[string]any{"id": sd.ID, "round": sd.Round, "at": rfc(sd.CreatedAt), "counts": sd.Counts})
	}
	out["undelivered"] = und
	writeJSON(w, http.StatusOK, out)
}

// SendText is the message an agent reads when the user presses Send: the
// counts, the user's notes (each with its row), and what to do next. It is
// the one place that wording lives.
func SendText(sd store.Send) string {
	c := sd.Counts
	var b strings.Builder
	fmt.Fprintf(&b, "The user sent their decisions for sift round %d: %d accepted, %d edited, %d rejected.\n",
		sd.Round, c.Accept, c.Edit, c.Reject)
	if len(sd.Notes) > 0 {
		b.WriteString("Notes:\n")
		for _, n := range sd.Notes {
			fmt.Fprintf(&b, "- %s: %s\n", n.Row, n.Note)
		}
	}
	b.WriteString("Next: run sift_apply (or `sift apply`): it writes each accepted or edited file whole, on a branch per repo, and lists what it leaves to you. Then run `sift reconcile` and review each branch before it merges.")
	return b.String()
}
