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

// TestPlanWithNothingToPlanMakesNoPlan: "plan all" when every decided item is
// already in a plan (another tab planned them a moment ago), or when nothing
// is decided, or naming no items, makes no plan: serve refuses with 422
// "nothing to plan…" rather than keeping an empty job.
func TestPlanWithNothingToPlanMakesNoPlan(t *testing.T) {
	r := newRig(t)
	jobs := func() int {
		js, err := r.s.Apply.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(js)
	}
	refused := func(name string, body map[string]any, want string) {
		t.Helper()
		before := jobs()
		var out struct {
			Error string `json:"error"`
		}
		if c := r.do(t, "POST", "/api/apply/plan", body, &out); c != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d (%q), want 422", name, c, out.Error)
		}
		if out.Error != want {
			t.Fatalf("%s: error %q, want %q", name, out.Error, want)
		}
		if n := jobs(); n != before {
			t.Fatalf("%s: a refusal made a job: %d jobs, want %d", name, n, before)
		}
	}

	refused("nothing decided", map[string]any{"all": true},
		"nothing to plan: no decided item is waiting to be applied")
	refused("no keys", map[string]any{"keys": []string{}},
		"nothing to plan: no decided item is waiting to be applied")

	if c := r.do(t, "POST", "/api/decide", map[string]any{
		"keys": []string{"pr:schuettc/hail#3"}, "disposition": "close", "note": "stale",
	}, nil); c != http.StatusOK {
		t.Fatalf("decide: %d", c)
	}
	var first PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, &first); c != http.StatusOK {
		t.Fatalf("plan all: %d", c)
	}
	if len(first.Job.Steps) == 0 {
		t.Fatalf("the first plan has no steps")
	}
	refused("everything already planned", map[string]any{"all": true},
		"nothing to plan: every decided item is already in a plan or a job")
}
