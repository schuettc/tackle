package serve

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// decideTwo decides the rig's PR and issue (close), so both are to apply.
func decideTwo(t *testing.T, r *rig) {
	t.Helper()
	if c := r.do(t, "POST", "/api/decide", map[string]any{
		"keys": []string{"pr:schuettc/hail#3", "issue:schuettc/hail#4"}, "disposition": "close", "note": "stale",
	}, nil); c != http.StatusOK {
		t.Fatalf("decide: %d", c)
	}
}

// planKeys is the space-joined step keys of a plan.
func planKeys(v PlanView) string {
	var ks []string
	for _, s := range v.Job.Steps {
		ks = append(ks, s.Key)
	}
	return strings.Join(ks, " ")
}

// TestAnItemIsInAtMostOneUnfinishedJob: an item in a job that hasn't finished
// (planned, approved, running or paused) can't be planned again. Plan all
// leaves it out; naming it is refused with the job it's in. A job that
// finished, failed or was cancelled releases its items.
func TestAnItemIsInAtMostOneUnfinishedJob(t *testing.T) {
	r := newRig(t)
	decideTwo(t, r)
	r.attach(t, "s1")

	var first PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{"pr:schuettc/hail#3"}}, &first); c != http.StatusOK {
		t.Fatalf("plan the PR: %d", c)
	}
	// Approved through the store, so no lane moves it on: the test walks it
	// through approved, running and paused itself.
	if _, err := r.s.Apply.Approve(ctx, first.Job.ID, "s1"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	for _, st := range []apply.JobState{apply.JobApproved, apply.JobRunning, apply.JobPaused} {
		if st != apply.JobApproved {
			if err := r.s.Apply.SetJobState(ctx, first.Job.ID, st); err != nil {
				t.Fatalf("set %s: %v", st, err)
			}
		}
		// Plan all leaves the PR out.
		var all PlanView
		if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, &all); c != http.StatusOK {
			t.Fatalf("%s: plan all: %d", st, c)
		}
		if got := planKeys(all); got != "issue:schuettc/hail#4" {
			t.Fatalf("%s: plan all = %q, want only the issue (the PR is in job %d)", st, got, first.Job.ID)
		}
		if c := r.do(t, "POST", "/api/apply/cancel", map[string]any{"plan_id": all.Job.ID}, nil); c != http.StatusOK {
			t.Fatalf("discard: %d", c)
		}
		// Naming it is refused, and the refusal names the job.
		var out map[string]any
		if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{"pr:schuettc/hail#3", "issue:schuettc/hail#4"}}, &out); c != http.StatusConflict {
			t.Fatalf("%s: plan the PR again: %d %v, want 409", st, c, out)
		}
		msg, _ := out["error"].(string)
		want := "pr:schuettc/hail#3 is already in job #" + strconv.FormatInt(first.Job.ID, 10)
		if !strings.Contains(msg, want) {
			t.Fatalf("%s: refusal %q doesn't say %q", st, msg, want)
		}
	}

	// A plan holds its items too: named, it's "plan #N".
	var issuePlan PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{"issue:schuettc/hail#4"}}, &issuePlan); c != http.StatusOK {
		t.Fatalf("plan the issue: %d", c)
	}
	before, _ := r.s.Apply.List(ctx)
	var out map[string]any
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{"issue:schuettc/hail#4"}}, &out); c != http.StatusConflict {
		t.Fatalf("plan the issue twice: %d, want 409", c)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "issue:schuettc/hail#4 is already in plan #"+strconv.FormatInt(issuePlan.Job.ID, 10)) {
		t.Fatalf("refusal %q doesn't name the plan", msg)
	}
	if jobs, _ := r.s.Apply.List(ctx); len(jobs) != len(before) {
		t.Fatalf("a refusal made a job: %d jobs, want %d", len(jobs), len(before))
	}

	// Failed releases (the PR's job), cancelled releases (the issue's plan).
	if err := r.s.Apply.Finish(ctx, first.Job.ID, apply.JobFailed); err != nil {
		t.Fatal(err)
	}
	if c := r.do(t, "POST", "/api/apply/cancel", map[string]any{"plan_id": issuePlan.Job.ID}, nil); c != http.StatusOK {
		t.Fatalf("discard: %d", c)
	}
	var again PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{"pr:schuettc/hail#3", "issue:schuettc/hail#4"}}, &again); c != http.StatusOK {
		t.Fatalf("plan after failed and cancelled: %d", c)
	}
	// Done releases.
	if err := r.s.Apply.Cancel(ctx, again.Job.ID); err != nil {
		t.Fatal(err)
	}
	done, err := r.s.Apply.Create(ctx, apply.Plan{Steps: []apply.Step{{Key: "pr:schuettc/hail#3", Action: "pr-close", Lane: apply.LaneAgent}}}, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Apply.Approve(ctx, done.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Apply.SetJobState(ctx, done.ID, apply.JobRunning); err != nil {
		t.Fatal(err)
	}
	if err := r.s.Apply.Finish(ctx, done.ID, apply.JobDone); err != nil {
		t.Fatal(err)
	}
	var afterDone PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, &afterDone); c != http.StatusOK {
		t.Fatalf("plan all after done: %d", c)
	}
	if got := planKeys(afterDone); !strings.Contains(got, "pr:schuettc/hail#3") {
		t.Fatalf("plan all after the PR's job is done = %q, want the PR", got)
	}
}

