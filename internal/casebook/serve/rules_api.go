package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/rules"
	"github.com/schuettc/tackle/internal/casebook/store"
)

// getRules handles GET /api/rules.
// Returns all rules with their track records.
func (s *Server) getRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, errs := s.App.Repo.Rules()
	rows := make([]RuleRow, 0, len(all))
	for _, ru := range all {
		rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
		rows = append(rows, RuleRow{
			Rule: ru, Record: rec, Invalid: invalidReason(ru),
			Matches: s.ruleMatches(ru), Excluded: len(ru.Exclude),
		})
	}
	// A file that can't be read as a rule is listed too, as invalid: Court
	// sees it and can replace it from the page.
	for _, err := range errs {
		var fe *store.RuleFileError
		if errors.As(err, &fe) {
			rows = append(rows, RuleRow{Rule: unreadableRule(fe.ID), Invalid: fe.Error()})
		}
	}
	reply(w, RulesView{Rules: rows}, nil)
}

// getRule handles GET /api/rule?id=.
func (s *Server) getRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.URL.Query().Get("id")
	if !rules.ValidID(id) {
		reply(w, nil, bad("invalid rule id %q", id))
		return
	}
	ru, err := s.App.Repo.ReadRule(id)
	var fe *store.RuleFileError
	if errors.As(err, &fe) {
		reply(w, RuleDetailView{Rule: unreadableRule(id), Matches: emptyPreview(nil), Invalid: fe.Error()}, nil)
		return
	}
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if ru == nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "rule " + id + " not found"})
		return
	}
	reply(w, s.ruleDetail(ctx, *ru), nil)
}

// ruleCount is one rule's match count, for one content of the rule
// (its version and exclusions) against one build of the index.
type ruleCount struct {
	sig string
	gen uint64
	n   int
}

// ruleMatches counts ru's matches now. The count is kept until the rule or
// the index changes, so listing the rules doesn't re-run every rule.
func (s *Server) ruleMatches(ru rules.Rule) int {
	if rules.ValidateConditions(ru.Match) != nil {
		return 0
	}
	sig := rules.Version(ru)
	for _, x := range ru.Exclude {
		sig += "\x00" + x.Key
	}
	gen := s.Index.Gen()
	s.ruleCountMu.Lock()
	c, ok := s.ruleCounts[ru.ID]
	s.ruleCountMu.Unlock()
	if ok && c.sig == sig && c.gen == gen {
		return c.n
	}
	s.ruleCountRuns.Add(1)
	ms, err := ru.MatchAll(s.Index.Result(), s.Now())
	n := len(ms)
	if err != nil {
		n = 0
	}
	s.ruleCountMu.Lock()
	if s.ruleCounts == nil {
		s.ruleCounts = map[string]ruleCount{}
	}
	s.ruleCounts[ru.ID] = ruleCount{sig: sig, gen: gen, n: n}
	s.ruleCountMu.Unlock()
	return n
}

// unreadableRule stands in for a rules/<id>.toml that can't be read: a draft
// with nothing in it, which a save from the page replaces.
func unreadableRule(id string) rules.Rule {
	return rules.Rule{ID: id, Name: id, Status: rules.StatusDraft, Match: []rules.Condition{}}
}

// invalidReason is serve's validation message for ru, "" when it is valid.
func invalidReason(ru rules.Rule) string {
	if err := ru.Validate(); err != nil {
		return err.Error()
	}
	return ""
}

// emptyPreview is the preview of a rule that isn't previewed: no matches,
// and what it may propose.
func emptyPreview(match []rules.Condition) MatchPreview {
	return MatchPreview{ByReason: []ReasonCount{}, Groups: []RepoGroup{}, Page: []MatchRow{},
		Dispositions: rules.Dispositions(match)}
}

