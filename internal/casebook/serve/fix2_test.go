package serve

import (
	"net/http"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// ── Final fix round: Issue 1 – jobs reach terminal state via serve APIs ───────

// TestCasebookOnlyJobEndsDoneAfterLaneDrains verifies that a casebook-only job
// transitions to done when its lane drains (via the goroutine in
// startCasebookLane → settleJob).
func TestCasebookOnlyJobEndsDoneAfterLaneDrains(t *testing.T) {
	r := newRig(t)

	// A job whose casebook step is already done (branch already gone = Done).
	// We advance the step manually to verified to simulate a drained lane.
	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-settle",
		Steps: []apply.Step{{
			Key:    "k1",
			Action: "branch-delete-local",
			Lane:   apply.LaneCasebook,
			// command that parses but target is never real: step will be skipped
			Command: "git -C '/tmp/nonexistent-for-test' update-ref -d refs/heads/feat/x abc123",
		}},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := r.s.Apply.SetJobState(ctx, job.ID, apply.JobRunning); err != nil {
		t.Fatalf("SetJobState running: %v", err)
	}
	// Mark step verified manually (simulating lane completion).
	step := job.Steps[0]
	_ = r.s.Apply.SetStepState(ctx, step.ID, apply.StepRunning, "")
	_ = r.s.Apply.SetStepState(ctx, step.ID, apply.StepReported, "")
	_ = r.s.Apply.SetStepState(ctx, step.ID, apply.StepVerified, "")

	// Settle should now mark the job done.
	newState, changed, err := r.s.Apply.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !changed || newState != apply.JobDone {
		t.Errorf("Settle = (%q, %v); want (done, true)", newState, changed)
	}
}

// TestAgentOnlyJobEndsDoneAfterLastReport verifies that an agent-only job ends
// done after its last step is verified via POST /api/agent/job-step.
func TestAgentOnlyJobEndsDoneAfterLastReport(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	// Create a single-step agent job.
	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-agent-only",
		Steps: []apply.Step{{
			Key:     "pr:schuettc/hail#99",
			Action:  "pr-close",
			Lane:    apply.LaneAgent,
			Command: "gh pr close 99 -R schuettc/hail",
			// no precondition: simplest case
		}},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	step := job.Steps[0]

	// started
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": step.ID, "state": "started",
	}, nil); code != http.StatusOK {
		t.Fatalf("started: %d", code)
	}
	// reported → observation will verify (FakeGh returns CLOSED for pr-close)
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": step.ID, "state": "reported",
	}, nil); code != http.StatusOK {
		t.Fatalf("reported: %d", code)
	}

	// After reported, the step is verified by observation, then Settle runs.
	got, err := r.s.Apply.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != apply.JobDone {
		t.Errorf("job state = %q after last step verified; want done", got.State)
	}
}

// TestMixedJobStaysPendingUntilBothLanesDone verifies that a mixed
// (casebook+agent) job does not complete until both lanes finish.
func TestMixedJobStaysPendingUntilBothLanesDone(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-mixed",
		Steps: []apply.Step{
			{
				Key:     "k1",
				Action:  "branch-delete-local",
				Lane:    apply.LaneCasebook,
				Command: "git -C '/tmp/never' update-ref -d refs/heads/x abc",
			},
			{
				Key:     "pr:schuettc/hail#5",
				Action:  "pr-close",
				Lane:    apply.LaneAgent,
				Command: "gh pr close 5 -R schuettc/hail",
			},
		},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := r.s.Apply.SetJobState(ctx, job.ID, apply.JobRunning); err != nil {
		t.Fatalf("SetJobState: %v", err)
	}

	csStep := job.Steps[0]
	agentStep := job.Steps[1]

	// Finish only the casebook step.
	_ = r.s.Apply.SetStepState(ctx, csStep.ID, apply.StepRunning, "")
	_ = r.s.Apply.SetStepState(ctx, csStep.ID, apply.StepReported, "")
	_ = r.s.Apply.SetStepState(ctx, csStep.ID, apply.StepVerified, "")

	// Settle should not complete yet.
	state, changed, err := r.s.Apply.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle after CS only: %v", err)
	}
	if changed {
		t.Errorf("job should not settle while agent step is still pending; got state=%q", state)
	}

	// Now finish the agent step via the API.
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": agentStep.ID, "state": "started",
	}, nil); code != http.StatusOK {
		t.Fatalf("started agent step: %d", code)
	}
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": agentStep.ID, "state": "reported",
	}, nil); code != http.StatusOK {
		t.Fatalf("reported agent step: %d", code)
	}

	// Now both are done, job should be done.
	got, err := r.s.Apply.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != apply.JobDone {
		t.Errorf("mixed job state = %q after both lanes done; want done", got.State)
	}
}

