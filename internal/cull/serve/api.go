package serve

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/schuettc/tackle/internal/cull/store"
)

// Handler is the /api/ mux (mounted by localweb, which checks the token).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/review", s.review)
	mux.HandleFunc("PUT /api/answers", s.putAnswers)
	mux.HandleFunc("DELETE /api/answers", s.deleteAnswer)
	mux.HandleFunc("POST /api/send", s.send)
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

// project resolves an id from text; ok is false after writing the error.
func (s *Server) project(w http.ResponseWriter, r *http.Request, v string) (store.Project, bool) {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no project")
		return store.Project{}, false
	}
	p, err := s.st.ProjectByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no project")
		return store.Project{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return store.Project{}, false
	}
	return p, true
}

type answerJSON struct {
	Value      string `json:"value"`
	Note       string `json:"note"`
	Via        string `json:"via"`
	Blind      bool   `json:"blind"`
	AnsweredAt string `json:"answered_at,omitempty"`
	SentAt     string `json:"sent_at,omitempty"`
}

type itemJSON struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Hash    string          `json:"hash"`
	File    string          `json:"file"`
	Name    string          `json:"name"`
	Verdict string          `json:"verdict"`
	Rule    string          `json:"rule"`
	Model   string          `json:"model"`
	State   json.RawMessage `json:"state"`
	Jev     json.RawMessage `json:"jev"`
	Rows    [][]string      `json:"rows,omitempty"`
	Members []string        `json:"members,omitempty"`
	Answer  *answerJSON     `json:"answer,omitempty"`
}

func rfc(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// actProb is the probability of the acting verdict (cut for tests,
// consolidate for groups), 0 when missing.
func actProb(it store.Item) float64 {
	var v struct {
		Verdict struct {
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"verdict"`
	}
	if len(it.Jev) == 0 || json.Unmarshal(it.Jev, &v) != nil {
		return 0
	}
	if it.Kind == "group" {
		return v.Verdict.Probabilities["consolidate"]
	}
	return v.Verdict.Probabilities["cut"]
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	// Read the cursor before any review data: an event racing the reads is
	// redelivered to the client, never skipped.
	cursor := s.events.format(s.events.latest())
	p, ok := s.project(w, r, r.URL.Query().Get("project"))
	if !ok {
		return
	}
	proj := map[string]any{"id": p.ID, "root": p.Root}
	run, items, err := s.st.LatestRun(r.Context(), p.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"project": proj, "run": nil, "items": []itemJSON{}, "blind": false, "cursor": cursor})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	answers, err := s.st.Answers(r.Context(), p.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Kind != b.Kind {
			return a.Kind == "test"
		}
		if pa, pb := actProb(a), actProb(b); pa != pb {
			return pa > pb
		}
		return a.ID < b.ID
	})
	sent := 0
	for _, a := range answers {
		if !a.SentAt.IsZero() {
			sent++
		}
	}
	out := make([]itemJSON, 0, len(items))
	for _, it := range items {
		j := itemJSON{ID: it.ID, Kind: it.Kind, Hash: it.Hash, File: it.File, Name: it.Name, Verdict: it.Verdict,
			Rule: it.Rule, Model: it.Model, State: it.State, Jev: it.Jev, Rows: it.Rows, Members: it.Members}
		if a, ok := answers[store.Key{ItemID: it.ID, Hash: it.Hash}]; ok {
			j.Answer = &answerJSON{Value: a.Value, Note: a.Note, Via: a.Via, Blind: a.Blind,
				AnsweredAt: rfc(a.AnsweredAt), SentAt: rfc(a.SentAt)}
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": proj,
		"run": map[string]any{"id": run.ID, "at": rfc(run.At), "mode": run.Mode, "base": run.Base,
			"summary": run.Summary, "total": run.Total},
		"items":    out,
		"blind":    false,
		"cursor":   cursor,
		"answered": map[string]int{"total": len(answers), "sent": sent},
	})
}

type putBody struct {
	Project int64 `json:"project"`
	Run     int64 `json:"run"`
	Answers []struct {
		ID    string `json:"id"`
		Hash  string `json:"hash"`
		Kind  string `json:"kind"`
		Value string `json:"value"`
		Note  string `json:"note"`
		Via   string `json:"via"`
		Blind bool   `json:"blind"`
	} `json:"answers"`
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad request body: %w", err)
	}
	return nil
}

func (s *Server) putAnswers(w http.ResponseWriter, r *http.Request) {
	var b putBody
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.Project == 0 || b.Run == 0 {
		writeErr(w, http.StatusBadRequest, "project and run are required")
		return
	}
	if len(b.Answers) == 0 {
		writeErr(w, http.StatusBadRequest, "no answers")
		return
	}
	valid := map[string]map[string]bool{
		"test":  {"cut": true, "keep": true},
		"group": {"merge": true, "separate": true},
	}
	for _, a := range b.Answers {
		switch {
		case a.ID == "" || a.Hash == "":
			writeErr(w, http.StatusBadRequest, "answer needs id and hash")
			return
		case !valid[a.Kind][a.Value]:
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("invalid value %q for kind %q", a.Value, a.Kind))
			return
		case a.Via != "item" && a.Via != "group":
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("invalid via %q", a.Via))
			return
		}
	}
	p, ok := s.project(w, r, strconv.FormatInt(b.Project, 10))
	if !ok {
		return
	}
	run, items, err := s.st.LatestRun(r.Context(), p.ID)
	// b.Run is required (it says which run the client was looking at) but
	// answers are checked against the latest run: an item that is unchanged
	// there (same id and hash) is still a valid answer from an older tab.
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusConflict, "stale")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	byKey := map[store.Key]store.Item{}
	for _, it := range items {
		byKey[store.Key{ItemID: it.ID, Hash: it.Hash}] = it
	}
	as := make([]store.Answer, 0, len(b.Answers))
	for _, a := range b.Answers {
		it, ok := byKey[store.Key{ItemID: a.ID, Hash: a.Hash}]
		if !ok || it.Kind != a.Kind {
			writeErr(w, http.StatusConflict, "stale")
			return
		}
		as = append(as, store.Answer{ItemID: a.ID, Hash: a.Hash, Kind: a.Kind, Value: a.Value, Note: a.Note, Via: a.Via,
			Blind: a.Blind, Jev: it.Jev, Model: it.Model, QuestionsHash: run.QuestionsHash})
	}
	err = s.st.SaveAnswers(r.Context(), p.ID, run.ID, as)
	if errors.Is(err, store.ErrStale) {
		writeErr(w, http.StatusConflict, "stale")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.emit("answers", map[string]int64{"project": p.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAnswer(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, ok := s.project(w, r, q.Get("project"))
	if !ok {
		return
	}
	if q.Get("id") == "" || q.Get("hash") == "" {
		writeErr(w, http.StatusBadRequest, "id and hash are required")
		return
	}
	if err := s.st.DeleteAnswer(r.Context(), p.ID, store.Key{ItemID: q.Get("id"), Hash: q.Get("hash")}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.events.emit("answers", map[string]int64{"project": p.ID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Project int64 `json:"project"`
	}
	if err := decode(r, &b); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p, ok := s.project(w, r, strconv.FormatInt(b.Project, 10))
	if !ok {
		return
	}
	n, err := s.st.MarkSent(r.Context(), p.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n > 0 {
		s.events.emit("answers", map[string]int64{"project": p.ID})
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}
