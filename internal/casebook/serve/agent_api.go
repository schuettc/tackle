package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/recommend"
)

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
		s.publish(r.Context(), "sessions", map[string]string{"id": in.ID})
	}
	reply(w, nil, err)
}

// agentSessionInfo is pi-casebook telling serve what pi knows about a
// session that the channel can't: its name (sent at session start and on
// every rename) and its parent session, or that it ended (pi replaced it in
// its process, or quit). See deliver.SessionInfo.
func (s *Server) agentSessionInfo(w http.ResponseWriter, r *http.Request) {
	var in deliver.SessionInfo
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if in.ID == "" {
		reply(w, nil, bad("id required"))
		return
	}
	err := s.Queue.SetInfo(r.Context(), in)
	if err == nil {
		s.publish(r.Context(), "sessions", map[string]string{"id": in.ID})
	}
	reply(w, nil, err)
}

// userName names the person casebook works for in what serve says to an
// agent: the configured user (the login decisions are recorded "by"), or
// "the user" when none is configured.
func (s *Server) userName() string {
	if s.App != nil {
		if u := strings.TrimSpace(s.App.Cfg.User); u != "" {
			return u
		}
	}
	return "the user"
}

// userSubject is userName at the start of a sentence: a login keeps its
// case, "the user" becomes "The user".
func (s *Server) userSubject() string {
	if u := s.userName(); u != "the user" {
		return u
	}
	return "The user"
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
			p := fmt.Sprintf("%s accepted %d of your %d settled proposals", s.userSubject(), t.Accepted, n)
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
		parts = append(parts, fmt.Sprintf("%s made %d decision batch(es) directly", s.userSubject(), direct))
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
		out += fmt.Sprintf("\n- %s: you proposed %s; %s %s it: %s", p.Key, p.Disposition, s.userName(), p.State, reason)
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
			text := deliver.Render(*d, working, summary, s.App.Cfg.User, time.Local)
			// The messages are already marked 'delivered' in the DB (see
			// deliver.Queue.Next). Handing d to the HTTP response is the
			// moment the agent receives them; the delivery stays in-flight
			// until the agent settles or Court intervenes.
			s.publish(ctx, "delivery", map[string]any{"id": d.ID, "session": sess.ID, "state": deliver.InFlight, "messages": len(d.Messages)})
			reply(w, WaitView{Delivery: d, Text: text}, nil)
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
		Session  string           `json:"session"`
		IDs      []int64          `json:"ids"`
		State    string           `json:"state"`
		Text     string           `json:"text"`
		Attached deliver.Attached `json:"attached"`
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
	touched, skipped, err := s.Queue.Reply(ctx, in.Session, in.IDs, in.State, in.Text, in.Attached)
	if err != nil {
		if errors.Is(err, deliver.ErrNotFound) {
			reply(w, nil, httpError{code: http.StatusNotFound, msg: err.Error()})
		} else {
			reply(w, nil, bad("%v", err))
		}
		return
	}
	s.publish(ctx, "messages", map[string]any{"ids": touched, "state": in.State, "session": in.Session, "reply": in.Text != ""})
	s.wake(in.Session)
	settled := touched
	if settled == nil {
		settled = []int64{}
	}
	if skipped == nil {
		skipped = []deliver.SkippedMessage{}
	}
	reply(w, ReplyResult{Settled: settled, Skipped: skipped}, nil)
}

