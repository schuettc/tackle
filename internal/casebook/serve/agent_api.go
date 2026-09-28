package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/item"
)

func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// source names a session as a proposer or author: "<harness>:<id>".
func source(sess deliver.Session) string {
	h := sess.Harness
	if h == "" {
		h = "agent"
	}
	return h + ":" + sess.ID
}

func (s *Server) session(ctx context.Context, id string) (deliver.Session, error) {
	if id == "" {
		return deliver.Session{}, bad("session required")
	}
	sess, err := s.Queue.Session(ctx, id)
	if err != nil {
		return sess, httpError{code: http.StatusNotFound, msg: fmt.Sprintf("unknown session %q (register with presence first)", id), errCode: "unknown_session"}
	}
	return sess, nil
}

func (s *Server) agentPresence(w http.ResponseWriter, r *http.Request) {
	var in deliver.Session
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if in.ID == "" {
		reply(w, nil, bad("id required"))
		return
	}
	err := s.Queue.Touch(r.Context(), deliver.Session{ID: in.ID, Harness: in.Harness, Label: in.Label, CWD: in.CWD, PID: in.PID})
	if err == nil {
		s.Bus.Publish(r.Context(), "sessions", map[string]string{"id": in.ID})
	}
	reply(w, nil, err)
}

// overruledShown caps the proposals listed one by one in a since-summary.
const overruledShown = 10

// summary is the "since you last looked" paragraph for a session: accepted
// proposals as a count, every changed or rejected one with Court's reason (up
// to overruledShown, then how many more), and direct decisions as a count.
func (s *Server) summary(ctx context.Context, sess deliver.Session) string {
	since := sess.LookedAt
	var parts []string
	if t, err := s.Props.Tally(ctx, source(sess), since); err == nil {
		if n := t.Accepted + t.Changed + t.Rejected; n > 0 {
			p := fmt.Sprintf("Court accepted %d of your %d settled proposals", t.Accepted, n)
			if t.Changed > 0 {
				p += fmt.Sprintf(", changed %d", t.Changed)
			}
			if t.Rejected > 0 {
				p += fmt.Sprintf(", rejected %d", t.Rejected)
			}
			parts = append(parts, p)
		}
	}
	var direct int
	_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE kind = 'decided' AND created_at >= ?
		AND json_extract(payload, '$.proposed_by') = ''`, since.UnixMilli()).Scan(&direct)
	if direct > 0 {
		parts = append(parts, fmt.Sprintf("Court made %d decision batch(es) directly", direct))
	}
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, "; ") + "."
	over, total, _ := s.Props.Overruled(ctx, source(sess), since, overruledShown)
	for _, p := range over {
		reason := p.Reason
		if reason == "" {
			reason = "no reason given"
		}
		out += fmt.Sprintf("\n- %s: you proposed %s; Court %s it: %s", p.Key, p.Disposition, p.State, reason)
	}
	if total > len(over) {
		out += fmt.Sprintf("\n- and %d more (casebook_show an item for the rest)", total-len(over))
	}
	return out
}

// agentWait is the channel's long-poll: it returns the session's next
// delivery (rendered for the agent) or 204 when none arrives in time.
func (s *Server) agentWait(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.session(ctx, r.URL.Query().Get("session"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	limit := s.Wait
	if sec := atoi(r.URL.Query().Get("timeout")); sec > 0 && time.Duration(sec)*time.Second < limit {
		limit = time.Duration(sec) * time.Second
	}
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	wake := s.waiter(sess.ID)
	for {
		summary := s.summary(ctx, sess)
		d, err := s.Queue.Next(ctx, sess.ID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if d != nil {
			working := ""
			if prev, _ := s.Queue.Previous(ctx, sess.ID, d.ID); prev != nil && len(prev.Messages) > 0 && len(d.Messages) > 0 &&
				!prev.FinishedAt.IsZero() && d.Messages[0].QueuedAt.Before(prev.FinishedAt) {
				working = prev.Messages[0].Body
			}
			text := deliver.Render(*d, working, summary, time.Local)
			// The messages are already marked 'delivered' in the DB (see
			// deliver.Queue.Next). Handing d to the HTTP response is the
			// moment the agent receives them; the delivery stays in-flight
			// until the agent settles or Court intervenes.
			s.Bus.Publish(ctx, "delivery", map[string]any{"id": d.ID, "session": sess.ID, "state": deliver.InFlight, "messages": len(d.Messages)})
			reply(w, map[string]any{"delivery": d, "text": text}, nil)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-wake:
		}
	}
}

func (s *Server) agentReply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string  `json:"session"`
		IDs     []int64 `json:"ids"`
		State   string  `json:"state"`
		Text    string  `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	if _, err := s.session(ctx, in.Session); err != nil {
		reply(w, nil, err)
		return
	}
	touched, skipped, err := s.Queue.Reply(ctx, in.Session, in.IDs, in.State, in.Text)
	if err != nil {
		if errors.Is(err, deliver.ErrNotFound) {
			reply(w, nil, httpError{code: http.StatusNotFound, msg: err.Error()})
		} else {
			reply(w, nil, bad("%v", err))
		}
		return
	}
	s.Bus.Publish(ctx, "messages", map[string]any{"ids": touched, "state": in.State, "session": in.Session, "reply": in.Text != ""})
	s.wake(in.Session)
	settled := touched
	if settled == nil {
		settled = []int64{}
	}
	if skipped == nil {
		skipped = []deliver.SkippedMessage{}
	}
	reply(w, map[string]any{"settled": settled, "skipped": skipped}, nil)
}

