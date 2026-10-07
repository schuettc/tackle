package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/propose"
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func (s *Server) getSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// The head first: an event published while this answer is made is
	// after it, so a page that starts its stream here hears it.
	cursor, err := s.Bus.Head(ctx)
	if err != nil {
		reply(w, nil, err)
		return
	}
	pending, err := s.Props.Pending(ctx)
	if err != nil {
		reply(w, nil, err)
		return
	}
	queued, pushErr := s.pushState(ctx)
	var synced time.Time
	if fi, err := os.Stat(config.CachePath()); err == nil {
		synced = fi.ModTime().UTC()
	}
	sessions, _ := s.Queue.Sessions(ctx)
	recommended, notYet := s.recommendCounts(pending)
	reply(w, SummaryView{
		Machine:        s.App.Cfg.Machine,
		User:           s.App.Cfg.User,
		Head:           s.Index.Head(),
		BuiltAt:        s.Index.BuiltAt(),
		SyncedAt:       synced,
		OfflineQueued:  queued,
		PushError:      pushErr,
		Counts:         s.Index.Counts(pending),
		Notices:        s.Index.Notices(),
		Sessions:       len(sessions),
		SyncIntervalMS: s.syncIntervalFor().Milliseconds(),
		Syncing:        s.Syncing(),
		Recommended:    recommended,
		NotRecommended: notYet,
		Cursor:         cursor,
	}, nil)
}

func (s *Server) getItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx := r.Context()
	pending, err := s.Props.Pending(ctx)
	if err != nil {
		reply(w, nil, err)
		return
	}
	view := q.Get("view")
	if view == "" {
		view = ViewAll
	}
	query := Query{
		View:     view,
		Kind:     q.Get("kind"),
		Repo:     q.Get("repo"),
		Text:     q.Get("q"),
		Relation: q.Get("relation"),
		Bot:      q.Get("bot"),
		Age:      q.Get("age"),
		Rule:     q.Get("rule"),
		Now:      s.Now(),
		Offset:   atoi(q.Get("offset")),
		Limit:    atoi(q.Get("limit")),
	}
	// Precompute rule match set if requested.
	if query.Rule != "" {
		ru, err := s.App.Repo.ReadRule(query.Rule)
		if err == nil && ru != nil {
			ms, _ := ru.MatchAll(s.Index.Result(), s.Now())
			set := make(map[string]bool, len(ms))
			for _, m := range ms {
				set[m.Key] = true
			}
			query.MatchSet = set
		} else {
			// Rule not found or error: match nothing.
			query.MatchSet = map[string]bool{}
		}
	}
	items, total := s.Index.List(query, pending)
	if items == nil {
		items = []ItemView{}
	}
	group := query
	group.Offset = 0
	left, leftTotal := s.Index.LeftOpen(group, pending)
	reply(w, ItemsView{Total: total, Items: items, LeftOpen: nonNil(left), LeftOpenTotal: leftTotal}, nil)
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	k, err := item.ParseKey(r.URL.Query().Get("key"))
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	it, ok := s.Index.Item(k.String())
	if !ok {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: k.String() + " is not a known item"})
		return
	}
	reply(w, s.itemDetail(ctx, it, k), nil)
}