// postRulesDraft handles POST /api/rules/draft.
// Creates or edits a draft rule. Body is a rules.Rule; status must be "draft".
// The user is always "court" (the config user). Commit: "rule <id> created by court"
// or "rule <id> edited by court".
func (s *Server) postRulesDraft(w http.ResponseWriter, r *http.Request) {
	var in rules.Rule
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	// Fix 6: validate the rule id up front before any disk access.
	if !rules.ValidID(in.ID) {
		reply(w, nil, bad("invalid rule id %q", in.ID))
		return
	}
	ctx := r.Context()
	by := s.App.Cfg.User
	if by == "" {
		by = "court"
	}

	// Conditions serve refuses are refused before touching disk; the
	// proposal may be unfinished (a new rule), and the rule then shows as
	// not valid until it is.
	if err := rules.ValidateConditions(in.Match); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	now := s.Now()
	existing, err := s.App.Repo.ReadRule(in.ID)
	var fe *store.RuleFileError
	replacing := errors.As(err, &fe) // a file that isn't a rule: the save replaces it
	if replacing && r.URL.Query().Get("create") == "1" {
		reply(w, nil, conflict("rule %q already exists (its file can't be read)", in.ID))
		return
	}
	if err != nil && !replacing {
		reply(w, nil, bad("%v", err))
		return
	}
	// ?create=1 makes a new draft and never overwrites one; ?version= saves
	// over exactly the copy the page read (a page never overwrites edits it
	// didn't show).
	q := r.URL.Query()
	if q.Get("create") == "1" && existing != nil {
		reply(w, nil, conflict("rule %q already exists", in.ID))
		return
	}
	if v := q.Get("version"); v != "" && existing != nil && v != rules.Version(*existing) {
		reply(w, nil, conflict("rule %s changed since you read it; nothing was saved", in.ID))
		return
	}

	var msg string
	if existing == nil {
		// Create.
		in.Status = rules.StatusDraft
		in.CreatedBy = by
		in.CreatedAt = now
		in.EditedAt = now
		msg = "rule " + in.ID + " created by " + by
		if replacing {
			msg = "rule " + in.ID + " rewritten by " + by + " (its file could not be read)"
		}
	} else {
		// Edit existing draft. An active rule is edited only when it is
		// invalid (rebuild skips it): saving the fix makes it a draft, for
		// Court to activate again.
		if existing.Status == rules.StatusActive && existing.Validate() == nil {
			reply(w, nil, bad("cannot edit an active rule via draft endpoint; deactivate it first"))
			return
		}
		in.Status = rules.StatusDraft
		in.CreatedBy = existing.CreatedBy
		in.CreatedAt = existing.CreatedAt
		in.EditedAt = editedAt(*existing, in, now)
		msg = "rule " + in.ID + " edited by " + by
	}

	if unchanged(existing, in) {
		reply(w, s.ruleDetail(ctx, in), nil)
		return
	}
	if err := s.App.Repo.WriteRule(ctx, in, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": in.ID, "action": "drafted", "by": by})

	reply(w, s.ruleDetail(ctx, in), nil)
}

// postRulesPreview handles POST /api/rules/preview.
// Runs a match over the live index without persisting anything.
// Invalid regex / field / value → 400.
func (s *Server) postRulesPreview(w http.ResponseWriter, r *http.Request) {
	// We accept a partial Rule (just Match and Propose, and optionally Exclude).
	var in rules.Rule
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}

	// Validate each condition. This produces a clear 400 for invalid regex,
	// unknown field, and invalid value before we ever run MatchAll.
	if err := rules.ValidateConditions(in.Match); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	q := r.URL.Query()
	offset := atoi(q.Get("offset"))
	limit := atoi(q.Get("limit"))

	preview := buildPreview(in, s.Index.Result(), s.Now(), offset, limit)
	reply(w, preview, nil)
}