func (s *Server) agentPropose(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session     string   `json:"session"`
		Keys        []string `json:"keys"`
		Disposition string   `json:"disposition"`
		Until       string   `json:"until"`
		Note        string   `json:"note"`
		// FromNext marks a recommendation for an item casebook_next handed
		// out: it must carry a reason.
		FromNext bool `json:"from_next"`
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
	if in.FromNext && strings.TrimSpace(in.Note) == "" {
		reply(w, nil, bad("a recommendation needs a one-line reason in note"))
		return
	}
	// Proposals are only valid for items that exist in the casebook index and
	// are currently in attention (awaiting Court's decision). Agents receive
	// keys from casebook's own tools (casebook_show, casebook_status), so an
	// unknown key is a programming error
	// — the proposal would sit orphaned, invisible to Court.
	//
	// A wait/watch decision whose until has lapsed returns the item to
	// attention (StatusDue); such items are accepted by InAttention().
	var attentionKeys []string
	var msgs []string
	for _, raw := range in.Keys {
		k, err := item.ParseKey(raw)
		if err != nil {
			msgs = append(msgs, err.Error())
			continue
		}
		it, known := s.Index.Item(k.String())
		if !known {
			msgs = append(msgs, fmt.Sprintf("no item %s in casebook", k))
			continue
		}
		if !s.Index.InAttention(k.String()) {
			msgs = append(msgs, fmt.Sprintf("%s is not in attention: current status is %s", k, it.Status))
			continue
		}
		attentionKeys = append(attentionKeys, raw)
	}

	var ps []propose.Proposal
	if len(attentionKeys) > 0 {
		var propErrs []error
		ps, propErrs = s.Props.Propose(ctx, source(sess), attentionKeys, in.Disposition, in.Until, in.Note)
		for _, e := range propErrs {
			msgs = append(msgs, e.Error())
		}
	}
	if len(ps) > 0 {
		var ids []int64
		for _, p := range ps {
			ids = append(ids, p.ID)
		}
		s.publish(ctx, "proposals", map[string]any{"ids": ids, "state": "pending", "source": source(sess)})
	}
	reply(w, ProposeResult{Proposed: len(ps), Proposals: nonNil(ps), Errors: nonNil(msgs)}, nil)
}

// nextViews is the order casebook_next works through: the items most
// likely to need the user first.
var nextViews = []string{ViewWaiting, ViewDue, ViewNew}

// needsDecision lists the items that need a decision, in nextViews order
// and, within a view, the index's order. An item in two views is listed
// once, at the first.
func (s *Server) needsDecision(pending map[string]propose.Proposal) []ItemView {
	seen := map[string]bool{}
	var out []ItemView
	for _, view := range nextViews {
		for offset := 0; ; {
			page, total := s.Index.List(Query{View: view, Offset: offset, Limit: 500}, pending)
			for _, it := range page {
				if seen[it.ID] {
					continue
				}
				seen[it.ID] = true
				out = append(out, it)
			}
			offset += len(page)
			if len(page) == 0 || offset >= total {
				break
			}
		}
	}
	return out
}

// needsRecommendation lists the items that need a decision and have no
// pending proposal, in needsDecision's order: what casebook_next hands out.
func (s *Server) needsRecommendation(pending map[string]propose.Proposal) []ItemView {
	var out []ItemView
	for _, it := range s.needsDecision(pending) {
		if it.Proposal == nil {
			out = append(out, it)
		}
	}
	return out
}

// recommendCounts counts the items that need a decision with a pending
// proposal and without one (the summary's "n recommended · m not yet").
func (s *Server) recommendCounts(pending map[string]propose.Proposal) (recommended, notYet int) {
	for _, it := range s.needsDecision(pending) {
		if it.Proposal != nil {
			recommended++
		} else {
			notYet++
		}
	}
	return recommended, notYet
}

// agentNext is casebook_next: the next item that needs a recommendation,
// its detail, its kind's choices, Not now's conditions, the guide and how
// to answer; done when every item that needs a decision has a proposal.
// Two sessions asking at once may get the same item (the newer proposal
// replaces the older), but a decided item is never handed out: only the
// waiting, due and new views are read.
func (s *Server) agentNext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := s.session(ctx, r.URL.Query().Get("session")); err != nil {
		reply(w, nil, err)
		return
	}
	pending, err := s.Props.Pending(ctx)
	if err != nil {
		reply(w, nil, err)
		return
	}
	vocab := decisionVocab()
	kinds := make([]recommend.KindChoices, 0, len(vocab.Kinds))
	for _, k := range vocab.Kinds {
		kc := recommend.KindChoices{Kind: k.Kind}
		for _, c := range k.Choices {
			kc.Choices = append(kc.Choices, recommend.Choice{Disposition: c.Disposition, Label: c.Label, Outward: c.Outward, NeedsUntil: c.NeedsUntil})
		}
		kinds = append(kinds, kc)
	}
	v := NextView{Done: true, Choices: []ChoiceVocab{}, NotNow: nonNil(vocab.NotNow),
		Guide: recommend.Guide(kinds), ProposeHow: recommend.ProposeHow}
	left := s.needsRecommendation(pending)
	if len(left) > 0 {
		it := left[0].Item
		k, err := item.ParseKey(it.ID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		d := s.itemDetail(ctx, it, k)
		v.Done, v.Left, v.Item = false, len(left), &d
		for _, kv := range vocab.Kinds {
			if kv.Kind == string(it.Kind) {
				v.Choices = nonNil(kv.Choices)
			}
		}
	}
	reply(w, v, nil)
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
	s.publish(ctx, "evidence", e)
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
		s.publish(ctx, "progress", p)
	}
	// SetProgress now surfaces progress_log insert errors; reply returns them
	// to the agent's progress tool so the caller sees the failure.
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
	reply(w, StatusView{Counts: s.Index.Counts(pending), Since: s.summary(ctx, sess), PageOpen: s.PageOpen()}, nil)
}