// itemDetail is one item with its pending proposal, its proposals, evidence,
// recent history (the last 50 events) and decision log: GET /api/item and
// casebook_next.
func (s *Server) itemDetail(ctx context.Context, it engine.Item, k item.Key) ItemDetailView {
	var prop *propose.Proposal
	pending, _ := s.Props.Pending(ctx)
	if p, ok := pending[it.ID]; ok {
		prop = &p
	}
	v := newItemView(it, prop)
	proposals, _ := s.Props.ForKey(ctx, it.ID)
	evidence, _ := s.Props.Evidence(ctx, it.ID)
	events, _ := s.App.Events()
	hist := engine.History(events, k)
	if len(hist) > 50 {
		hist = hist[len(hist)-50:]
	}
	log, _ := s.App.Repo.Log(ctx, k.File())
	return ItemDetailView{
		Item:      v,
		Proposals: nonNil(proposals),
		Evidence:  nonNil(evidence),
		History:   nonNil(hist),
		Decisions: nonNil(log),
	}
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// decideOneKey records one decision with NoPush, supersedes other pending
// proposals for the key except keep (the proposal being accepted), and returns
// the normalized key and the decision it committed. By defaults to
// s.App.Cfg.User.
func (s *Server) decideOneKey(ctx context.Context, key, disposition string, o app.DecideOptions, keep int64) (string, Committed, error) {
	o.NoPush = true
	if o.By == "" {
		o.By = s.App.Cfg.User
	}
	d, _, err := s.App.Decide(ctx, key, disposition, o)
	if err != nil {
		return "", Committed{}, err
	}
	k, _ := item.ParseKey(key)
	_ = s.Props.SupersedeKey(ctx, k.String(), keep)
	return k.String(), committedOf(app.RevisionOf(d)), nil
}

func committedOf(r app.Revision) Committed {
	return Committed{Disposition: string(r.Disposition), Until: r.Until, Note: r.Note, DecidedAt: r.DecidedAt}
}

func (c Committed) revision() app.Revision {
	return app.Revision{Disposition: item.Disposition(c.Disposition), Until: c.Until, Note: c.Note, DecidedAt: c.DecidedAt}
}

// finishDecides, once n > 0 decisions are committed, asks for the push
// (push.go: it runs in the background, so no decide waits on the network)
// and rebuilds the index once. A rebuild error is appended to errs and not
// returned as a failure: decisions are already durable and the watch loop
// rebuilds on the moved HEAD. The push is asked for before the rebuild
// announces "index", so the summary the page then asks for knows a push is
// in flight.
func (s *Server) finishDecides(ctx context.Context, n int, errs []string) []string {
	if n == 0 {
		return errs
	}
	s.schedulePush()
	if err := s.rebuild(ctx); err != nil {
		errs = append(errs, err.Error())
	}
	return errs
}

// decideAll records decisions (one commit each), retires the pending
// proposals for those keys (except keep, the one being accepted), asks for
// the push and rebuilds the index (finishDecides).
func (s *Server) decideAll(ctx context.Context, keys []string, disposition string, o app.DecideOptions, keep int64) (int, []string, []string, map[string]Committed) {
	if o.By == "" {
		o.By = s.App.Cfg.User
	}
	n := 0
	var errs []string
	var done []string
	made := map[string]Committed{}
	for _, key := range keys {
		k, c, err := s.decideOneKey(ctx, key, disposition, o, keep)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		done = append(done, k)
		made[k] = c
		n++
	}
	if n > 0 {
		s.publish(ctx, "decided", map[string]any{"keys": done, "disposition": disposition, "by": o.By, "proposed_by": o.ProposedBy})
	}
	errs = s.finishDecides(ctx, n, errs)
	return n, done, errs, made
}

// decisionVocab is the decision vocabulary: GET /api/decisions/vocabulary
// returns it, and casebook_next renders its guide and choices from it.
func decisionVocab() DecisionVocabView {
	kinds := []item.Kind{item.KindRepo, item.KindPR, item.KindIssue, item.KindBranch, item.KindWorktree}
	vocab := DecisionVocabView{
		Kinds:      make([]KindVocab, len(kinds)),
		UntilForms: nil,
	}
	for i, k := range kinds {
		allowed := item.Allowed(k)
		strs := make([]string, len(allowed))
		var needsUntil []string
		for j, d := range allowed {
			strs[j] = string(d)
			if d == item.Wait || d == item.Watch {
				needsUntil = append(needsUntil, string(d))
			}
		}
		if needsUntil == nil {
			needsUntil = []string{}
		}
		var choices []ChoiceVocab
		for _, c := range item.Choices(k) {
			choices = append(choices, ChoiceVocab{Disposition: string(c.Disposition), Label: c.Label, Says: c.Says, Outward: c.Outward, NeedsUntil: c.NeedsUntil})
		}
		vocab.Kinds[i] = KindVocab{Kind: string(k), Allowed: strs, NeedsUntil: needsUntil, Question: item.Question(k), Choices: nonNil(choices)}
	}
	for _, f := range item.UntilForms() {
		vocab.UntilForms = append(vocab.UntilForms, UntilForm{Op: f.Op, Syntax: f.Syntax, Example: f.Example})
	}
	for _, f := range item.NotNowForms() {
		vocab.NotNow = append(vocab.NotNow, NotNowForm{ID: f.ID, Label: f.Label, Template: f.Template, Asks: f.Asks, Days: f.Days})
	}
	return vocab
}

func (s *Server) getDecisionsVocabulary(w http.ResponseWriter, r *http.Request) {
	reply(w, decisionVocab(), nil)
}

// postClearDecision handles POST /api/decisions/clear: an older page's undo
// (POST /api/decisions/undo replaces it). It removes the item's decision only
// when it is still the one the page made: its decided_at, and its
// disposition, until and note when the page sends them (409 when the agent,
// a rule or another page decided since), then asks for the background push
// and rebuilds the index, as a decide does.
func (s *Server) postClearDecision(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key         string    `json:"key"`
		DecidedAt   time.Time `json:"decided_at"`
		Disposition string    `json:"disposition"`
		Until       string    `json:"until"`
		Note        string    `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	k, err := item.ParseKey(in.Key)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if in.DecidedAt.IsZero() {
		reply(w, nil, bad("decided_at required"))
		return
	}
	ctx := r.Context()
	err = s.App.Clear(ctx, k.String(), app.Revision{Disposition: item.Disposition(in.Disposition), Until: in.Until, Note: in.Note, DecidedAt: in.DecidedAt})
	switch {
	case errors.Is(err, app.ErrNotDecided):
		reply(w, ClearResult{Cleared: false, PushedLater: true}, nil)
		return
	case errors.Is(err, app.ErrStaleDecision):
		reply(w, nil, httpError{code: http.StatusConflict, msg: err.Error()})
		return
	case err != nil:
		reply(w, nil, err)
		return
	}
	s.publish(ctx, "cleared", map[string]any{"keys": []string{k.String()}, "by": s.App.Cfg.User})
	// A rebuild error isn't a failure: the clear is committed and the watch
	// loop rebuilds on the moved HEAD (finishDecides).
	_ = s.finishDecides(ctx, 1, nil)
	reply(w, ClearResult{Cleared: true, PushedLater: true}, nil)
}

// postUndoDecision handles POST /api/decisions/undo: the page's undo of a
// decision it made. Under the store lock, when the item's decision is still
// expect (the decision the page's decide or accept committed, compared in
// every field), it decides restore (the decision the item had before) or,
// when restore is null, removes the decision. Otherwise 409 and nothing
// changes. Then the background push and the rebuild, as a decide does.
func (s *Server) postUndoDecision(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key     string     `json:"key"`
		Expect  *Committed `json:"expect"`
		Restore *Committed `json:"restore"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	k, err := item.ParseKey(in.Key)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if in.Expect == nil || in.Expect.Disposition == "" || in.Expect.DecidedAt.IsZero() {
		reply(w, nil, bad("expect needs the decision's disposition and decided_at"))
		return
	}
	var restore *app.Revision
	if in.Restore != nil {
		rv := in.Restore.revision()
		restore = &rv
	}
	ctx := r.Context()
	d, err := s.App.Undo(ctx, k.String(), in.Expect.revision(), restore, app.DecideOptions{By: s.App.Cfg.User})
	switch {
	case errors.Is(err, app.ErrStaleDecision):
		reply(w, nil, httpError{code: http.StatusConflict, msg: err.Error()})
		return
	case err != nil:
		reply(w, nil, bad("%v", err))
		return
	}
	out := DecisionUndoResult{Undone: true}
	if d == nil {
		s.publish(ctx, "cleared", map[string]any{"keys": []string{k.String()}, "by": s.App.Cfg.User})
	} else {
		c := committedOf(app.RevisionOf(*d))
		out.Decision = &c
		s.publish(ctx, "decided", map[string]any{"keys": []string{k.String()}, "disposition": c.Disposition, "by": s.App.Cfg.User})
	}
	_ = s.finishDecides(ctx, 1, nil)
	reply(w, out, nil)
}

func (s *Server) postDecide(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys        []string `json:"keys"`
		Disposition string   `json:"disposition"`
		Until       string   `json:"until"`
		Note        string   `json:"note"`
		DryRun      bool     `json:"dry_run"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if len(in.Keys) == 0 {
		reply(w, nil, bad("no keys"))
		return
	}
	if in.DryRun {
		// Validate without writing any decision.
		d := item.Decision{
			Disposition: item.Disposition(in.Disposition),
			Until:       in.Until,
			DecidedBy:   s.App.Cfg.User,
			DecidedAt:   s.Now(),
		}
		var errs []string
		for _, key := range in.Keys {
			k, err := item.ParseKey(key)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if err := d.Validate(k.Kind); err != nil {
				errs = append(errs, err.Error())
			}
		}
		reply(w, DecideResult{Decided: 0, DecidedKeys: []string{}, Errors: nonNil(errs), Decisions: map[string]Committed{}}, nil)
		return
	}
	n, done, errs, made := s.decideAll(r.Context(), in.Keys, in.Disposition, app.DecideOptions{Until: in.Until, Note: in.Note}, 0)
	reply(w, DecideResult{Decided: n, DecidedKeys: nonNil(done), Errors: nonNil(errs), Decisions: made}, nil)
}

// proposalOpts is how a proposal is decided when it is accepted. Its note is
// the recommendation's reason, written to Court; a close posts its decision's
// note as the public closing comment, so a close takes no note from its
// proposal (the reason stays on the proposal). Other dispositions post
// nothing and keep it.
func proposalOpts(p propose.Proposal) app.DecideOptions {
	o := app.DecideOptions{Until: p.Until, Note: p.Note, ProposedBy: p.Source}
	if item.Disposition(p.Disposition) == item.Close {
		o.Note = ""
	}
	if id, ok := strings.CutPrefix(p.Source, "rule:"); ok {
		o.Rule = id
	}
	return o
}

// postAccept is POST /api/proposals/accept. note, when given, is Court's
// closing comment for an accepted close (the page's closing-comment field);
// without it an accepted close records no note, so nothing is posted. It
// does nothing for other dispositions.
func (s *Server) postAccept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs  []int64 `json:"ids"`
		Note string  `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	total := 0
	var errs []string
	by := s.App.Cfg.User
	made := map[string]Committed{}
	for _, id := range in.IDs {
		p, err := s.Props.Get(ctx, id)
		if err != nil || p.State != propose.Pending {
			errs = append(errs, fmt.Sprintf("proposal %d is not pending", id))
			continue
		}
		o := proposalOpts(p)
		if item.Disposition(p.Disposition) == item.Close {
			o.Note = in.Note
		}
		normed, c, err := s.decideOneKey(ctx, p.Key, p.Disposition, o, p.ID)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		made[normed] = c
		_ = s.Props.Settle(ctx, id, propose.Accepted, "")
		s.publish(ctx, "decided", map[string]any{
			"keys":        []string{normed},
			"disposition": p.Disposition,
			"by":          by,
			"proposed_by": p.Source,
		})
		total++
	}
	errs = s.finishDecides(ctx, total, errs)
	s.publish(ctx, "proposals", map[string]any{"ids": in.IDs, "state": propose.Accepted})
	reply(w, AcceptResult{Accepted: total, Errors: nonNil(errs), Decisions: made}, nil)
}

func (s *Server) postChange(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID          int64  `json:"id"`
		Disposition string `json:"disposition"`
		Until       string `json:"until"`
		Note        string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	p, err := s.Props.Get(ctx, in.ID)
	if err != nil || p.State != propose.Pending {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: fmt.Sprintf("proposal %d is not pending", in.ID)})
		return
	}
	o := proposalOpts(p)
	o.Until, o.Note = in.Until, in.Note
	n, done, errs, made := s.decideAll(ctx, []string{p.Key}, in.Disposition, o, p.ID)
	if n == 1 {
		_ = s.Props.Settle(ctx, p.ID, propose.Changed, changedTo(in.Disposition, in.Until, in.Note))
		s.publish(ctx, "proposals", map[string]any{"ids": []int64{p.ID}, "state": propose.Changed})
	}
	reply(w, DecideResult{Decided: n, DecidedKeys: nonNil(done), Errors: nonNil(errs), Decisions: made}, nil)
}

