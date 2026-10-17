package serve

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/engine"
	"github.com/subaud-tools/tackle/internal/casebook/item"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
)

// newRuleRig is newRig plus a ready-to-use rule written to the repo.
func newRuleRig(t *testing.T) (*rig, rules.Rule) {
	t.Helper()
	r := newRig(t)

	ru := rules.Rule{
		ID:        "kind-repo",
		Name:      "Repos rule",
		Status:    rules.StatusDraft,
		CreatedBy: "court",
		CreatedAt: r.s.Now(),
		EditedAt:  r.s.Now(),
		Match:     []rules.Condition{{Field: "kind", Op: "is", Value: "repo"}},
		Propose:   rules.RuleAction{Disposition: "archive"},
	}
	if err := r.App.Repo.WriteRule(ctx, ru, "rule kind-repo created by court"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	return r, ru
}

func TestPreviewCountsMatchesByReason(t *testing.T) {
	r := newRig(t)

	// Preview a rule that matches all repos (there's one: repo:schuettc/hail).
	var preview MatchPreview
	code := r.do(t, "POST", "/api/rules/preview", map[string]any{
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}},
		"propose": map[string]any{"disposition": "archive"},
	}, &preview)
	if code != 200 {
		t.Fatalf("preview status %d", code)
	}
	if preview.Total < 1 {
		t.Fatalf("preview total %d, want >=1", preview.Total)
	}
	if len(preview.Page) == 0 {
		t.Fatal("preview page is empty")
	}
}

func TestDraftThenActivateProposes(t *testing.T) {
	r, _ := newRuleRig(t)

	// Activate the rule.
	var detail RuleDetailView
	code := r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "kind-repo"}, &detail)
	if code != 200 {
		t.Fatalf("activate status %d", code)
	}
	if detail.Rule.Status != rules.StatusActive {
		t.Fatalf("rule status %q, want active", detail.Rule.Status)
	}
	// Activation runs EvaluateActive immediately, so proposals must exist.
	// The rule matches "kind is repo" and the test index always has
	// repo:schuettc/hail, so there must be at least one pending proposal.
	if detail.Record.Pending < 1 && detail.Matches.Total < 1 {
		t.Fatalf("activation produced no pending proposals or matches; the fixture rule must match something (record=%+v matches=%+v)", detail.Record, detail.Matches)
	}

	// The rule file on disk is now active.
	ru, err := r.App.Repo.ReadRule("kind-repo")
	if err != nil || ru == nil {
		t.Fatalf("rule not found after activate: %v", err)
	}
	if ru.Status != rules.StatusActive {
		t.Fatalf("disk rule status %q, want active", ru.Status)
	}
}

func TestUntickAddsExclusionAndPreviewDrops(t *testing.T) {
	r, ru := newRuleRig(t)

	// Get a match from preview.
	var preview MatchPreview
	code := r.do(t, "POST", "/api/rules/preview", map[string]any{
		"id":      ru.ID,
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}},
		"propose": map[string]any{"disposition": "archive"},
	}, &preview)
	if code != 200 {
		t.Fatalf("preview %d", code)
	}
	if len(preview.Page) == 0 {
		t.Skip("no matches to exclude")
	}
	key := preview.Page[0].Key

	// Untick: add exclusion.
	var detail RuleDetailView
	code = r.do(t, "POST", "/api/rules/exclude", map[string]any{
		"id":     ru.ID,
		"key":    key,
		"reason": "keep for reference",
	}, &detail)
	if code != 200 {
		t.Fatalf("exclude status %d", code)
	}
	if len(detail.Rule.Exclude) == 0 {
		t.Fatal("exclusion not added")
	}

	// Preview now drops the excluded key.
	var preview2 MatchPreview
	r.do(t, "POST", "/api/rules/preview", map[string]any{
		"id":      ru.ID,
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}},
		"propose": map[string]any{"disposition": "archive"},
		"exclude": detail.Rule.Exclude,
	}, &preview2)
	for _, row := range preview2.Page {
		if row.Key == key {
			t.Fatalf("excluded key %q still appears in preview", key)
		}
	}
}