// agentInterrupted is the channel asking, after it saw serve restart,
// whether this serve's start interrupted a delivery of ITS session: a
// restart is that session's business only then. Session-scoped like the
// other agent calls: an unknown session is 404 unknown_session, and only
// the session's own deliveries are ever listed.
func (s *Server) agentInterrupted(w http.ResponseWriter, r *http.Request) {
	sess, err := s.session(r.Context(), r.URL.Query().Get("session"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	v := InterruptedView{StartedAt: s.StartedAt(), Deliveries: []int64{}, Messages: []int64{}}
	for _, in := range s.interrupted[sess.ID] {
		v.Deliveries = append(v.Deliveries, in.Delivery)
		v.Messages = append(v.Messages, in.Messages...)
	}
	reply(w, v, nil)
}

// agentOpen is casebook_open: open the page in Court's browser.
// openViews are the routes casebook_open can land on besides an item: the
// Attention views and the board (spec §3.2).
var openViews = map[string]bool{ViewWaiting: true, ViewNew: true, ViewDue: true, ViewProposed: true, ViewAll: true, "board": true}

// agentOpen is casebook_open: open the page in Court's browser, at one item
// (key), at an Attention view (view), or at the front (neither).
//
// The page belongs to the session that opened it (galley's rule: an editor
// belongs to the one session that opened it). The channel sends its session,
// and the page opens attached to it: the URL carries ?session=<id>, so a
// reload keeps it. A channel with no session opens a page that belongs to
// no one; the page then asks Court to choose.
func (s *Server) agentOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string `json:"session"`
		Key     string `json:"key"`
		View    string `json:"view"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if in.Session != "" {
		if _, err := s.session(r.Context(), in.Session); err != nil {
			reply(w, nil, err)
			return
		}
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
	s.mu.Lock()
	openFn := s.openPage
	s.mu.Unlock()
	if openFn == nil {
		reply(w, nil, httpError{code: http.StatusConflict, msg: "this casebook serve can't open a browser"})
		return
	}
	if err := openFn(SessionQuery(in.Session) + fragment); err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, OpenResult{Opened: fragment, Session: in.Session}, nil)
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
	worked, lines, _ := s.Props.ClearProgress(ctx, in.Session)
	result := SettledResult{Session: in.Session, WorkedMs: worked.Milliseconds()}
	if d != nil {
		id := d.ID
		result.Delivery = &id
	}
	// If the turn had progress lines, record a 'worked' message in the thread
	// so the page can show the history after a reload (spec §3.5).
	if len(lines) > 0 {
		if thID, ok := s.currentThread(ctx, in.Session, d); ok {
			wv := WorkedView{DurationMs: worked.Milliseconds(), Lines: lines}
			s.postWorkedMessage(ctx, in.Session, thID, worked, wv)
		}
	}
	ev := map[string]any{"session": in.Session, "worked_ms": result.WorkedMs}
	if result.Delivery != nil {
		ev["delivery"] = *result.Delivery
	}
	s.publish(ctx, "settled", ev)
	s.wake(in.Session)
	// Prune the waiter: the turn is over. The next agentWait creates a fresh
	// channel, so the map stays bounded to sessions that are actively waiting.
	s.mu.Lock()
	delete(s.waiters, in.Session)
	s.mu.Unlock()
	reply(w, result, nil)
}

// currentThread returns the thread ID for recording a worked message:
// the settled delivery's first message's thread, or the session's most
// recent thread. Returns (0, false) when the session has no thread.
func (s *Server) currentThread(ctx context.Context, sessionID string, d *deliver.Delivery) (int64, bool) {
	if d != nil && len(d.Messages) > 0 {
		return d.Messages[0].ThreadID, true
	}
	threads, err := s.Queue.Threads(ctx, sessionID)
	if err != nil || len(threads) == 0 {
		return 0, false
	}
	return threads[len(threads)-1].ID, true
}

// workedBody formats a duration as "worked for Xm Ys" (spec §3.5).
func workedBody(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("worked for %dh %dm %ds", h, m, sec)
	case m > 0:
		return fmt.Sprintf("worked for %dm %ds", m, sec)
	default:
		return fmt.Sprintf("worked for %ds", sec)
	}
}

// postWorkedMessage inserts a 'worked' message into thread thID and publishes
// the messages/thread bus events.  Failures are logged to stderr as
// diagnostics (they are non-fatal: the turn has already settled).
func (s *Server) postWorkedMessage(ctx context.Context, sessionID string, thID int64, dur time.Duration, wv WorkedView) {
	wj, err := json.Marshal(wv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "casebook serve: postWorkedMessage marshal: %v\n", err)
		return
	}
	msg, err := s.Queue.PostWorked(ctx, thID, sessionID, workedBody(dur), string(wj))
	if err != nil {
		fmt.Fprintf(os.Stderr, "casebook serve: postWorkedMessage PostWorked: %v\n", err)
		return
	}
	s.publish(ctx, "messages", map[string]any{"ids": []int64{msg.ID}, "thread": thID, "session": sessionID})
	s.publish(ctx, "thread", map[string]any{"id": thID, "session": sessionID})
}

// jobForSession validates that the job exists and is owned by the given
// session, returning 404 for unknown jobs and 403 when the session doesn't
// match. Only the session the job was approved for may call job-step or
// job-ask (spec §6.5).
func (s *Server) jobForSession(ctx context.Context, jobID int64, sessionID string) (apply.Job, error) {
	job, err := s.Apply.Get(ctx, jobID)
	if err != nil {
		return apply.Job{}, httpError{code: http.StatusNotFound, msg: err.Error()}
	}
	if job.Session != sessionID {
		return apply.Job{}, httpError{code: http.StatusForbidden,
			msg: "session " + sessionID + " is not approved for job " + strconv.FormatInt(jobID, 10)}
	}
	return job, nil
}

// agentJobStep is POST /api/agent/job-step: the agent reports a step's
// progress (started, reported, paused, or failed). Only the session the job
// was approved for may call this endpoint.
//
//   - started:  pending → running
//   - reported: running → reported, then verified by a fresh gh observation
//   - paused:   running → paused; opens a needs-you card of kind "paused"
//   - failed:   running → failed;  opens a needs-you card of kind "failed"
func (s *Server) agentJobStep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string `json:"session"`
		Job     int64  `json:"job"`
		Step    int64  `json:"step"`
		State   string `json:"state"`
		Detail  string `json:"detail"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()

	switch in.State {
	case "started", "reported", "paused", "failed":
	default:
		reply(w, nil, bad("state must be started, reported, paused, or failed"))
		return
	}

	if _, err := s.session(ctx, in.Session); err != nil {
		reply(w, nil, err)
		return
	}
	job, err := s.jobForSession(ctx, in.Job, in.Session)
	if err != nil {
		reply(w, nil, err)
		return
	}

	// Fix 4: Only approved, running, or paused jobs can accept step reports.
	switch job.State {
	case apply.JobApproved, apply.JobRunning, apply.JobPaused:
		// ok
	default:
		reply(w, nil, httpError{code: http.StatusConflict,
			msg: fmt.Sprintf("job %d is %s: cannot accept step updates", job.ID, job.State)})
		return
	}

	// Find the step in the job.
	var step apply.JobStep
	var found bool
	for _, st := range job.Steps {
		if st.ID == in.Step {
			step = st
			found = true
			break
		}
	}
	if !found {
		reply(w, nil, httpError{code: http.StatusNotFound,
			msg: fmt.Sprintf("step %d not found in job %d", in.Step, in.Job)})
		return
	}

	switch in.State {
	case "started":
		// Fix 4: atomically start the step and move an approved job to running.
		if err := s.Apply.StartStepWithJob(ctx, job.ID, step.ID); err != nil {
			reply(w, nil, bad("%v", err))
			return
		}
		s.publish(ctx, "step", map[string]any{"id": step.ID, "job_id": in.Job, "state": apply.StepRunning})

	case "reported":
		if err := s.Apply.SetStepState(ctx, step.ID, apply.StepReported, ""); err != nil {
			reply(w, nil, bad("%v", err))
			return
		}
		s.publish(ctx, "step", map[string]any{"id": step.ID, "job_id": in.Job, "state": apply.StepReported})
		// Verify the outcome with a fresh gh read.
		obs := apply.ObserveGh(ctx, step, s.App.Gh)
		verState := apply.Verify(step, obs)
		if verState != apply.StepReported { // StepReported means inconclusive: leave as-is
			detail := ""
			if verState == apply.StepFailed {
				detail = apply.DetailDrift
			}
			if err := s.Apply.SetStepState(ctx, step.ID, verState, detail); err == nil {
				s.publish(ctx, "step", map[string]any{"id": step.ID, "job_id": in.Job, "state": verState})
			}
		}
		// Check whether the job has reached a terminal state after this report.
		s.settleJob(ctx, job.ID)

	case "paused":
		// Fix 2: atomic step state + card.
		ny, err := s.Apply.PauseStepWithCard(ctx, step.ID, apply.StepPaused, "paused", in.Detail)
		if err != nil {
			reply(w, nil, bad("%v", err))
			return
		}
		s.publish(ctx, "step", map[string]any{"id": step.ID, "job_id": in.Job, "state": apply.StepPaused})
		s.publish(ctx, "needs_you", ny)

	case "failed":
		// Fix 2: atomic step state + card.
		ny, err := s.Apply.PauseStepWithCard(ctx, step.ID, apply.StepFailed, "failed", in.Detail)
		if err != nil {
			reply(w, nil, bad("%v", err))
			return
		}
		s.publish(ctx, "step", map[string]any{"id": step.ID, "job_id": in.Job, "state": apply.StepFailed})
		s.publish(ctx, "needs_you", ny)
		// A failed step with an open card does not immediately end the job, but
		// trigger Settle so it can detect completion if all other steps are done.
		s.settleJob(ctx, job.ID)
	}

	reply(w, JobStepResult{JobID: in.Job, StepID: step.ID, State: in.State}, nil)
}

// agentJobAsk is POST /api/agent/job-ask: the agent drafts public text or
// asks a question, opening a needs-you card so Court can review and answer it.
// Nothing is posted until Court approves (spec §5.4, Court 2026-09-27).
func (s *Server) agentJobAsk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session  string `json:"session"`
		Job      int64  `json:"job"`
		Step     int64  `json:"step"`
		Question string `json:"question"`
		Text     string `json:"text"`
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
	jobForAsk, err := s.jobForSession(ctx, in.Job, in.Session)
	if err != nil {
		reply(w, nil, err)
		return
	}

	// Fix 4: Only approved, running, or paused jobs can accept job-ask.
	switch jobForAsk.State {
	case apply.JobApproved, apply.JobRunning, apply.JobPaused:
		// ok
	default:
		reply(w, nil, httpError{code: http.StatusConflict,
			msg: fmt.Sprintf("job %d is %s: cannot accept job-ask", jobForAsk.ID, jobForAsk.State)})
		return
	}

	ny, err := s.Apply.OpenNeedsYou(ctx, in.Job, in.Step, "text", in.Question, in.Text)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	s.publish(ctx, "needs_you", ny)
	reply(w, JobAskResult{NeedsYou: ny}, nil)
}