func (s *Server) postReject(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []int64 `json:"ids"`
		Reason string  `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	n := 0
	for _, id := range in.IDs {
		if s.Props.Settle(ctx, id, propose.Rejected, in.Reason) == nil {
			n++
		}
	}
	s.publish(ctx, "proposals", map[string]any{"ids": in.IDs, "state": propose.Rejected})
	reply(w, RejectResult{Rejected: n}, nil)
}

// getSessions answers the sessions the page uses: the eligible ones (live,
// not subagent workers: what the chooser, "move to…" and To apply offer),
// plus each session named by ?session= (comma-separated, or repeated:
// the page's attached session, shown even after it left, and To apply's
// jobs' sessions), whatever their state. ?all=1 answers every known session
// (the page never asks for that; Court's table held 288, 39 live).
func (s *Server) getSessions(w http.ResponseWriter, r *http.Request) {
	ss, err := s.Queue.Sessions(r.Context())
	if err != nil {
		reply(w, nil, err)
		return
	}
	q := r.URL.Query()
	if q.Get("all") == "1" {
		reply(w, SessionsView{Sessions: nonNil(ss)}, nil)
		return
	}
	named := map[string]bool{}
	for _, v := range q["session"] {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				named[id] = true
			}
		}
	}
	out := []deliver.Session{}
	for _, sess := range ss {
		if sess.Eligible || named[sess.ID] {
			out = append(out, sess)
		}
	}
	reply(w, SessionsView{Sessions: out}, nil)
}

// postMoveSession atomically moves all threads (and their queued messages) from
// a left session to a target session. Used by the dock's "move to..." action on
// a left session (spec §2.1). No delivery is needed; the messages remain queued
// and become deliverable to the target session on its next turn.
func (s *Server) postMoveSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string `json:"session"`
		Target  string `json:"target"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if in.Session == "" || in.Target == "" {
		reply(w, nil, bad("session and target required"))
		return
	}
	ctx := r.Context()
	n, movedID, err := s.Queue.MoveSession(ctx, in.Session, in.Target)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if n > 0 {
		// Publish a sessions event so the page dock refreshes.
		s.publish(ctx, "sessions", map[string]string{"moved_from": in.Session, "moved_to": in.Target})
		// Wake the target session's long-poll so it picks up the new threads.
		s.wake(in.Target)
	}
	if movedID > 0 {
		// An in-flight delivery was rescued: publish a delivery event so the
		// source session's dock removes its stuck buttons (same as MoveDelivery).
		s.publish(ctx, "delivery", map[string]any{"id": movedID, "state": deliver.Moved, "session": in.Target, "from": in.Session})
		s.wake(in.Session)
	}
	reply(w, map[string]int{"moved": n}, nil)
}