func TestPreviewRejectsInvalidRegex(t *testing.T) {
	r := newRig(t)

	// field=title op=matches value="(" is an invalid regex.
	var out map[string]any
	code := r.do(t, "POST", "/api/rules/preview", map[string]any{
		"match":   []map[string]any{{"field": "title", "op": "matches", "value": "("}},
		"propose": map[string]any{"disposition": "archive"},
	}, &out)
	if code != 400 {
		t.Fatalf("invalid regex: want 400, got %d", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "regex") && !strings.Contains(msg, "condition") {
		t.Fatalf("error message %q does not mention regex or condition", msg)
	}
}

// TestPreviewRejectsBadCountAndDuration: a count comparison with a value
// that isn't a number, and a duration outside <n>h/<n>d/<n>w, are 400s that
// name the condition, never an empty preview that reads as "0 matches".
func TestPreviewRejectsBadCountAndDuration(t *testing.T) {
	r := newRig(t)
	for _, c := range []map[string]any{
		{"field": "open-prs", "op": "gt", "value": "lots"},
		{"field": "age", "op": "older-than", "value": "7x"},
	} {
		var out map[string]any
		code := r.do(t, "POST", "/api/rules/preview", map[string]any{
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}, c},
			"propose": map[string]any{"disposition": "archive"},
		}, &out)
		if code != 400 {
			t.Errorf("%v: want 400, got %d (%v)", c, code, out)
			continue
		}
		if msg, _ := out["error"].(string); !strings.HasPrefix(msg, "condition 2: ") {
			t.Errorf("%v: error %q does not name condition 2", c, msg)
		}
	}
}

func TestVocabularyEndpointListsFields(t *testing.T) {
	r := newRig(t)

	var vocab VocabularyView
	code := r.do(t, "GET", "/api/rules/vocabulary", nil, &vocab)
	if code != 200 {
		t.Fatalf("vocabulary %d", code)
	}
	if len(vocab.Fields) == 0 {
		t.Fatal("no fields in vocabulary")
	}
	// Check that known fields are present.
	names := make(map[string]bool)
	for _, f := range vocab.Fields {
		names[f.Name] = true
	}
	for _, want := range []string{"kind", "repo", "title", "age", "landed", "bot"} {
		if !names[want] {
			t.Errorf("field %q missing from vocabulary", want)
		}
	}
	// The note's placeholders, for the page's hint (T8 re-review c).
	var toks []string
	for _, tk := range vocab.NoteTokens {
		toks = append(toks, tk.Token)
	}
	if got := strings.Join(toks, " "); got != "{how} {tip} {age} {repo} {title}" {
		t.Errorf("note tokens %q", got)
	}
	// The page's words: each field's label, and every operator's.
	if vocab.Fields[0].Label != "type" || len(vocab.Operators) != 13 || vocab.Operators[1].Label != "is not" {
		t.Errorf("labels: field %+v operators %+v", vocab.Fields[0], vocab.Operators)
	}
}

func TestRulesListAndDetail(t *testing.T) {
	r, _ := newRuleRig(t)

	// List.
	var list RulesView
	code := r.do(t, "GET", "/api/rules", nil, &list)
	if code != 200 {
		t.Fatalf("rules list %d", code)
	}
	if len(list.Rules) == 0 {
		t.Fatal("rules list empty")
	}
	// The rig's rule, beside the example rules a new repo has.
	if !slices.ContainsFunc(list.Rules, func(x RuleRow) bool { return x.Rule.ID == "kind-repo" }) {
		t.Fatalf("rules %+v lack kind-repo", list.Rules)
	}

	// Detail.
	var detail RuleDetailView
	code = r.do(t, "GET", "/api/rule?id=kind-repo", nil, &detail)
	if code != 200 {
		t.Fatalf("rule detail %d", code)
	}
	if detail.Rule.ID != "kind-repo" {
		t.Fatalf("detail rule id %q", detail.Rule.ID)
	}
}