// postRulesExclude handles POST /api/rules/exclude.
// Adds an exclusion to a rule (untick a match). Exclusions do not bump
// edited_at because they do not change what the rule means (spec §4.1, brief
// note: "exclusions don't change what the rule means, so don't bump edited_at
// for them").
func (s *Server) postRulesExclude(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Reason string `json:"reason"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	if in.ID == "" {
		reply(w, nil, bad("ID required"))
		return
	}
	if in.Key == "" {
		reply(w, nil, bad("Key required"))
		return
	}

	ru, err := s.App.Repo.ReadRule(in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if ru == nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "rule " + in.ID + " not found"})
		return
	}

	by := s.App.Cfg.User
	if by == "" {
		by = "court"
	}

	// Add exclusion if not already present.
	for _, ex := range ru.Exclude {
		if ex.Key == in.Key {
			// Already excluded; no-op.
			reply(w, s.ruleDetail(ctx, *ru), nil)
			return
		}
	}
	ru.Exclude = append(ru.Exclude, rules.Exclusion{
		Key:    in.Key,
		Reason: in.Reason,
		By:     by,
		At:     s.Now(),
	})

	// Exclusions do not bump edited_at.
	msg := "rule " + ru.ID + " exclude " + in.Key + " by " + by
	if err := s.App.Repo.WriteRule(ctx, *ru, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": ru.ID, "action": "excluded", "key": in.Key})

	reply(w, s.ruleDetail(ctx, *ru), nil)
}

// postRulesInclude handles POST /api/rules/include.
// Removes an exclusion from a rule (re-tick a match). Does not bump edited_at.
func (s *Server) postRulesInclude(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	if in.ID == "" {
		reply(w, nil, bad("ID required"))
		return
	}
	if in.Key == "" {
		reply(w, nil, bad("Key required"))
		return
	}

	ru, err := s.App.Repo.ReadRule(in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if ru == nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "rule " + in.ID + " not found"})
		return
	}

	by := s.App.Cfg.User
	if by == "" {
		by = "court"
	}

	var kept []rules.Exclusion
	removed := false
	for _, ex := range ru.Exclude {
		if ex.Key == in.Key {
			removed = true
			continue
		}
		kept = append(kept, ex)
	}
	if !removed {
		// No exclusion to remove; no-op.
		reply(w, s.ruleDetail(ctx, *ru), nil)
		return
	}
	ru.Exclude = kept

	// Inclusions do not bump edited_at.
	msg := "rule " + ru.ID + " include " + in.Key + " by " + by
	if err := s.App.Repo.WriteRule(ctx, *ru, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": ru.ID, "action": "included", "key": in.Key})

	reply(w, s.ruleDetail(ctx, *ru), nil)
}

// postRulesProposeOnce handles POST /api/rules/propose-once.
// Runs ProposeOnce for the rule (draft or active) and returns ProposeResult.
func (s *Server) postRulesProposeOnce(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	ru, err := s.App.Repo.ReadRule(in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if ru == nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "rule " + in.ID + " not found"})
		return
	}

	if err := ru.Validate(); err != nil {
		reply(w, nil, bad("rule %s is not valid: %v", ru.ID, err))
		return
	}
	proposals, n, err := rules.ProposeOnce(ctx, *ru, s.Index.Result(), s.Now(), s.Props)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if n > 0 {
		s.publish(ctx, "rules", map[string]any{"id": ru.ID, "proposed": n})
	}
	if proposals == nil {
		proposals = []propose.Proposal{}
	}
	reply(w, ProposeResult{Proposed: n, Proposals: proposals, Errors: []string{}}, nil)
}

// postRulesActivate handles POST /api/rules/activate.
// Activates the rule and immediately runs EvaluateActive through the rebuild path.
func (s *Server) postRulesActivate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
		// Version is the copy the page shows (RuleDetailView.Version); when
		// given, serve activates only that copy.
		Version string `json:"version"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	ru, err := s.App.Repo.ReadRule(in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if ru == nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "rule " + in.ID + " not found"})
		return
	}

	if in.Version != "" && in.Version != rules.Version(*ru) {
		reply(w, nil, conflict("rule %s changed since you read it; nothing was activated", ru.ID))
		return
	}

	by := s.App.Cfg.User
	if by == "" {
		by = "court"
	}

	if err := ru.Validate(); err != nil {
		reply(w, nil, bad("rule %s is not valid: %v", ru.ID, err))
		return
	}
	ru.Status = rules.StatusActive
	msg := "rule " + ru.ID + " → active by " + by
	if err := s.App.Repo.WriteRule(ctx, *ru, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	// Activation runs evaluation immediately through the serialized rebuild path.
	if err := s.rebuild(ctx); err != nil {
		reply(w, nil, bad("rebuild: %v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": ru.ID, "action": "activated", "by": by})

	reply(w, s.ruleDetail(ctx, *ru), nil)
}

// postRulesDeactivate handles POST /api/rules/deactivate.
// Sets the rule back to draft status.
func (s *Server) postRulesDeactivate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	ctx := r.Context()
	ru, err := s.App.Repo.ReadRule(in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if ru == nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "rule " + in.ID + " not found"})
		return
	}

	by := s.App.Cfg.User
	if by == "" {
		by = "court"
	}

	ru.Status = rules.StatusDraft
	msg := "rule " + ru.ID + " → draft by " + by
	if err := s.App.Repo.WriteRule(ctx, *ru, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": ru.ID, "action": "deactivated", "by": by})

	reply(w, s.ruleDetail(ctx, *ru), nil)
}