// getSessionProgress returns the current progress line for a session,
// or a SessionProgressView with null progress when there is none.
func (s *Server) getSessionProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sid := r.URL.Query().Get("session")
	if sid == "" {
		reply(w, nil, bad("session required"))
		return
	}
	p, ok, err := s.Props.Progress(ctx, sid)
	if err != nil {
		reply(w, nil, err)
		return
	}
	now := s.Now()
	if !ok {
		reply(w, SessionProgressView{Progress: nil, Now: now}, nil)
		return
	}
	reply(w, SessionProgressView{Progress: &p, Now: now}, nil)
}

// getSessionDelivery returns the current in-flight delivery for a session,
// or a DeliveryView with a null delivery when there is none.
func (s *Server) getSessionDelivery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sid := r.URL.Query().Get("session")
	if sid == "" {
		reply(w, nil, bad("session required"))
		return
	}
	d, err := s.Queue.Inflight(ctx, sid)
	if err != nil {
		reply(w, nil, err)
		return
	}
	reply(w, DeliveryView{Delivery: d}, nil)
}

func (s *Server) getThreads(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Queue.Threads(r.Context(), r.URL.Query().Get("session"))
	reply(w, ThreadsView{Threads: nonNil(ts)}, err)
}

func (s *Server) postThread(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string `json:"session"`
		Name    string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	if _, err := s.Queue.Session(r.Context(), in.Session); err != nil {
		reply(w, nil, bad("unknown session %q", in.Session))
		return
	}
	if in.Name == "" {
		in.Name = "thread"
	}
	t, err := s.Queue.NewThread(r.Context(), in.Session, in.Name)
	if err == nil {
		s.publish(r.Context(), "thread", t)
	}
	reply(w, t, err)
}

