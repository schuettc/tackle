package serve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Handler is the /api/ mux (mounted by localweb, which checks the token).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/review", s.review)
	mux.HandleFunc("PUT /api/decisions", s.putDecisions)
	mux.HandleFunc("DELETE /api/decisions", s.deleteDecision)
	mux.HandleFunc("POST /api/undo", s.undo)
	mux.HandleFunc("POST /api/redo", s.redo)
	mux.HandleFunc("POST /api/send", s.send)
	mux.HandleFunc("GET /api/file", s.file)
	mux.HandleFunc("POST /api/agent/presence", s.agentPresence)
	mux.HandleFunc("POST /api/agent/review", s.agentReview)
	mux.HandleFunc("GET /api/agent/wait", s.agentWait)
	mux.HandleFunc("GET /api/agent/status", s.agentStatus)
	mux.HandleFunc("GET /api/events", s.stream)
	mux.HandleFunc("GET /api/poll", s.poll)
	mux.HandleFunc("POST /api/stop", s.postStop)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.activity.Store(time.Now().UnixMilli())
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) postStop(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
	go func() { time.Sleep(50 * time.Millisecond); s.stopNow() }()
}

func rfc(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

type roundJSON struct {
	ID      int64          `json:"id"`
	Kind    string         `json:"kind"`
	At      string         `json:"at"`
	Summary map[string]int `json:"summary"`
	Owner   string         `json:"owner"`
}

type applyJSON struct {
	Repo   string   `json:"repo"`
	Base   string   `json:"base"`
	Branch string   `json:"branch"`
	PR     string   `json:"pr"`
	State  string   `json:"state"`
	Detail string   `json:"detail"`
	Rows   []string `json:"rows"`
	At     string   `json:"at"`
	// LastState and LastDetail are a later attempt that left a success as
	// it was (held: the branch exists).
	LastState  string `json:"last_state,omitempty"`
	LastDetail string `json:"last_detail,omitempty"`
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	// Read the cursor before any review data: an event racing the reads is
	// redelivered to the client, never skipped.
	cursor := s.events.format(s.events.latest())
	rd, rows, err := s.st.LatestRound(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"cursor": cursor, "round": nil, "rows": []row.Row{}, "applies": []applyJSON{}, "sends": 0, "home": home()})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	applies, err := s.st.Applies(r.Context(), rd.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	aj := make([]applyJSON, 0, len(applies))
	for _, a := range applies {
		aj = append(aj, applyJSON{Repo: a.Repo, Base: a.Base, Branch: a.Branch, PR: a.PR, State: a.State, Detail: a.Detail,
			Rows: append([]string{}, a.Rows...), At: rfc(a.At), LastState: a.Last.State, LastDetail: a.Last.Detail})
	}
	sends, err := s.st.Sends(r.Context(), rd.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []row.Row{}
	}
	sum := rd.Summary
	if sum == nil {
		sum = map[string]int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cursor":  cursor,
		"round":   roundJSON{ID: rd.ID, Kind: rd.Kind, At: rfc(rd.At), Summary: sum, Owner: rd.OwnerLabel},
		"rows":    rows,
		"applies": aj,
		"sends":   sends,
		"home":    home(),
	})
}

// home is the user's home directory, so the page can show paths under it
// as ~/….
func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad request body: %w", err)
	}
	return nil
}

// current checks that round is the latest round (else the page is stale)
// and returns its rows by id. ok is false after writing the error.
func (s *Server) current(w http.ResponseWriter, ctx context.Context, round int64) (map[string]row.Row, bool) {
	if round == 0 {
		writeErr(w, http.StatusBadRequest, "round is required")
		return nil, false
	}
	rd, rows, err := s.st.LatestRound(ctx)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && rd.ID != round) {
		writeErr(w, http.StatusConflict, "stale")
		return nil, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	byID := make(map[string]row.Row, len(rows))
	for _, rw := range rows {
		byID[rw.ID] = rw
	}
	return byID, true
}

type decisionIn struct {
	ID      string   `json:"id"`
	Action  string   `json:"action"`
	Verdict string   `json:"verdict"`
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Cleared []string `json:"cleared"`
	Note    string   `json:"note"`
}

func (d decisionIn) decision() row.Decision {
	return row.Decision{Action: d.Action, Verdict: d.Verdict, Title: d.Title, Text: d.Text, Cleared: d.Cleared, Note: d.Note}
}