// getRulesVocabulary handles GET /api/rules/vocabulary.
func (s *Server) getRulesVocabulary(w http.ResponseWriter, r *http.Request) {
	reply(w, VocabularyView{Fields: rules.Vocabulary()}, nil)
}

// agentRuleDraft handles POST /api/agent/rule-draft.
// Creates or updates a DRAFT rule from an agent session. Refuses status="active".
// Sets created_by = "<harness>:<session>".
// An agent may only edit drafts it created itself (created_by == source(sess));
// editing another author's draft is refused with 400 naming the author.
func (s *Server) agentRuleDraft(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string     `json:"session"`
		Rule    rules.Rule `json:"rule"`
	}
	if err := decode(r, &in); err != nil {
		reply(w, nil, err)
		return
	}
	// Fix 6: validate the rule id up front before any disk access.
	if !rules.ValidID(in.Rule.ID) {
		reply(w, nil, bad("invalid rule id %q", in.Rule.ID))
		return
	}
	ctx := r.Context()
	sess, err := s.session(ctx, in.Session)
	if err != nil {
		reply(w, nil, err)
		return
	}

	// Refuse status="active" with a clear error.
	if in.Rule.Status == rules.StatusActive {
		reply(w, nil, bad("casebook_rule_draft cannot set status to active; Court activates rules on the page"))
		return
	}
	in.Rule.Status = rules.StatusDraft

	// Validate conditions.
	if err := rules.ValidateConditions(in.Rule.Match); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	by := source(sess)
	now := s.Now()

	existing, err := s.App.Repo.ReadRule(in.Rule.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	var msg string
	if existing == nil {
		// Create new draft.
		in.Rule.CreatedBy = by
		in.Rule.CreatedAt = now
		in.Rule.EditedAt = now
		msg = "rule " + in.Rule.ID + " created by " + by
	} else {
		// Refuse to edit an active rule.
		if existing.Status == rules.StatusActive {
			reply(w, nil, bad("casebook_rule_draft cannot edit an active rule; only Court can edit active rules"))
			return
		}
		// Fix 3: an agent may only edit drafts it created itself.
		if existing.CreatedBy != by {
			reply(w, nil, bad("casebook_rule_draft cannot edit rule %q: it was created by %s", in.Rule.ID, existing.CreatedBy))
			return
		}
		in.Rule.CreatedBy = existing.CreatedBy
		in.Rule.CreatedAt = existing.CreatedAt
		in.Rule.EditedAt = editedAt(*existing, in.Rule, now)
		msg = "rule " + in.Rule.ID + " edited by " + by
	}

	if unchanged(existing, in.Rule) {
		reply(w, s.ruleDetail(ctx, in.Rule), nil)
		return
	}
	if err := s.App.Repo.WriteRule(ctx, in.Rule, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": in.Rule.ID, "action": "drafted", "by": by})

	reply(w, s.ruleDetail(ctx, in.Rule), nil)
}

// conflict is a 409: the request was made against a copy that is no longer
// serve's.
func conflict(format string, a ...any) error {
	return httpError{code: http.StatusConflict, msg: fmt.Sprintf(format, a...), errCode: "conflict"}
}

// ruleDetail is what every rule route answers with: the rule, its track
// record, its first page of matches now and its version.
func (s *Server) ruleDetail(ctx context.Context, ru rules.Rule) RuleDetailView {
	rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
	// Conditions serve refuses are never previewed: a bad value would read
	// as "0 matches" (or match as something it doesn't say).
	matches := emptyPreview(ru.Match)
	if rules.ValidateConditions(ru.Match) == nil {
		matches = buildPreview(ru, s.Index.Result(), s.Now(), 0, 200)
	}
	return RuleDetailView{
		Rule:    ru,
		Record:  rec,
		Matches: matches,
		Version: rules.Version(ru),
		Invalid: invalidReason(ru),
	}
}

// editedAt is a saved draft's edited_at: now when its conditions or its
// proposal changed, else the existing one (spec §4.1). A re-save or a rename
// must not re-open the items Court rejected for the rule (§4.2).
func editedAt(existing, next rules.Rule, now time.Time) time.Time {
	if rules.SameMeaning(existing, next) {
		return existing.EditedAt
	}
	return now
}

// unchanged reports whether saving next would write existing's file again,
// byte for byte: nothing to write, commit or announce.
func unchanged(existing *rules.Rule, next rules.Rule) bool {
	if existing == nil {
		return false
	}
	a, errA := rules.Encode(*existing)
	b, errB := rules.Encode(next)
	return errA == nil && errB == nil && string(a) == string(b)
}

// buildPreview runs MatchAll on r against res and returns a paginated,
// grouped MatchPreview. It is safe to call with any (potentially invalid)
// rule; the error case returns an empty preview with error info.
//
// offset and limit follow the same defaults as the items list (limit 0 → 200).
func buildPreview(r rules.Rule, res engine.Result, now time.Time, offset, limit int) MatchPreview {
	matches, err := r.MatchAll(res, now)
	if err != nil {
		// MatchAll errors are returned as 400 by callers that validate first;
		// here we return an empty preview so callers that embed the preview
		// still get a valid response.
		return emptyPreview(r.Match)
	}

	// Count by reason.
	byReason := map[string]int{}
	for _, m := range matches {
		reason := m.Reason
		if reason == "" {
			reason = "matched"
		}
		byReason[reason]++
	}
	reasons := make([]ReasonCount, 0, len(byReason))
	for reason, count := range byReason {
		reasons = append(reasons, ReasonCount{Reason: reason, Count: count})
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i].Reason < reasons[j].Reason })

	// Group by repo.
	repoCount := map[string]int{}
	repoReasons := map[string]map[string]int{}
	for _, m := range matches {
		it, ok := res.Find(m.Key)
		if !ok {
			continue
		}
		repo := it.Repo
		if repo == "" {
			repo = it.ID
		}
		repoCount[repo]++
		if repoReasons[repo] == nil {
			repoReasons[repo] = map[string]int{}
		}
		reason := m.Reason
		if reason == "" {
			reason = "matched"
		}
		repoReasons[repo][reason]++
	}
	groups := make([]RepoGroup, 0, len(repoCount))
	for repo, count := range repoCount {
		rs := make([]ReasonCount, 0, len(repoReasons[repo]))
		for reason, c := range repoReasons[repo] {
			rs = append(rs, ReasonCount{Reason: reason, Count: c})
		}
		sort.Slice(rs, func(i, j int) bool { return rs[i].Reason < rs[j].Reason })
		groups = append(groups, RepoGroup{Repo: repo, Count: count, Reasons: rs})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Repo < groups[j].Repo })

	// Paginate.
	total := len(matches)
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := matches[offset:end]
	rows := make([]MatchRow, 0, len(page))
	for _, m := range page {
		it, ok := res.Find(m.Key)
		reason := m.Reason
		if reason == "" {
			reason = "matched"
		}
		row := MatchRow{Key: m.Key, Reason: reason}
		if ok {
			row.Repo = it.Repo
			row.Title = it.Title
			row.Status = string(it.Status)
		}
		rows = append(rows, row)
	}

	if reasons == nil {
		reasons = []ReasonCount{}
	}
	if groups == nil {
		groups = []RepoGroup{}
	}
	if rows == nil {
		rows = []MatchRow{}
	}

	return MatchPreview{
		Total:        total,
		ByReason:     reasons,
		Groups:       groups,
		Page:         rows,
		Dispositions: rules.Dispositions(r.Match),
	}
}