func (s *Server) postMoveThread(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Thread  int64  `json:"thread"`
		Session string `json:"session"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	err := s.Queue.MoveThread(r.Context(), in.Thread, in.Session)
	if err == nil {
		s.publish(r.Context(), "thread", map[string]any{"id": in.Thread, "session_id": in.Session})
		s.wake(in.Session)
	}
	reply(w, nil, err)
}

func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := int64(atoi(r.URL.Query().Get("thread")))
	ms, err := s.Queue.Messages(ctx, id)
	if err != nil {
		reply(w, nil, err)
		return
	}
	bid, drafts, err := s.Queue.DraftBatch(ctx, id)
	var out []MessageView
	for _, m := range ms {
		if m.State != deliver.Draft {
			out = append(out, toMessageView(m))
		}
	}
	reply(w, MessagesView{Messages: nonNil(out), Batch: bid, Drafts: nonNil(drafts)}, err)
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Thread   int64            `json:"thread"`
		Body     string           `json:"body"`
		Attached deliver.Attached `json:"attached"`
		Batch    bool             `json:"batch"`
		// Session is the session the page is attached to (its header). A
		// thread that belongs to another one now (moved behind the page's
		// back) is refused: see ownedBy.
		Session string `json:"session"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	t, err := s.Queue.Thread(ctx, in.Thread)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if err := ownedBy(t, in.Session); err != nil {
		reply(w, nil, err)
		return
	}
	m, err := s.Queue.Post(ctx, in.Thread, in.Body, in.Attached, in.Batch)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	s.publish(ctx, "message", m)
	if !in.Batch {
		s.wake(t.SessionID)
	}
	reply(w, m, nil)
}

