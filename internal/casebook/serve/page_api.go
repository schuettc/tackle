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
	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/store"
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func (s *Server) getSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, err := s.Props.Pending(ctx)
	if err != nil {
		reply(w, nil, err)
		return
	}
	queued := 0
	if out, err := gitx.Run(ctx, s.App.Repo.Dir, "rev-list", "--count", "origin/main..HEAD"); err == nil {
		queued = atoi(out)
	}
	var synced time.Time
	if fi, err := os.Stat(config.CachePath()); err == nil {
		synced = fi.ModTime().UTC()
	}
	sessions, _ := s.Queue.Sessions(ctx)
	reply(w, SummaryView{
		Machine:       s.App.Cfg.Machine,
		User:          s.App.Cfg.User,
		Head:          s.Index.Head(),
		BuiltAt:       s.Index.BuiltAt(),
		SyncedAt:      synced,
		OfflineQueued: queued,
		Counts:        s.Index.Counts(pending),
		Notices:       s.Index.Notices(),
		Sessions:      len(sessions),
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
	reply(w, ItemsView{Total: total, Items: items}, nil)
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
	v := ItemView{Item: it}
	pending, _ := s.Props.Pending(ctx)
	if p, ok := pending[it.ID]; ok {
		v.Proposal = &p
	}
	proposals, _ := s.Props.ForKey(ctx, it.ID)
	evidence, _ := s.Props.Evidence(ctx, it.ID)
	events, _ := s.App.Events()
	hist := engine.History(events, k)
	if len(hist) > 50 {
		hist = hist[len(hist)-50:]
	}
	log, _ := s.App.Repo.Log(ctx, k.File())
	reply(w, ItemDetailView{
		Item:      v,
		Proposals: nonNil(proposals),
		Evidence:  nonNil(evidence),
		History:   nonNil(hist),
		Decisions: nonNil(log),
	}, nil)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// decideOneKey records one decision with NoPush, supersedes other pending
// proposals for the key except keep (the proposal being accepted), and returns
// the normalized key. By defaults to s.App.Cfg.User.
func (s *Server) decideOneKey(ctx context.Context, key, disposition string, o app.DecideOptions, keep int64) (string, error) {
	o.NoPush = true
	if o.By == "" {
		o.By = s.App.Cfg.User
	}
	if _, _, err := s.App.Decide(ctx, key, disposition, o); err != nil {
		return "", err
	}
	k, _ := item.ParseKey(key)
	_ = s.Props.SupersedeKey(ctx, k.String(), keep)
	return k.String(), nil
}

// finishDecides pushes once (ErrOffline is not an error) and rebuilds the
// index once when n > 0. A rebuild error is appended to errs and not returned
// as a failure: decisions are already durable and the watch loop rebuilds on
// the moved HEAD.
func (s *Server) finishDecides(ctx context.Context, n int, errs []string) (bool, []string) {
	if n == 0 {
		return false, errs
	}
	pushed, err := s.App.Push(ctx)
	if err != nil && !errors.Is(err, store.ErrOffline) {
		errs = append(errs, err.Error())
	}
	if err := s.rebuild(ctx); err != nil {
		errs = append(errs, err.Error())
	}
	return pushed, errs
}

// decideAll records decisions (one commit each), pushes once, retires the
// pending proposals for those keys (except keep, the one being accepted),
// rebuilds the index and announces it.
func (s *Server) decideAll(ctx context.Context, keys []string, disposition string, o app.DecideOptions, keep int64) (int, []string, []string, bool, error) {
	if o.By == "" {
		o.By = s.App.Cfg.User
	}
	n := 0
	var errs []string
	var done []string
	for _, key := range keys {
		k, err := s.decideOneKey(ctx, key, disposition, o, keep)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		done = append(done, k)
		n++
	}
	if n > 0 {
		s.publish(ctx, "decided", map[string]any{"keys": done, "disposition": disposition, "by": o.By, "proposed_by": o.ProposedBy})
	}
	pushed, errs := s.finishDecides(ctx, n, errs)
	return n, done, errs, pushed, nil
}

func (s *Server) getDecisionsVocabulary(w http.ResponseWriter, r *http.Request) {
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
		vocab.Kinds[i] = KindVocab{Kind: string(k), Allowed: strs, NeedsUntil: needsUntil}
	}
	for _, f := range item.UntilForms() {
		vocab.UntilForms = append(vocab.UntilForms, UntilForm{Op: f.Op, Syntax: f.Syntax, Example: f.Example})
	}
	reply(w, vocab, nil)
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
		reply(w, DecideResult{Decided: 0, DecidedKeys: []string{}, Errors: nonNil(errs), Pushed: false}, nil)
		return
	}
	n, done, errs, pushed, err := s.decideAll(r.Context(), in.Keys, in.Disposition, app.DecideOptions{Until: in.Until, Note: in.Note}, 0)
	reply(w, DecideResult{Decided: n, DecidedKeys: nonNil(done), Errors: nonNil(errs), Pushed: pushed}, err)
}

func proposalOpts(p propose.Proposal) app.DecideOptions {
	o := app.DecideOptions{Until: p.Until, Note: p.Note, ProposedBy: p.Source}
	if id, ok := strings.CutPrefix(p.Source, "rule:"); ok {
		o.Rule = id
	}
	return o
}

func (s *Server) postAccept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	total := 0
	var errs []string
	by := s.App.Cfg.User
	for _, id := range in.IDs {
		p, err := s.Props.Get(ctx, id)
		if err != nil || p.State != propose.Pending {
			errs = append(errs, fmt.Sprintf("proposal %d is not pending", id))
			continue
		}
		normed, err := s.decideOneKey(ctx, p.Key, p.Disposition, proposalOpts(p), p.ID)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		_ = s.Props.Settle(ctx, id, propose.Accepted, "")
		s.publish(ctx, "decided", map[string]any{
			"keys":        []string{normed},
			"disposition": p.Disposition,
			"by":          by,
			"proposed_by": p.Source,
		})
		total++
	}
	pushed, errs := s.finishDecides(ctx, total, errs)
	s.publish(ctx, "proposals", map[string]any{"ids": in.IDs, "state": propose.Accepted})
	reply(w, AcceptResult{Accepted: total, Errors: nonNil(errs), Pushed: pushed}, nil)
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
	n, done, errs, pushed, err := s.decideAll(ctx, []string{p.Key}, in.Disposition, o, p.ID)
	if err == nil && n == 1 {
		_ = s.Props.Settle(ctx, p.ID, propose.Changed, changedTo(in.Disposition, in.Until, in.Note))
		s.publish(ctx, "proposals", map[string]any{"ids": []int64{p.ID}, "state": propose.Changed})
	}
	reply(w, DecideResult{Decided: n, DecidedKeys: nonNil(done), Errors: nonNil(errs), Pushed: pushed}, err)
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

func (s *Server) getSessions(w http.ResponseWriter, r *http.Request) {
	ss, err := s.Queue.Sessions(r.Context())
	reply(w, SessionsView{Sessions: nonNil(ss)}, err)
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
		s.publish(ctx, "delivery", map[string]any{"id": in.ID, "state": deliver.Moved, "session": in.Session})
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