func (s *Server) putDecisions(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Round     int64        `json:"round"`
		Decisions []decisionIn `json:"decisions"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(b.Decisions) == 0 {
		writeErr(w, http.StatusBadRequest, "no decisions")
		return
	}
	for _, d := range b.Decisions {
		if err := d.decision().Validate(); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	rows, ok := s.current(w, r.Context(), b.Round)
	if !ok {
		return
	}
	for _, d := range b.Decisions {
		if _, ok := rows[d.ID]; !ok {
			writeErr(w, http.StatusConflict, "stale")
			return
		}
	}
	for _, d := range b.Decisions {
		err := s.st.Decide(r.Context(), b.Round, d.ID, d.decision())
		if errors.Is(err, store.ErrStale) {
			writeErr(w, http.StatusConflict, "stale")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.events.emit("decisions", map[string]int64{"round": b.Round})
	w.WriteHeader(http.StatusNoContent)
}

// applied reports whether apply already wrote rw's repo for the round: an
// undo or redo can no longer change that branch.
func (s *Server) applied(ctx context.Context, round int64, rw row.Row) (bool, error) {
	as, err := s.st.Applies(ctx, round)
	if err != nil {
		return false, err
	}
	for _, a := range as {
		if a.Repo == rw.Source.Repo && a.Succeeded() {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) deleteDecision(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	round, _ := strconv.ParseInt(q.Get("round"), 10, 64)
	rows, ok := s.current(w, r.Context(), round)
	if !ok {
		return
	}
	rw, ok := rows[q.Get("id")]
	if !ok {
		writeErr(w, http.StatusConflict, "stale")
		return
	}
	if rw.FixOnly() {
		writeErr(w, http.StatusBadRequest, "a certain fix is undone or redone, not cleared")
		return
	}
	if err := s.st.Undecide(r.Context(), round, rw.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.emit("decisions", map[string]int64{"round": round})
	w.WriteHeader(http.StatusNoContent)
}

// undo takes a certain fix out of what sift applies: a reject on it.
func (s *Server) undo(w http.ResponseWriter, r *http.Request) { s.fixDecision(w, r, "reject") }

// redo puts an undone certain fix back: an accept of the fix, a decision
// the next Send carries like any other.
func (s *Server) redo(w http.ResponseWriter, r *http.Request) { s.fixDecision(w, r, "accept") }

// fixDecision stores action on a certain row that carries only its own fix
// (row.FixOnly), until apply has written the row's repo.
func (s *Server) fixDecision(w http.ResponseWriter, r *http.Request, action string) {
	var b struct {
		Round int64  `json:"round"`
		ID    string `json:"id"`
		Note  string `json:"note"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, ok := s.current(w, r.Context(), b.Round)
	if !ok {
		return
	}
	rw, ok := rows[b.ID]
	if !ok {
		writeErr(w, http.StatusConflict, "stale")
		return
	}
	if !rw.FixOnly() {
		writeErr(w, http.StatusBadRequest, "only a certain fix can be undone or redone; accept or reject a proposal instead")
		return
	}
	if done, err := s.applied(r.Context(), b.Round, rw); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	} else if done {
		writeErr(w, http.StatusConflict, "already applied: change it on the branch")
		return
	}
	if err := s.st.Decide(r.Context(), b.Round, b.ID, row.Decision{Action: action, Note: b.Note}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.emit("decisions", map[string]int64{"round": b.Round})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Round int64 `json:"round"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := s.current(w, r.Context(), b.Round); !ok {
		return
	}
	rd, _, err := s.st.Round(r.Context(), b.Round)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sd, err := s.st.Send(r.Context(), b.Round, rd.OwnerSession)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n := sd.Counts.Total()
	to := ""
	if sd.ID != 0 {
		s.events.emit("decisions", map[string]int64{"round": b.Round})
		s.wakeAll()
		if rd.OwnerSession != "" && s.present(rd.OwnerSession) {
			to = rd.OwnerLabel
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Sent int    `json:"sent"`
		To   string `json:"to"`
	}{n, to})
}

// maxFile caps what /api/file returns.
const maxFile = 2 << 20

// moved says why a row's file on disk can't be read as audited: its path
// is not clean and absolute, or it resolves somewhere other than it did then
// (a symlink, the leaf or a parent, retargeted). "" when it can be read.
func moved(src row.Source) string {
	if src.Canon == "" {
		return "sift did not record where " + src.File + " resolved: run sift check again"
	}
	if now := row.Resolve(src.File); now != src.Canon {
		return src.File + " resolves somewhere else since the audit: run sift check again"
	}
	return ""
}

// readNoFollow reads up to n bytes of path, refusing a symlink at the leaf.
func readNoFollow(path string, n int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, n))
}

// file returns the whole file a row is about, as it was audited: a repo
// file at the ref it was read at, any other file from disk where it
// resolved at the audit (see moved). Only a row's own file can be read.
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	round, _ := strconv.ParseInt(q.Get("round"), 10, 64)
	rows, ok := s.current(w, r.Context(), round)
	if !ok {
		return
	}
	rw, ok := rows[q.Get("id")]
	if !ok {
		writeErr(w, http.StatusNotFound, "no such row")
		return
	}
	src := rw.Source
	var b []byte
	var err error
	switch {
	case src.Repo != "" && src.Ref != "" && src.Path != "":
		b, err = discover.Git(r.Context(), src.Repo, "show", src.Ref+":"+src.Path).Output()
	case src.File != "":
		if why := moved(src); why != "" {
			writeErr(w, http.StatusForbidden, why)
			return
		}
		b, err = readNoFollow(src.Canon, maxFile+1)
	default:
		writeErr(w, http.StatusNotFound, "this row has no file")
		return
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("can't read %s: %v", src.File, err))
		return
	}
	truncated := len(b) > maxFile
	if truncated {
		b = b[:maxFile]
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": src.File, "ref": src.Ref, "content": string(b), "truncated": truncated})
}