// ownedBy refuses a page's send into a thread that belongs to a session
// other than the one the page is attached to (session, from its header):
// a move (a stuck delivery moved, or "move to…") re-homes a thread, and a
// page that hasn't caught up must not send to a session it doesn't show.
// 409 thread_moved; the page reloads its threads. An empty session (a
// caller that isn't the page: tests, scripts) is not checked.
func ownedBy(t deliver.Thread, session string) error {
	if session == "" || t.SessionID == session {
		return nil
	}
	return httpError{code: http.StatusConflict, errCode: "thread_moved",
		msg: fmt.Sprintf("thread %d belongs to session %q now, not %q; nothing was sent", t.ID, t.SessionID, session)}
}

func (s *Server) sessionsOf(ctx context.Context, ids []int64) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		m, err := s.Queue.Message(ctx, id)
		if err != nil {
			continue
		}
		t, err := s.Queue.Thread(ctx, m.ThreadID)
		if err == nil && !seen[t.SessionID] {
			seen[t.SessionID] = true
			out = append(out, t.SessionID)
		}
	}
	return out
}

func (s *Server) postResend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	n, err := s.Queue.Resend(r.Context(), in.IDs)
	for _, sess := range s.sessionsOf(r.Context(), in.IDs) {
		s.wake(sess)
	}
	s.publish(r.Context(), "messages", map[string]any{"ids": in.IDs, "state": deliver.Queued})
	reply(w, ResendResult{Resent: n}, err)
}

