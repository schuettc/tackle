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
	"time"

	"github.com/schuettc/tackle/internal/sift/content"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Handler is the /api/ mux (mounted by localweb, which checks the token).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/review", s.review)
	mux.HandleFunc("PUT /api/decisions", s.putDecisions)
	mux.HandleFunc("DELETE /api/decisions", s.deleteDecision)
	mux.HandleFunc("PUT /api/files", s.putFile)
	mux.HandleFunc("POST /api/files/clear", s.clearFile)
	mux.HandleFunc("PUT /api/notes", s.putNote)
	mux.HandleFunc("GET /api/base", s.base)
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
		writeJSON(w, http.StatusOK, map[string]any{"cursor": cursor, "round": nil, "rows": []row.Row{}, "files": []fileJSON{},
			"progress": store.Progress{}, "applies": []applyJSON{}, "sends": 0, "home": home()})
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
	prog, err := s.st.State(r.Context(), rd.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := s.st.Files(r.Context(), rd.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	files := make([]fileJSON, 0, len(items))
	for _, it := range items {
		files = append(files, fileOf(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cursor":   cursor,
		"round":    roundJSON{ID: rd.ID, Kind: rd.Kind, At: rfc(rd.At), Summary: sum, Owner: rd.OwnerLabel},
		"progress": prog,
		"rows":     rows,
		"files":    files,
		"applies":  aj,
		"sends":    sends,
		"home":     home(),
	})
}

// fileJSON is one of an audit round's files on the page: where it is, its
// size before (Size) and after (After: the recommendation, or the user's
// edit), its findings, the recommendation, the decision, the fingerprint a
// decision answers and the files decided with it. The audited content
// itself is read on demand (GET /api/base).
type fileJSON struct {
	Key         string        `json:"key"`
	Path        string        `json:"path"`
	Source      row.Source    `json:"source"`
	Commit      string        `json:"commit,omitempty"`
	Class       string        `json:"class"`
	Budget      int           `json:"budget"`
	Base        string        `json:"base"`
	Size        int           `json:"size"`
	After       int           `json:"after"`
	Rows        []string      `json:"rows"`
	Rec         *rec.Rec      `json:"rec"`
	Decision    *rec.Decision `json:"decision"`
	Fingerprint string        `json:"fingerprint"`
	Group       []string      `json:"group"`
}

func fileOf(it store.FileItem) fileJSON {
	f := fileJSON{Key: it.Key, Path: it.Source.File, Source: it.Source, Commit: it.Commit, Class: it.Class, Budget: it.Budget,
		Base: it.Base, Size: it.Size, After: it.Size, Rows: append([]string{}, it.Rows...), Rec: it.Rec, Decision: it.Decision,
		Fingerprint: it.Fingerprint, Group: append([]string{}, it.Group...)}
	if it.Rec != nil {
		f.After = len(it.Rec.Content)
		if d := it.Decision; d != nil && d.Action == "edit" {
			f.After = len(d.Content)
		}
	}
	return f
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
	// Fingerprint is the row's fingerprint as the page showed it.
	Fingerprint string `json:"fingerprint"`
	// TargetFingerprint is, for an edit to merge:C, C's fingerprint as the
	// page showed it.
	TargetFingerprint string `json:"target_fingerprint"`
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
		if d.Fingerprint == "" {
			writeErr(w, http.StatusBadRequest, "a decision carries the fingerprint of the row it answers")
			return
		}
	}
	rows, ok := s.current(w, r.Context(), b.Round)
	if !ok {
		return
	}
	if !s.perItem(w, r.Context(), b.Round) {
		return
	}
	as := make([]store.Answer, 0, len(b.Decisions))
	for _, d := range b.Decisions {
		if _, ok := rows[d.ID]; !ok {
			writeErr(w, http.StatusConflict, "stale")
			return
		}
		as = append(as, store.Answer{Row: d.ID, Fingerprint: d.Fingerprint, TargetFingerprint: d.TargetFingerprint, Decision: d.decision()})
	}
	after, err := s.st.Answer(r.Context(), b.Round, as)
	if err != nil {
		s.rowErr(w, r.Context(), b.Round, err)
		return
	}
	s.events.emit("decisions", map[string]int64{"round": b.Round})
	// The answered rows as they now are: the page shows them, and its next
	// decision answers their fingerprints.
	writeJSON(w, http.StatusOK, map[string]any{"rows": after})
}

// rowErr writes a row decision's error: 409 when the round moved on, a row
// changed since the page showed it, or the agent is still answering the
// items.
func (s *Server) rowErr(w http.ResponseWriter, ctx context.Context, round int64, err error) {
	switch {
	case errors.Is(err, store.ErrStale):
		writeErr(w, http.StatusConflict, "stale")
	case errors.Is(err, store.ErrChanged):
		writeErr(w, http.StatusConflict, "changed: the agent changed this row since the page showed it; look again")
	case errors.Is(err, store.ErrNotReady):
		p, _ := s.st.State(ctx, round)
		writeErr(w, http.StatusConflict, fmt.Sprintf("the agent is still recommending: %d of %d items have its verdict; decisions open once every one has", p.Recommended, p.Files))
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// perItem checks the round is decided per item (backlog, intake); an audit
// round's rows are evidence, and its files are decided. ok is false after
// writing the error.
func (s *Server) perItem(w http.ResponseWriter, ctx context.Context, round int64) bool {
	rd, _, err := s.st.Round(ctx, round)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !store.PerItem(rd.Kind) {
		writeErr(w, http.StatusBadRequest, "an audit round is decided per file, not per row: PUT /api/files")
		return false
	}
	return true
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
	if q.Get("fingerprint") == "" {
		writeErr(w, http.StatusBadRequest, "a clear carries the fingerprint of the row it answers")
		return
	}
	if !s.perItem(w, r.Context(), round) {
		return
	}
	after, err := s.st.Undecide(r.Context(), round, rw.ID, q.Get("fingerprint"))
	if err != nil {
		s.rowErr(w, r.Context(), round, err)
		return
	}
	s.events.emit("decisions", map[string]int64{"round": round})
	writeJSON(w, http.StatusOK, map[string]any{"rows": []row.Row{after}})
}

// fileApplied names the first file of key's group whose repo apply has
// already written for the round ("" when none): its decision is on the
// branch now, and changing it here would change nothing.
func (s *Server) fileApplied(ctx context.Context, round int64, key string) (string, error) {
	items, err := s.st.Files(ctx, round)
	if err != nil {
		return "", err
	}
	as, err := s.st.Applies(ctx, round)
	if err != nil {
		return "", err
	}
	by := map[string]store.FileItem{}
	for _, it := range items {
		by[it.Key] = it
	}
	for _, k := range by[key].Group {
		for _, a := range as {
			if src := by[k].Source; src.Repo != "" && a.Repo == src.Repo && a.Succeeded() {
				return src.File, nil
			}
		}
	}
	return "", nil
}

// fileErr writes a file decision's error: 409 when the page is stale (the
// round moved on, a print changed, the agent is still recommending), 400
// for a bad decision.
func fileErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrStale):
		writeErr(w, http.StatusConflict, "stale")
	case errors.Is(err, store.ErrChanged):
		writeErr(w, http.StatusConflict, "changed: this file's recommendation or edit changed since the page showed it; look again")
	case errors.Is(err, store.ErrNotReady):
		writeErr(w, http.StatusConflict, "the agent is still recommending: decisions open once every file has a recommendation")
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) putFile(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Round   int64             `json:"round"`
		File    string            `json:"file"`
		Action  string            `json:"action"`
		Content string            `json:"content"`
		Note    string            `json:"note"`
		Prints  map[string]string `json:"prints"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d := rec.Decision{Action: b.Action, Content: b.Content, Note: b.Note}
	if err := d.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(b.Prints) == 0 {
		writeErr(w, http.StatusBadRequest, "a decision carries the fingerprints of the files it answers (prints)")
		return
	}
	if _, ok := s.current(w, r.Context(), b.Round); !ok {
		return
	}
	if done, err := s.fileApplied(r.Context(), b.Round, b.File); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	} else if done != "" {
		writeErr(w, http.StatusConflict, "already applied: "+done+" is on its branch; change it there")
		return
	}
	after, err := s.st.DecideFile(r.Context(), b.Round, b.File, d, b.Prints)
	if err != nil {
		fileErr(w, err)
		return
	}
	s.events.emit("decisions", map[string]int64{"round": b.Round})
	writeJSON(w, http.StatusOK, map[string]any{"files": filesOf(after)})
}

// filesOf is a decision's snapshot on the wire: the group's files as they
// now are. The page shows them, and its next decision answers their prints.
func filesOf(items []store.FileItem) []fileJSON {
	out := make([]fileJSON, 0, len(items))
	for _, it := range items {
		out = append(out, fileOf(it))
	}
	return out
}

// clearFile clears a file's group (an edited file goes back to the
// recommendation), against the prints the page showed, as putFile decides.
func (s *Server) clearFile(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Round  int64             `json:"round"`
		File   string            `json:"file"`
		Prints map[string]string `json:"prints"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(b.Prints) == 0 {
		writeErr(w, http.StatusBadRequest, "a clear carries the fingerprints of the files it answers (prints)")
		return
	}
	if _, ok := s.current(w, r.Context(), b.Round); !ok {
		return
	}
	if done, err := s.fileApplied(r.Context(), b.Round, b.File); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	} else if done != "" {
		writeErr(w, http.StatusConflict, "already applied: "+done+" is on its branch; change it there")
		return
	}
	after, err := s.st.UndecideFile(r.Context(), b.Round, b.File, b.Prints)
	if err != nil {
		fileErr(w, err)
		return
	}
	s.events.emit("decisions", map[string]int64{"round": b.Round})
	writeJSON(w, http.StatusOK, map[string]any{"files": filesOf(after)})
}