// TestFailedStepWithNoOpenCardEndsFailed verifies that answering a failed card
// with skip (closing the card) allows the job to transition to failed.
func TestFailedStepWithNoOpenCardEndsFailed(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-failed",
		Steps: []apply.Step{{
			Key:     "pr:schuettc/hail#11",
			Action:  "pr-close",
			Lane:    apply.LaneAgent,
			Command: "gh pr close 11 -R schuettc/hail",
		}},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	step := job.Steps[0]

	// started
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": step.ID, "state": "started",
	}, nil); code != http.StatusOK {
		t.Fatalf("started: %d", code)
	}
	// failed → opens a needs-you card
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": step.ID, "state": "failed",
		"detail": "network error",
	}, nil); code != http.StatusOK {
		t.Fatalf("failed: %d", code)
	}

	// With the open card, job should stay running.
	got, err := r.s.Apply.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State == apply.JobFailed {
		t.Error("job should not be failed while needs-you card is open")
	}

	// Load the open card and skip it (close it).
	cards, err := r.s.Apply.OpenNeedsYouAll(ctx)
	if err != nil {
		t.Fatalf("OpenNeedsYouAll: %v", err)
	}
	var card apply.NeedsYou
	for _, c := range cards {
		if c.JobID == job.ID {
			card = c
			break
		}
	}
	if card.ID == 0 {
		t.Fatal("no open needs-you card for the failed job")
	}

	// Answer with skip → closes the card.
	if code := r.do(t, "POST", "/api/jobs/answer", map[string]any{
		"needs_you": card.ID, "action": "skip",
	}, nil); code != http.StatusOK {
		t.Fatalf("answer skip: %d", code)
	}

	// Now the job should be failed.
	got, err = r.s.Apply.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != apply.JobFailed {
		t.Errorf("job state = %q after card closed; want failed", got.State)
	}
}

// TestOpenNeedsYouCardKeepsJobRunning verifies that a job with an open
// needs-you card does not settle even if all steps are otherwise blocked.
func TestOpenNeedsYouCardKeepsJobRunning(t *testing.T) {
	r := newRig(t)

	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-card-keeps-running",
		Steps: []apply.Step{{
			Key:     "k1",
			Action:  "branch-delete-local",
			Lane:    apply.LaneCasebook,
			Command: "git -C '/tmp/test' update-ref -d refs/heads/x abc",
		}},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := r.s.Apply.SetJobState(ctx, job.ID, apply.JobRunning); err != nil {
		t.Fatalf("SetJobState: %v", err)
	}
	step := job.Steps[0]
	_ = r.s.Apply.SetStepState(ctx, step.ID, apply.StepRunning, "")
	// Fail the step and open a card atomically.
	_, err = r.s.Apply.PauseStepWithCard(ctx, step.ID, apply.StepFailed, "failed", "precondition failed")
	if err != nil {
		t.Fatalf("PauseStepWithCard: %v", err)
	}

	// Settle should NOT change the state while the card is open.
	state, changed, err := r.s.Apply.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if changed {
		t.Errorf("Settle changed state to %q, but open card should prevent that", state)
	}
}