func (s *Server) postEditDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	err := s.Queue.EditDraft(r.Context(), in.ID, in.Body)
	if err == nil {
		s.publish(r.Context(), "drafts", map[string]int64{"id": in.ID})
	}
	reply(w, nil, err)
}

func (s *Server) postRemoveDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	err := s.Queue.RemoveDraft(r.Context(), in.ID)
	if err == nil {
		s.publish(r.Context(), "drafts", map[string]int64{"id": in.ID})
	}
	reply(w, nil, err)
}

func (s *Server) postReorder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Batch int64   `json:"batch"`
		IDs   []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	err := s.Queue.ReorderBatch(r.Context(), in.Batch, in.IDs)
	if err != nil {
		err = bad("%v", err)
	} else {
		s.publish(r.Context(), "drafts", map[string]int64{"batch": in.Batch})
	}
	reply(w, nil, err)
}

func (s *Server) postSendBatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Batch int64 `json:"batch"`
		// Session: the page's session, as on POST /api/messages.
		Session string `json:"session"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	var thread int64
	if err := s.DB.QueryRowContext(ctx, "SELECT thread_id FROM batches WHERE id = ?", in.Batch).Scan(&thread); err != nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "no such batch"})
		return
	}
	if t, err := s.Queue.Thread(ctx, thread); err != nil {
		reply(w, nil, err)
		return
	} else if err := ownedBy(t, in.Session); err != nil {
		reply(w, nil, err)
		return
	}
	n, err := s.Queue.SendBatch(ctx, in.Batch)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if t, err := s.Queue.Thread(ctx, thread); err == nil {
		s.wake(t.SessionID)
	}
	s.publish(ctx, "batch", map[string]any{"batch": in.Batch, "sent": n})
	reply(w, SendBatchResult{Sent: n}, nil)
}

func (s *Server) postRelease(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	d, err := s.Queue.Delivery(ctx, in.ID)
	if err == nil {
		err = s.Queue.Release(ctx, in.ID)
	}
	if err == nil {
		s.publish(ctx, "delivery", map[string]any{"id": in.ID, "state": deliver.Released})
		s.wake(d.SessionID)
	}
	reply(w, nil, err)
}

func (s *Server) postMoveDelivery(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID      int64  `json:"id"`
		Session string `json:"session"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	// Look up the current session before the move so we can wake it: moving a
	// delivery ends the old session's in-flight slot, making any messages queued
	// behind it sendable, but only the new session would be woken otherwise.
	d, err := s.Queue.Delivery(ctx, in.ID)
	if err == nil {
		err = s.Queue.MoveDelivery(ctx, in.ID, in.Session)
	}
	if err == nil {
		// Include the source session ("from") so the dock knows to refresh even
		// when watching the source, not the target.
		s.publish(ctx, "delivery", map[string]any{"id": in.ID, "state": deliver.Moved, "session": in.Session, "from": d.SessionID})
		s.wake(d.SessionID) // parity with postRelease: wake the old session
		s.wake(in.Session)
	}
	reply(w, nil, err)
}

// changedTo is what Court decided instead of a proposal, for the agent's
// since-summary: "decided keep", "decided wait until merged(#12): after the demo".
func changedTo(disposition, until, note string) string {
	out := "decided " + disposition
	if until != "" {
		out += " until " + until
	}
	if note != "" {
		out += ": " + note
	}
	return out
}
