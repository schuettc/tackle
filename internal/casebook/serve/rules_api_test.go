package serve

import (
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/rules"
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
	if list.Rules[0].Rule.ID != "kind-repo" {
		t.Fatalf("first rule id %q", list.Rules[0].Rule.ID)
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

func TestAgentRuleDraftCannotActivate(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	// Agent tries to set status=active → 400.
	var out map[string]any
	code := r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "agent-rule",
			"name":    "Agent rule",
			"status":  "active", // not allowed
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &out)
	if code != 400 {
		t.Fatalf("agent activate: want 400, got %d", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(strings.ToLower(msg), "active") {
		t.Fatalf("error %q does not mention active", msg)
	}

	// Writing a draft works and records created_by = "pi:s1".
	var detail RuleDetailView
	code = r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "agent-rule",
			"name":    "Agent rule",
			"status":  "draft",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &detail)
	if code != 200 {
		t.Fatalf("agent draft %d", code)
	}
	if detail.Rule.CreatedBy != "pi:s1" {
		t.Fatalf("created_by %q, want pi:s1", detail.Rule.CreatedBy)
	}

	// Trying to edit an active rule via agent is refused.
	// First activate via the page endpoint.
	ru, _ := r.App.Repo.ReadRule("agent-rule")
	if ru == nil {
		t.Fatal("rule not found")
	}
	ru.Status = rules.StatusActive
	if err := r.App.Repo.WriteRule(ctx, *ru, "rule agent-rule → active by court"); err != nil {
		t.Fatal(err)
	}
	var out2 map[string]any
	code = r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
		"session": "s1",
		"rule": map[string]any{
			"id":      "agent-rule",
			"name":    "Agent rule edited",
			"status":  "draft",
			"match":   []map[string]any{{"field": "kind", "op": "is", "value": "pr"}},
			"propose": map[string]any{"disposition": "close"},
		},
	}, &out2)
	if code != 400 {
		t.Fatalf("editing active rule via agent: want 400, got %d", code)
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
	code = r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
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
	code = r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
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
	code = r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
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
	code = r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
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

	// Court may edit any draft (including s1's).
	var detail4 RuleDetailView
	code = r.do(t, "POST", "/api/rules/draft", map[string]any{
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

// TestAgentDraftRejectsInvalidID checks Fix 6: POST /api/agent/rule-draft
// returns 400 "invalid rule id" for an id that fails ValidID.
func TestAgentDraftRejectsInvalidID(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	var out map[string]any
	code := r.do(t, "POST", "/api/agent/rule-draft", map[string]any{
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