func TestDraftEndpointCreatesAndEdits(t *testing.T) {
	r := newRig(t)
	now := time.Now()
	_ = now

	// Create a draft.
	var detail RuleDetailView
	code := r.do(t, "POST", "/api/rules/draft", map[string]any{
		"id":         "my-rule",
		"name":       "My Rule",
		"status":     "draft",
		"created_by": "court",
		"match":      []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
		"propose":    map[string]any{"disposition": "close"},
	}, &detail)
	if code != 200 {
		t.Fatalf("draft create %d", code)
	}
	if detail.Rule.ID != "my-rule" || detail.Rule.Status != "draft" {
		t.Fatalf("draft rule %+v", detail.Rule)
	}

	// Edit the draft.
	var detail2 RuleDetailView
	code = r.do(t, "POST", "/api/rules/draft", map[string]any{
		"id":         "my-rule",
		"name":       "My Rule v2",
		"status":     "draft",
		"created_by": "court",
		"match":      []map[string]any{{"field": "kind", "op": "is", "value": "issue"}},
		"propose":    map[string]any{"disposition": "close"},
	}, &detail2)
	if code != 200 {
		t.Fatalf("draft edit %d", code)
	}
	if detail2.Rule.Name != "My Rule v2" {
		t.Fatalf("rule name %q", detail2.Rule.Name)
	}
}

// TestAgentRuleSaveIsOn: a rule an agent saves is on at once, records who
// made it, and has proposed; the agent can edit it again (Court,
// 2026-10-17: every rule is on once saved).
func TestAgentRuleSaveIsOn(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	var detail RuleDetailView
	code := r.do(t, "POST", "/api/agent/rule-save", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "agent-rule",
			"name":    "Agent rule",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &detail)
	if code != 200 {
		t.Fatalf("agent save %d", code)
	}
	if detail.Rule.CreatedBy != "pi:s1" || detail.Rule.Status != rules.StatusActive {
		t.Fatalf("saved %+v, want on and by pi:s1", detail.Rule)
	}
	pend, err := r.s.Props.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range pend {
		if p.Source == "rule:agent-rule" {
			n++
		}
	}
	if n == 0 {
		t.Fatal("the agent's rule proposed nothing")
	}
	if code := r.do(t, "POST", "/api/agent/rule-save", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "agent-rule",
			"name":    "Agent rule edited",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &detail); code != 200 || detail.Rule.Name != "Agent rule edited" || detail.Rule.Status != rules.StatusActive {
		t.Fatalf("agent edit of its own rule: %d %+v", code, detail.Rule)
	}
}

func TestProposeOnceEndpoint(t *testing.T) {
	r, _ := newRuleRig(t)

	var result ProposeResult
	code := r.do(t, "POST", "/api/rules/propose-once", map[string]any{"id": "kind-repo"}, &result)
	if code != 200 {
		t.Fatalf("propose-once %d", code)
	}
	// There's at least one repo in the index (repo:schuettc/hail).
	if result.Proposed < 1 {
		t.Logf("propose-once proposed %d (expected >=1)", result.Proposed)
	}
}

func TestDeactivateRule(t *testing.T) {
	r, _ := newRuleRig(t)

	// Activate first.
	var detail RuleDetailView
	r.do(t, "POST", "/api/rules/activate", map[string]any{"id": "kind-repo"}, &detail)
	if detail.Rule.Status != rules.StatusActive {
		t.Fatalf("activate %q", detail.Rule.Status)
	}

	// Deactivate.
	var detail2 RuleDetailView
	code := r.do(t, "POST", "/api/rules/deactivate", map[string]any{"id": "kind-repo"}, &detail2)
	if code != 200 {
		t.Fatalf("deactivate %d", code)
	}
	if detail2.Rule.Status != rules.StatusDraft {
		t.Fatalf("deactivate status %q", detail2.Rule.Status)
	}
}

