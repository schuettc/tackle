package serve

import "testing"

func listRow(t *testing.T, r *rig, id string) RuleRow {
	t.Helper()
	var list RulesView
	if code := r.do(t, "GET", "/api/rules", nil, &list); code != 200 {
		t.Fatalf("rules %d", code)
	}
	for _, row := range list.Rules {
		if row.Rule.ID == id {
			return row
		}
	}
	t.Fatalf("rule %s not listed", id)
	return RuleRow{}
}

// The rules list carries each rule's match and exclusion counts (the list's
// "N match · M excluded"), the same as the rule's own preview, computed once
// per rule content and index, not on every request.
func TestRulesListCountsMatchesAndExclusions(t *testing.T) {
	r, _ := newRuleRig(t)
	d := ruleDetail(t, r, "kind-repo")
	row := listRow(t, r, "kind-repo")
	if row.Matches != d.Matches.Total || row.Matches == 0 || row.Excluded != 0 {
		t.Fatalf("row matches %d excluded %d; the rule matches %d", row.Matches, row.Excluded, d.Matches.Total)
	}
	runs := r.s.ruleCountRuns.Load()
	listRow(t, r, "kind-repo")
	if got := r.s.ruleCountRuns.Load(); got != runs {
		t.Errorf("a second list with nothing changed counted again (%d -> %d)", runs, got)
	}

	// An exclusion changes both counts, at once.
	key := d.Matches.Page[0].Key
	if code := r.do(t, "POST", "/api/rules/exclude", map[string]any{"id": "kind-repo", "key": key}, nil); code != 200 {
		t.Fatalf("exclude %d", code)
	}
	row = listRow(t, r, "kind-repo")
	if row.Matches != d.Matches.Total-1 || row.Excluded != 1 {
		t.Fatalf("after one exclusion: matches %d excluded %d, want %d and 1", row.Matches, row.Excluded, d.Matches.Total-1)
	}

	// A new index (a sync) is counted again.
	runs = r.s.ruleCountRuns.Load()
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	listRow(t, r, "kind-repo")
	if got := r.s.ruleCountRuns.Load(); got == runs {
		t.Error("a rebuilt index did not recount")
	}
}
