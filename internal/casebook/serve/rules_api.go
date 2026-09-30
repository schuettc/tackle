package serve

import (
	"net/http"
	"sort"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/rules"
)

// getRules handles GET /api/rules.
// Returns all rules with their track records.
func (s *Server) getRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, _ := s.App.Repo.Rules()
	rows := make([]RuleRow, 0, len(all))
	for _, ru := range all {
		rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
		rows = append(rows, RuleRow{Rule: ru, Record: rec})
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
	if err != nil {
		reply(w, nil, bad("%v", err))
		return
	}
	if ru == nil {
		reply(w, nil, httpError{code: http.StatusNotFound, msg: "rule " + id + " not found"})
		return
	}
	rec, _ := rules.RuleRecord(ctx, s.Props, id)
	preview := buildPreview(*ru, s.Index.Result(), s.Now(), 0, 200)
	reply(w, RuleDetailView{Rule: *ru, Record: rec, Matches: preview}, nil)
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

	// Validate conditions and proposal before touching disk.
	for i, c := range in.Match {
		if err := rules.ValidateCondition(c); err != nil {
			reply(w, nil, bad("condition %d: %v", i, err))
			return
		}
	}

	now := s.Now()
	existing, err := s.App.Repo.ReadRule(in.ID)
	if err != nil {
		reply(w, nil, bad("%v", err))
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
	} else {
		// Edit existing draft.
		if existing.Status == rules.StatusActive {
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
		rec, _ := rules.RuleRecord(ctx, s.Props, in.ID)
		reply(w, RuleDetailView{Rule: in, Record: rec, Matches: buildPreview(in, s.Index.Result(), now, 0, 200)}, nil)
		return
	}
	if err := s.App.Repo.WriteRule(ctx, in, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": in.ID, "action": "drafted", "by": by})

	rec, _ := rules.RuleRecord(ctx, s.Props, in.ID)
	preview := buildPreview(in, s.Index.Result(), now, 0, 200)
	reply(w, RuleDetailView{Rule: in, Record: rec, Matches: preview}, nil)
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
	for i, c := range in.Match {
		if err := rules.ValidateCondition(c); err != nil {
			reply(w, nil, bad("condition %d: %v", i, err))
			return
		}
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
			rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
			preview := buildPreview(*ru, s.Index.Result(), s.Now(), 0, 200)
			reply(w, RuleDetailView{Rule: *ru, Record: rec, Matches: preview}, nil)
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

	rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
	preview := buildPreview(*ru, s.Index.Result(), s.Now(), 0, 200)
	reply(w, RuleDetailView{Rule: *ru, Record: rec, Matches: preview}, nil)
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
		rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
		preview := buildPreview(*ru, s.Index.Result(), s.Now(), 0, 200)
		reply(w, RuleDetailView{Rule: *ru, Record: rec, Matches: preview}, nil)
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

	rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
	preview := buildPreview(*ru, s.Index.Result(), s.Now(), 0, 200)
	reply(w, RuleDetailView{Rule: *ru, Record: rec, Matches: preview}, nil)
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

	rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
	preview := buildPreview(*ru, s.Index.Result(), s.Now(), 0, 200)
	reply(w, RuleDetailView{Rule: *ru, Record: rec, Matches: preview}, nil)
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

	rec, _ := rules.RuleRecord(ctx, s.Props, ru.ID)
	preview := buildPreview(*ru, s.Index.Result(), s.Now(), 0, 200)
	reply(w, RuleDetailView{Rule: *ru, Record: rec, Matches: preview}, nil)
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
	for i, c := range in.Rule.Match {
		if err := rules.ValidateCondition(c); err != nil {
			reply(w, nil, bad("condition %d: %v", i, err))
			return
		}
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
		rec, _ := rules.RuleRecord(ctx, s.Props, in.Rule.ID)
		reply(w, RuleDetailView{Rule: in.Rule, Record: rec, Matches: buildPreview(in.Rule, s.Index.Result(), now, 0, 200)}, nil)
		return
	}
	if err := s.App.Repo.WriteRule(ctx, in.Rule, msg); err != nil {
		reply(w, nil, bad("%v", err))
		return
	}

	s.publish(ctx, "rules", map[string]any{"id": in.Rule.ID, "action": "drafted", "by": by})

	rec, _ := rules.RuleRecord(ctx, s.Props, in.Rule.ID)
	preview := buildPreview(in.Rule, s.Index.Result(), now, 0, 200)
	reply(w, RuleDetailView{Rule: in.Rule, Record: rec, Matches: preview}, nil)
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
		return MatchPreview{ByReason: []ReasonCount{}, Groups: []RepoGroup{}, Page: []MatchRow{}}
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
		Total:    total,
		ByReason: reasons,
		Groups:   groups,
		Page:     rows,
	}
}