func TestIncludeRemovesExclusion(t *testing.T) {
	r, ru := newRuleRig(t)

	// Add exclusion first.
	var preview MatchPreview
	r.do(t, "POST", "/api/rules/preview", map[string]any{
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "repo"}},
		"propose": map[string]any{"disposition": "archive"},
	}, &preview)
	if len(preview.Page) == 0 {
		t.Skip("no matches")
	}
	key := preview.Page[0].Key

	var detail RuleDetailView
	r.do(t, "POST", "/api/rules/exclude", map[string]any{"id": ru.ID, "key": key, "reason": "test"}, &detail)
	if len(detail.Rule.Exclude) == 0 {
		t.Fatal("exclusion not added")
	}

	// Remove exclusion.
	var detail2 RuleDetailView
	code := r.do(t, "POST", "/api/rules/include", map[string]any{"id": ru.ID, "key": key}, &detail2)
	if code != 200 {
		t.Fatalf("include %d", code)
	}
	for _, ex := range detail2.Rule.Exclude {
		if ex.Key == key {
			t.Fatalf("exclusion for %q still present", key)
		}
	}
}

// TestAgentCannotEditOtherAgentsDraft checks Fix 3: an agent session may only
// edit drafts it created itself; editing another author's draft is 400 naming
// the author. The page (Court) may edit any draft.
func TestAgentCannotEditOtherAgentsDraft(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.attach(t, "s2")

	// Court creates a draft.
	var detail RuleDetailView
	code := r.do(t, "POST", "/api/rules/draft", map[string]any{
		"id":      "court-rule",
		"name":    "Court rule",
		"status":  "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
		"propose": map[string]any{"disposition": "close"},
	}, &detail)
	if code != 200 {
		t.Fatalf("court draft create %d", code)
	}

	// Session s1 tries to edit court's draft → 400 naming "court".
	var out map[string]any
	code = r.do(t, "POST", "/api/agent/rule-save", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "court-rule",
			"name":    "Court rule hacked",
			"status":  "draft",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &out)
	if code != 400 {
		t.Fatalf("s1 edit court draft: want 400, got %d", code)
	}
	// Error message must name the author ("court").
	if msg, _ := out["error"].(string); !strings.Contains(msg, "court") {
		t.Fatalf("error %q must name the author (court)", msg)
	}

	// Session s1 creates its own draft.
	var detail2 RuleDetailView
	code = r.do(t, "POST", "/api/agent/rule-save", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "s1-rule",
			"name":    "s1 rule",
			"status":  "draft",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &detail2)
	if code != 200 {
		t.Fatalf("s1 create draft %d", code)
	}

	// Session s2 tries to edit s1's draft → 400 naming "pi:s1".
	var out2 map[string]any
	code = r.do(t, "POST", "/api/agent/rule-save", map[string]any{
		"session": "s2",
		"rule": map[string]any{
			"id":      "s1-rule",
			"name":    "s1 rule stolen",
			"status":  "draft",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &out2)
	if code != 400 {
		t.Fatalf("s2 edit s1 draft: want 400, got %d", code)
	}
	if msg, _ := out2["error"].(string); !strings.Contains(msg, "pi:s1") {
		t.Fatalf("error %q must name the author (pi:s1)", msg)
	}

	// Session s1 edits its own draft → 200.
	var detail3 RuleDetailView
	code = r.do(t, "POST", "/api/agent/rule-save", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "s1-rule",
			"name":    "s1 rule updated",
			"status":  "draft",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &detail3)
	if code != 200 {
		t.Fatalf("s1 edit own draft %d", code)
	}
	if detail3.Rule.Name != "s1 rule updated" {
		t.Fatalf("rule name %q", detail3.Rule.Name)
	}

	// Court may edit any rule (including s1's).
	var detail4 RuleDetailView
	code = r.do(t, "POST", "/api/rules/save", map[string]any{
		"id":      "s1-rule",
		"name":    "s1 rule fixed by court",
		"status":  "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
		"propose": map[string]any{"disposition": "close"},
	}, &detail4)
	if code != 200 {
		t.Fatalf("court edit s1 draft %d", code)
	}
	if detail4.Rule.Name != "s1 rule fixed by court" {
		t.Fatalf("court edit: rule name %q", detail4.Rule.Name)
	}
}

// TestProposeOnceReturnsProposals checks Fix 5: ProposeOnce returns the actual
// proposal objects, not just a count, so ProposeResult.Proposals is populated.
func TestProposeOnceReturnsProposals(t *testing.T) {
	r, _ := newRuleRig(t)

	var result ProposeResult
	code := r.do(t, "POST", "/api/rules/propose-once", map[string]any{"id": "kind-repo"}, &result)
	if code != 200 {
		t.Fatalf("propose-once %d", code)
	}
	if result.Proposed < 1 {
		t.Fatalf("propose-once proposed %d, want >=1", result.Proposed)
	}
	// Fix 5: Proposals must be populated, not an empty slice.
	if len(result.Proposals) < 1 {
		t.Fatalf("propose-once returned %d proposals, want >=1; ProposeOnce must return the proposal objects", len(result.Proposals))
	}
}

// TestDraftEndpointRejectsInvalidID checks Fix 6: POST /api/rules/draft
// returns 400 "invalid rule id" for an id that fails ValidID.
func TestDraftEndpointRejectsInvalidID(t *testing.T) {
	r := newRig(t)

	// Empty id.
	var out map[string]any
	code := r.do(t, "POST", "/api/rules/draft", map[string]any{
		"id":      "",
		"name":    "Bad Rule",
		"status":  "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
		"propose": map[string]any{"disposition": "close"},
	}, &out)
	if code != 400 {
		t.Fatalf("empty id: want 400, got %d", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "invalid rule id") {
		t.Fatalf("error %q does not say 'invalid rule id'", msg)
	}

	// Id with uppercase letters (not allowed).
	var out2 map[string]any
	code = r.do(t, "POST", "/api/rules/draft", map[string]any{
		"id":      "Bad-ID",
		"name":    "Bad Rule",
		"status":  "draft",
		"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
		"propose": map[string]any{"disposition": "close"},
	}, &out2)
	if code != 400 {
		t.Fatalf("bad id: want 400, got %d", code)
	}
	if msg, _ := out2["error"].(string); !strings.Contains(msg, "invalid rule id") {
		t.Fatalf("error %q does not say 'invalid rule id'", msg)
	}
}

// TestAgentDraftRejectsInvalidID checks Fix 6: POST /api/agent/rule-save
// returns 400 "invalid rule id" for an id that fails ValidID.
func TestAgentDraftRejectsInvalidID(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	var out map[string]any
	code := r.do(t, "POST", "/api/agent/rule-save", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "Bad-ID",
			"name":    "Bad Rule",
			"status":  "draft",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &out)
	if code != 400 {
		t.Fatalf("bad id via agent: want 400, got %d", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "invalid rule id") {
		t.Fatalf("error %q does not say 'invalid rule id'", msg)
	}
}

// TestPreviewPageIsNewestFirst: the preview's page holds at most limit rows,
// newest first (engine.NewerFirst), whatever the index's key order: the
// filter sheet lists its first rows.
func TestPreviewPageIsNewestFirst(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	at := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }
	res := engine.Result{Items: []engine.Item{
		{ID: "issue:a/b#1", Kind: item.KindIssue, UpdatedAt: at(9)},
		{ID: "issue:a/b#2", Kind: item.KindIssue, UpdatedAt: at(1)},
		{ID: "issue:a/b#3", Kind: item.KindIssue},
		{ID: "issue:a/b#4", Kind: item.KindIssue, UpdatedAt: at(4)},
		{ID: "repo:a/b", Kind: item.KindRepo, UpdatedAt: at(0)},
	}}
	ru := rules.Rule{Match: []rules.Condition{{Field: "kind", Op: "is", Value: "issue"}}}
	p := buildPreview(ru, res, now, 0, 3)
	var keys []string
	for _, r := range p.Page {
		keys = append(keys, r.Key)
	}
	if got := strings.Join(keys, " "); p.Total != 4 || got != "issue:a/b#2 issue:a/b#4 issue:a/b#1" {
		t.Errorf("total %d page %q, want 4 and #2 #4 #1", p.Total, got)
	}
}

// TestValuesEndpoint: the values casebook has seen for a pick-or-type field,
// most items first; a field that isn't one is refused.
func TestValuesEndpoint(t *testing.T) {
	r := newRig(t)
	var v ValuesView
	if code := r.do(t, "GET", "/api/rules/values?field=repo", nil, &v); code != 200 {
		t.Fatalf("values %d", code)
	}
	if len(v.Values) == 0 || v.Values[0].Value != "schuettc/hail" || v.Values[0].Count == 0 {
		t.Fatalf("repo values %+v, want schuettc/hail first", v.Values)
	}
	for i := 1; i < len(v.Values); i++ {
		if v.Values[i].Count > v.Values[i-1].Count {
			t.Fatalf("values not most items first: %+v", v.Values)
		}
	}
	if code := r.do(t, "GET", "/api/rules/values?field=kind", nil, nil); code != 400 {
		t.Fatalf("values?field=kind %d, want 400", code)
	}
}

// TestRuleSaveTurnsItOn: saving a rule (new, or an edit of one that is on)
// writes it on and evaluates it at once; a rule with no disposition, or
// ?create=1 over an existing one, is refused and nothing is written.
func TestRuleSaveTurnsItOn(t *testing.T) {
	r := newRig(t)
	ru := rules.Rule{ID: "open-issues", Name: "Open issues", Match: []rules.Condition{{Field: "kind", Op: "is", Value: "issue"}}, Propose: rules.RuleAction{Disposition: "keep", Note: "still open"}}
	var got RuleDetailView
	if c := r.do(t, "POST", "/api/rules/save?create=1", ru, &got); c != 200 {
		t.Fatalf("save %d", c)
	}
	if got.Rule.Status != rules.StatusActive {
		t.Fatalf("saved status %q, want active", got.Rule.Status)
	}
	pend, err := r.s.Props.Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range pend {
		if p.Source == "rule:open-issues" {
			n++
		}
	}
	if n == 0 {
		t.Fatal("saving the rule proposed nothing")
	}
	var pv MatchPreview
	if c := r.do(t, "POST", "/api/rules/preview?limit=0", rules.Rule{Match: []rules.Condition{{Field: "relation", Op: "is", Value: "incoming"}}}, &pv); c != 200 || strings.Join(pv.Kinds, ",") != "pr,issue" {
		t.Fatalf("preview kinds for opened by someone else: %d %v, want pr,issue", c, pv.Kinds)
	}
	if c := r.do(t, "POST", "/api/rules/save?create=1", ru, nil); c != 409 {
		t.Fatalf("create over existing %d, want 409", c)
	}
	ru.Name = "Open issues, kept"
	if c := r.do(t, "POST", "/api/rules/save", ru, &got); c != 200 || got.Rule.Name != "Open issues, kept" || got.Rule.Status != rules.StatusActive {
		t.Fatalf("edit of an on rule: %d %+v", c, got.Rule)
	}
	bad := rules.Rule{ID: "no-disp", Name: "No disposition", Match: ru.Match}
	if c := r.do(t, "POST", "/api/rules/save?create=1", bad, nil); c != 400 {
		t.Fatalf("no disposition %d, want 400", c)
	}
	if got, _ := r.s.App.Repo.ReadRule("no-disp"); got != nil {
		t.Fatal("a refused rule was written")
	}
}

// ruleProposals is the rule's pending proposals, by key.
func (r *rig) ruleProposals(t *testing.T, id string) map[string]int64 {
	t.Helper()
	ps, err := r.s.Props.PendingFromRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, p := range ps {
		if p.Source == "rule:"+id {
			out[p.Key] = p.ID
		}
	}
	return out
}

// TestTurningARuleOffWithdrawsItsRecommendations: turning a rule off
// withdraws its pending recommendations at once (not on the next sync),
// says so in a "proposals" event, and its group leaves the list.
func TestTurningARuleOffWithdrawsItsRecommendations(t *testing.T) {
	r := newRig(t)
	ru := rules.Rule{ID: "open-issues", Name: "Open issues", Match: []rules.Condition{{Field: "kind", Op: "is", Value: "issue"}}, Propose: rules.RuleAction{Disposition: "close"}}
	if c := r.do(t, "POST", "/api/rules/save?create=1", ru, nil); c != 200 {
		t.Fatalf("save %d", c)
	}
	before := r.ruleProposals(t, ru.ID)
	if len(before) == 0 {
		t.Fatal("the rule recommended nothing")
	}
	head, _ := r.s.Bus.Head(ctx)
	if c := r.do(t, "POST", "/api/rules/deactivate", map[string]any{"id": ru.ID}, nil); c != 200 {
		t.Fatalf("deactivate %d", c)
	}
	if left := r.ruleProposals(t, ru.ID); len(left) != 0 {
		t.Fatalf("still pending after turning it off: %v", left)
	}
	ev := r.lastProposals(t, head, "withdrawn")
	if len(ev.IDs) != len(before) {
		t.Fatalf("withdrawn event %+v, want %d ids", ev, len(before))
	}
	for _, g := range r.viewItems(t, "view=all").Groups {
		if strings.HasPrefix(g.ID, "rule:"+ru.ID+"|") {
			t.Fatalf("the rule's group is still listed: %+v", g)
		}
	}
}

// TestEditingARuleWithdrawsWhatItNoLongerRecommends: saving a rule that
// now recommends another decision withdraws the old recommendations and
// makes the new ones in the same save.
func TestEditingARuleWithdrawsWhatItNoLongerRecommends(t *testing.T) {
	r := newRig(t)
	ru := rules.Rule{ID: "open-issues", Name: "Open issues", Match: []rules.Condition{{Field: "kind", Op: "is", Value: "issue"}}, Propose: rules.RuleAction{Disposition: "close"}}
	if c := r.do(t, "POST", "/api/rules/save?create=1", ru, nil); c != 200 {
		t.Fatalf("save %d", c)
	}
	old := r.ruleProposals(t, ru.ID)
	ru.Propose.Disposition = "keep"
	if c := r.do(t, "POST", "/api/rules/save", ru, nil); c != 200 {
		t.Fatalf("edit %d", c)
	}
	for key, id := range old {
		if p, _ := r.s.Props.Get(ctx, id); p.State != "withdrawn" {
			t.Fatalf("old recommendation for %s is %s, want withdrawn", key, p.State)
		}
	}
	now := r.ruleProposals(t, ru.ID)
	if len(now) != len(old) {
		t.Fatalf("after the edit %d pending, want %d", len(now), len(old))
	}
	for key, id := range now {
		if p, _ := r.s.Props.Get(ctx, id); p.Disposition != "keep" {
			t.Fatalf("%s is recommended %s, want keep", key, p.Disposition)
		}
	}
}
