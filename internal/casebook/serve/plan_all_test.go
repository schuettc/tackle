package serve

import (
	"net/http"
	"sort"
	"strings"
	"testing"
)

// TestPlanAllLeavesOutItemsAlreadyInAPlan: "plan all" plans the decided
// items that aren't already in an unapproved plan, so pressing it twice
// doesn't make a second plan of the same steps. Once that plan is discarded
// its items are plannable again.
func TestPlanAllLeavesOutItemsAlreadyInAPlan(t *testing.T) {
	r := newRig(t)
	if c := r.do(t, "POST", "/api/decide", map[string]any{
		"keys": []string{"pr:schuettc/hail#3", "issue:schuettc/hail#4"}, "disposition": "close", "note": "stale",
	}, nil); c != http.StatusOK {
		t.Fatalf("decide: %d", c)
	}
	keys := func(v PlanView) string {
		var ks []string
		for _, s := range v.Job.Steps {
			ks = append(ks, s.Key)
		}
		sort.Strings(ks)
		return strings.Join(ks, " ")
	}

	var first PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{"pr:schuettc/hail#3"}}, &first); c != http.StatusOK {
		t.Fatalf("plan the PR: %d", c)
	}
	if keys(first) != "pr:schuettc/hail#3" {
		t.Fatalf("first plan = %q", keys(first))
	}
	var all PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, &all); c != http.StatusOK {
		t.Fatalf("plan all: %d", c)
	}
	if keys(all) != "issue:schuettc/hail#4" {
		t.Fatalf("plan all = %q, want only the issue (the PR is in plan %d)", keys(all), first.Job.ID)
	}

	// Discard both: plan all takes everything again.
	for _, id := range []int64{first.Job.ID, all.Job.ID} {
		if c := r.do(t, "POST", "/api/apply/cancel", map[string]any{"plan_id": id}, nil); c != http.StatusOK {
			t.Fatalf("cancel %d: %d", id, c)
		}
	}
	var again PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, &again); c != http.StatusOK {
		t.Fatalf("plan all again: %d", c)
	}
	if keys(again) != "issue:schuettc/hail#4 pr:schuettc/hail#3" {
		t.Fatalf("plan all after discarding = %q, want both", keys(again))
	}
}