// TestApproveRefusesAPlanWhoseItemsAreInAnotherJob: two plans can overlap
// before either is approved (built before serve kept items to one job, or
// straight through the store). Approving the first holds its items; the
// second is refused with a message naming the first and stays a plan.
func TestApproveRefusesAPlanWhoseItemsAreInAnotherJob(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	a, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": a.ID, "session": "s1"}, nil); c != http.StatusOK {
		t.Fatalf("approve the first: %d", c)
	}
	var out map[string]any
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": b.ID, "session": "s1"}, &out); c != http.StatusConflict {
		t.Fatalf("approve the overlapping second: %d %v, want 409", c, out)
	}
	msg, _ := out["error"].(string)
	if !strings.Contains(msg, "pr:schuettc/hail#3 is already in job #"+strconv.FormatInt(a.ID, 10)) {
		t.Fatalf("refusal %q doesn't name job %d", msg, a.ID)
	}
	j, _ := r.s.Apply.Get(ctx, b.ID)
	if j.State != apply.JobPlanned {
		t.Fatalf("refused plan is %s, want planned", j.State)
	}
	if cards, _ := r.s.Apply.NeedsYouFor(ctx, b.ID); len(cards) != 0 {
		t.Fatalf("a refused approve opened cards: %+v", cards)
	}
	// Once the first has finished, the second approves.
	if err := r.s.Apply.Finish(ctx, a.ID, apply.JobFailed); err != nil {
		t.Fatal(err)
	}
	if c := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": b.ID, "session": "s1"}, nil); c != http.StatusOK {
		t.Fatalf("approve after the first failed: %d", c)
	}
}

// TestConcurrentPlansAndApprovesKeepAnItemInOneJob: the overlap check and the
// create (or approve) are one step, so concurrent requests can't both pass.
// A test hook holds each request between its check and its write, which
// makes the race certain without the lock.
func TestConcurrentPlansAndApprovesKeepAnItemInOneJob(t *testing.T) {
	r := newRig(t)
	decideTwo(t, r)
	r.attach(t, "s1")
	r.s.afterOverlapCheck = func() { time.Sleep(30 * time.Millisecond) }

	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{"pr:schuettc/hail#3"}}, nil)
		}()
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("concurrent plan answered %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("%d concurrent plans of one item were made (codes %v), want 1", ok, codes)
	}

	// Two overlapping plans (through the store), approved at once: one wins.
	jobs, _ := r.s.Apply.List(ctx)
	for _, j := range jobs {
		_ = r.s.Apply.Cancel(ctx, j.ID)
	}
	var ids []int64
	for range n {
		j, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": id, "session": "s1"}, nil)
		}()
	}
	wg.Wait()
	ok = 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		} else if c != http.StatusConflict {
			t.Fatalf("concurrent approve answered %d", c)
		}
	}
	if ok != 1 {
		t.Fatalf("%d overlapping plans were approved at once (codes %v), want 1", ok, codes)
	}
}