// dispatchAgentJob enqueues the job as a delivery to session through
// deliver.Queue (spec §6.5). The message body describes the job's agent-lane
// steps, the preconditions the agent must re-check, and the confirmation
// protocol for steps that post public text. Attached.Job carries the job id
// so the agent can reference it in job-step and job-ask calls.
func (s *Server) dispatchAgentJob(ctx context.Context, job apply.Job, session string) error {
	// Create a dedicated thread for this job.
	thread, err := s.Queue.NewThread(ctx, session, "job:"+strconv.FormatInt(job.ID, 10))
	if err != nil {
		return fmt.Errorf("dispatchAgentJob: create thread: %w", err)
	}

	body, err := buildJobBody(job, s.userName())
	if err != nil {
		return fmt.Errorf("dispatchAgentJob: %w", err)
	}
	att := deliver.Attached{Job: strconv.FormatInt(job.ID, 10)}
	if _, err := s.Queue.Post(ctx, thread.ID, body, att, false); err != nil {
		return fmt.Errorf("dispatchAgentJob: post: %w", err)
	}
	s.wake(session)
	return nil
}

// buildJobBody formats the delivery message body for an agent-lane job.
// It lists every agent-lane step with its command and precondition, calls out
// steps that post public text (requiring casebook_job_ask before posting), and
// describes the protocol for working the job with casebook_job_step.
func buildJobBody(job apply.Job, user string) (string, error) {
	var b strings.Builder

	// Count agent-lane steps.
	var agentSteps []apply.JobStep
	for _, st := range job.Steps {
		if st.Lane == apply.LaneAgent {
			agentSteps = append(agentSteps, st)
		}
	}

	fmt.Fprintf(&b, "casebook apply job %d — %d agent-lane step(s) to execute.\n",
		job.ID, len(agentSteps))
	b.WriteString("Work through each step in order. Check preconditions live before running.\n")

	// The batch is not confirmed until Court says so. The agent must not touch
	// the world until then, and must do nothing at all if Court skips it.
	b.WriteString("\nWAIT FOR THE BATCH:\n")
	fmt.Fprintf(&b, "Do not run any step until %s confirms the batch.\n", user)
	b.WriteString("The confirmation arrives as a message in this thread.\n")
	fmt.Fprintf(&b, "If %s skips the batch, run nothing.\n", user)

	// List agent-lane steps.
	if len(agentSteps) > 0 {
		b.WriteString("\nSTEPS:\n")
	}
	for _, st := range agentSteps {
		fmt.Fprintf(&b, "[s-%d] %s · %s\n", st.ID, st.Key, st.Action)
		fmt.Fprintf(&b, "  command: %s\n", st.Command)
		if st.Precondition != "" {
			desc, err := describePrecondition(st.Precondition, st.Key)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "  precondition: %s\n", desc)
		}
		if st.Posts {
			fmt.Fprintf(&b, "  ⚠ posts public text: draft text first with casebook_job_ask; wait for %s's approval before running\n", user)
		}
	}

	// Protocol.
	b.WriteString("\nPROTOCOL for each step:\n")
	fmt.Fprintf(&b, "  1. casebook_job_step(job=%d, step=<step_id>, state=\"started\")\n", job.ID)
	b.WriteString("  2. [if posts=true] casebook_job_ask(job=<id>, step=<step_id>, text=\"<your draft>\")\n")
	fmt.Fprintf(&b, "     Wait for %s's answer (a message in this thread with the approved text).\n", user)
	b.WriteString("     Run the command with exactly that text.\n")
	fmt.Fprintf(&b, "  3. casebook_job_step(job=%d, step=<step_id>, state=\"reported\") on success\n", job.ID)
	fmt.Fprintf(&b, "  4. casebook_job_step(job=%d, step=<step_id>, state=\"paused\", detail=\"reason\") if precondition fails\n", job.ID)
	fmt.Fprintf(&b, "  5. casebook_job_step(job=%d, step=<step_id>, state=\"failed\", detail=\"reason\") on error\n", job.ID)
	b.WriteString("casebook verifies each reported step's outcome by a fresh observation.\n")

	return b.String(), nil
}

// describePrecondition translates a machine precondition name into a human
// description the agent can act on, including the live check to perform.
// An unknown precondition name is a programming error: a bare token would be
// sent to the agent, which would silently skip a real check. An error is
// returned so buildJobBody and dispatchAgentJob can refuse to dispatch.
func describePrecondition(precondition, key string) (string, error) {
	switch precondition {
	case "pr-no-new-activity":
		return precondition + " (run: gh pr view <num> -R <repo> --json updatedAt; confirm updatedAt ≤ decision time)", nil
	case "repo-no-open-human-prs":
		return precondition + " (run: gh pr list -R <repo> --state open --json author; confirm no human authors)", nil
	}
	return "", fmt.Errorf("unknown precondition %q for step %s: add a description in describePrecondition before dispatching", precondition, key)
}