// putNote sets the note on a file's decision (file) or a backlog row's
// (id), and nothing else: it approves nothing, so it carries no print.
func (s *Server) putNote(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Round int64  `json:"round"`
		File  string `json:"file"`
		ID    string `json:"id"`
		Note  string `json:"note"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if (b.File == "") == (b.ID == "") {
		writeErr(w, http.StatusBadRequest, "a note is on one file or one row")
		return
	}
	if _, ok := s.current(w, r.Context(), b.Round); !ok {
		return
	}
	var err error
	if b.File != "" {
		if done, err := s.fileApplied(r.Context(), b.Round, b.File); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		} else if done != "" {
			writeErr(w, http.StatusConflict, "already applied: "+done+" is on its branch; change it there")
			return
		}
		err = s.st.NoteFile(r.Context(), b.Round, b.File, b.Note)
	} else {
		if !s.perItem(w, r.Context(), b.Round) {
			return
		}
		err = s.st.Note(r.Context(), b.Round, b.ID, b.Note)
	}
	switch {
	case errors.Is(err, store.ErrStale):
		writeErr(w, http.StatusConflict, "stale")
		return
	case errors.Is(err, store.ErrChanged):
		writeErr(w, http.StatusConflict, "changed: the decision this note was on is gone; look again")
		return
	case errors.Is(err, store.ErrNotReady):
		writeErr(w, http.StatusConflict, "the agent is still recommending")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.emit("decisions", map[string]int64{"round": b.Round})
	w.WriteHeader(http.StatusNoContent)
}

// base returns a round file's content at the audit, read back at its
// source and checked against its base hash.
func (s *Server) base(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	round, _ := strconv.ParseInt(q.Get("round"), 10, 64)
	if _, ok := s.current(w, r.Context(), round); !ok {
		return
	}
	items, err := s.st.Files(r.Context(), round)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, it := range items {
		if it.Key != q.Get("file") {
			continue
		}
		body, err := content.Read(r.Context(), it.File)
		if err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"file": it.Key, "content": body})
		return
	}
	writeErr(w, http.StatusNotFound, "no such file in the round")
}

// send marks sent the decisions the page shows (files by key, rows by id),
// each only while it is still the one in force: a decision made since Send
// was pressed waits for the next. The response names what it sent, and the
// page marks only those. A Send that names no decisions is refused.
func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Round int64                   `json:"round"`
		Files map[string]rec.Decision `json:"files"`
		Rows  map[string]row.Decision `json:"rows"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.Files == nil && b.Rows == nil {
		writeErr(w, http.StatusBadRequest, "a Send names the decisions the page shows (files, rows)")
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
	sd, err := s.st.Send(r.Context(), b.Round, rd.OwnerSession, &store.Shown{Files: b.Files, Rows: b.Rows})
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
		Sent  int      `json:"sent"`
		To    string   `json:"to"`
		Files []string `json:"files"`
		Rows  []string `json:"rows"`
	}{n, to, append([]string{}, sd.Files...), append([]string{}, sd.Rows...)})
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
	if src.File == "" && (src.Repo == "" || src.Path == "") {
		writeErr(w, http.StatusNotFound, "this row has no file")
		return
	}
	if src.FromDisk() {
		if why := content.Moved(src); why != "" {
			writeErr(w, http.StatusForbidden, why)
			return
		}
	}
	b, err := content.Raw(r.Context(), src, "")
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("can't read %s: %v", src.File, err))
		return
	}
	truncated := len(b) > content.Max
	if truncated {
		b = b[:content.Max]
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": src.File, "ref": src.Ref, "content": string(b), "truncated": truncated})
}