func (s *Server) agentPropose(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session     string   `json:"session"`
		Keys        []string `json:"keys"`
		Disposition string   `json:"disposition"`
		Until       string   `json:"until"`
		Note        string   `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	sess, err := s.session(ctx, in.Session)
	if err != nil {
		reply(w, nil, err)
		return
	}
	ps, errs := s.Props.Propose(ctx, source(sess), in.Keys, in.Disposition, in.Until, in.Note)
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	if len(ps) > 0 {
		var ids []int64
		for _, p := range ps {
			ids = append(ids, p.ID)
		}
		s.Bus.Publish(ctx, "proposals", map[string]any{"ids": ids, "state": "pending", "source": source(sess)})
	}
	reply(w, map[string]any{"proposed": len(ps), "proposals": nonNil(ps), "errors": nonNil(msgs)}, nil)
}

func (s *Server) agentEvidence(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string `json:"session"`
		Key     string `json:"key"`
		Text    string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	sess, err := s.session(ctx, in.Session)
	if err != nil {
		reply(w, nil, err)
		return
	}
	e, err := s.Props.AddEvidence(ctx, in.Key, in.Text, source(sess))
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	s.Bus.Publish(ctx, "evidence", e)
	reply(w, e, nil)
}

func (s *Server) agentProgress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string `json:"session"`
		Text    string `json:"text"`
		N       int    `json:"n"`
		Total   int    `json:"total"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	if _, err := s.session(ctx, in.Session); err != nil {
		reply(w, nil, err)
		return
	}
	p, err := s.Props.SetProgress(ctx, in.Session, in.Text, in.N, in.Total)
	if err == nil {
		s.Bus.Publish(ctx, "progress", p)
	}
	reply(w, p, err)
}

// agentStatus is casebook_status: the attention counts, whether the page is
// open, and what changed since the session last looked. It carries no live
// page context; what Court was looking at travels with each message.
func (s *Server) agentStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess, err := s.session(ctx, r.URL.Query().Get("session"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	pending, _ := s.Props.Pending(ctx)
	reply(w, map[string]any{"counts": s.Index.Counts(pending), "since": s.summary(ctx, sess), "page_open": s.PageOpen()}, nil)
}

// agentOpen is casebook_open: open the page in Court's browser.
// openViews are the routes casebook_open can land on besides an item: the
// Attention views and the board (spec §3.2).
var openViews = map[string]bool{ViewWaiting: true, ViewNew: true, ViewDue: true, ViewProposed: true, ViewAll: true, "board": true}

// agentOpen is casebook_open: open the page in Court's browser, at one item
// (key), at an Attention view (view), or at the front (neither).
func (s *Server) agentOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key  string `json:"key"`
		View string `json:"view"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	fragment := ""
	switch {
	case in.Key != "" && in.View != "":
		reply(w, nil, bad("give a key or a view, not both"))
		return
	case in.Key != "":
		k, err := item.ParseKey(in.Key)
		if err != nil {
			reply(w, nil, bad("%v", err))
			return
		}
		if _, ok := s.Index.Item(k.String()); !ok {
			reply(w, nil, httpError{code: http.StatusNotFound, msg: fmt.Sprintf("%s is not a known item", k)})
			return
		}
		fragment = "#/item/" + url.PathEscape(k.String())
	case in.View != "":
		if !openViews[in.View] {
			reply(w, nil, bad("unknown view %q (waiting, new, due, proposed, all or board)", in.View))
			return
		}
		fragment = "#/attention/" + in.View
	}
	if s.openPage == nil {
		reply(w, nil, httpError{code: http.StatusConflict, msg: "this casebook serve can't open a browser"})
		return
	}
	if err := s.openPage(fragment); err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, map[string]string{"opened": fragment}, nil)
}

func (s *Server) agentSettled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string  `json:"session"`
		Shown   []int64 `json:"shown"` // delivery ids the agent was shown this turn
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	if _, err := s.session(ctx, in.Session); err != nil {
		reply(w, nil, err)
		return
	}
	d, err := s.Queue.Settled(ctx, in.Session, in.Shown)
	if err != nil {
		reply(w, nil, err)
		return
	}
	worked, _ := s.Props.ClearProgress(ctx, in.Session)
	ev := map[string]any{"session": in.Session, "worked_ms": worked.Milliseconds()}
	if d != nil {
		ev["delivery"] = d.ID
	}
	s.Bus.Publish(ctx, "settled", ev)
	s.wake(in.Session)
	reply(w, ev, nil)
}
