package serve

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// agentTestPlan builds a minimal plan with one agent-lane step (pr-close,
// posts=true) and one agent-lane step (repo-archive, no public text).
func agentTestPlan() apply.Plan {
	return apply.Plan{
		BuiltAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		Head:    "abc123",
		Steps: []apply.Step{
			{
				Key:          "pr:schuettc/hail#3",
				Action:       "pr-close",
				Lane:         apply.LaneAgent,
				Command:      "gh pr close 3 -R schuettc/hail --comment 'Closing.'",
				Precondition: "pr-no-new-activity",
				Posts:        true,
			},
			{
				Key:          "repo:schuettc/hail",
				Action:       "repo-archive",
				Lane:         apply.LaneAgent,
				Command:      "gh repo archive schuettc/hail",
				Precondition: "repo-no-open-human-prs",
			},
		},
	}
}

// TestAgentJobArrivesAsADelivery verifies that dispatchAgentJob enqueues a
// delivery to the session carrying the job's agent-lane steps and
// Attached.Job = "<id>".
func TestAgentJobArrivesAsADelivery(t *testing.T) {
	r := newRig(t)

	// Register session s1.
	r.attach(t, "s1")

	// Create and approve a job with agent-lane steps.
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Dispatch the job to the session.
	if err := r.s.dispatchAgentJob(ctx, job, "s1"); err != nil {
		t.Fatalf("dispatchAgentJob: %v", err)
	}

	// The session should now have a delivery waiting.
	var w struct {
		Delivery *struct {
			ID       int64 `json:"id"`
			Messages []struct {
				ID       int64 `json:"id"`
				Attached struct {
					Job string `json:"job"`
				} `json:"attached"`
			} `json:"messages"`
		} `json:"delivery"`
		Text string `json:"text"`
	}
	if code := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); code != http.StatusOK {
		t.Fatalf("wait: %d", code)
	}
	if w.Delivery == nil || w.Delivery.ID == 0 {
		t.Fatal("no delivery")
	}
	if len(w.Delivery.Messages) == 0 {
		t.Fatal("delivery has no messages")
	}
	// Attached.Job must carry the job id.
	if got := w.Delivery.Messages[0].Attached.Job; got == "" {
		t.Fatal("Attached.Job is empty in the delivery message")
	}
	// The message body must mention the step commands.
	if w.Text == "" {
		t.Fatal("delivery text is empty")
	}
	if !containsAny(w.Text, "pr-close", "repo-archive", "pr close", "repo archive") {
		t.Fatalf("delivery text does not mention agent steps: %q", w.Text[:min(len(w.Text), 200)])
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestJobStepReportsState verifies that POST /api/agent/job-step transitions
// a step through started → reported, and that reported triggers gh observation
// and verification.
func TestJobStepReportsState(t *testing.T) {
	r := newRig(t)

	// Register session s1.
	r.attach(t, "s1")

	// Create and approve a job.
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	step := job.Steps[0] // pr-close step

	// Started: pending → running.
	var startedOut map[string]any
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1",
		"job":     job.ID,
		"step":    step.ID,
		"state":   "started",
	}, &startedOut); code != http.StatusOK {
		t.Fatalf("started: %d %v", code, startedOut)
	}

	// Verify step is now running.
	refreshed, err := r.s.Apply.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var runningStep apply.JobStep
	for _, s := range refreshed.Steps {
		if s.ID == step.ID {
			runningStep = s
		}
	}
	if runningStep.State != apply.StepRunning {
		t.Fatalf("step state = %q after started, want running", runningStep.State)
	}

	// Reported: running → reported → (observation) → verified or failed.
	var reportedOut map[string]any
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1",
		"job":     job.ID,
		"step":    step.ID,
		"state":   "reported",
	}, &reportedOut); code != http.StatusOK {
		t.Fatalf("reported: %d %v", code, reportedOut)
	}

	// Step must be verified — the fake Gh returns CLOSED for pr views and
	// Verify(pr-close, CLOSED) → StepVerified.
	refreshed2, _ := r.s.Apply.Get(ctx, job.ID)
	var finalStep apply.JobStep
	for _, s := range refreshed2.Steps {
		if s.ID == step.ID {
			finalStep = s
		}
	}
	if finalStep.State != apply.StepVerified {
		t.Fatalf("step state = %q after reported, want %q (FakeGh returns CLOSED)",
			finalStep.State, apply.StepVerified)
	}
}

// TestJobAskCreatesNeedsYouCard verifies that POST /api/agent/job-ask opens a
// needs-you card of kind "text" (for a draft comment).
func TestJobAskCreatesNeedsYouCard(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	// Create, approve and start a job.
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	step := job.Steps[0]

	// Move step to running first.
	r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": step.ID, "state": "started",
	}, nil)

	// Draft a comment: job_ask opens a needs-you card of kind "text".
	var out map[string]any
	if code := r.do(t, "POST", "/api/agent/job-ask", map[string]any{
		"session":  "s1",
		"job":      job.ID,
		"step":     step.ID,
		"question": "Please approve this comment",
		"text":     "Closing: this branch has landed.",
	}, &out); code != http.StatusOK {
		t.Fatalf("job-ask: %d %v", code, out)
	}

	// A needs-you card must exist for the job.
	cards, err := r.s.Apply.NeedsYouFor(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) == 0 {
		t.Fatal("no needs-you cards created by job-ask")
	}
	var found bool
	for _, c := range cards {
		if c.Kind == "text" && c.StepID == step.ID && c.State == "open" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no open text card found; cards: %+v", cards)
	}
}

// TestPostsStepWaitsForCourtsAnswer verifies that casebook_job_ask opens a
// needs-you card (kind "text") and no additional delivery automatically tells
// the agent to post until the answer endpoint (Task 12) runs.
func TestPostsStepWaitsForCourtsAnswer(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	// Dispatch the initial job delivery so the agent has its instructions.
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := r.s.dispatchAgentJob(ctx, job, "s1"); err != nil {
		t.Fatalf("dispatchAgentJob: %v", err)
	}

	// Agent claims the delivery.
	var w struct {
		Delivery *struct{ ID int64 } `json:"delivery"`
	}
	r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w)
	if w.Delivery == nil {
		t.Fatal("no initial delivery")
	}

	// Agent settles the delivery and then moves step to running.
	step := job.Steps[0]
	r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1", "job": job.ID, "step": step.ID, "state": "started",
	}, nil)

	// Agent drafts comment text via job-ask.
	var askOut map[string]any
	if code := r.do(t, "POST", "/api/agent/job-ask", map[string]any{
		"session":  "s1",
		"job":      job.ID,
		"step":     step.ID,
		"question": "Approve this comment?",
		"text":     "Closing: the branch has landed.",
	}, &askOut); code != http.StatusOK {
		t.Fatalf("job-ask: %d %v", code, askOut)
	}

	// A needs-you card of kind "text" must be open.
	cards, _ := r.s.Apply.NeedsYouFor(ctx, job.ID)
	var hasTextCard bool
	for _, c := range cards {
		if c.Kind == "text" && c.State == "open" {
			hasTextCard = true
		}
	}
	if !hasTextCard {
		t.Fatal("expected an open text needs-you card after job_ask")
	}

	// No new delivery must arrive for the agent yet (the answer hasn't come).
	// With a short timeout the wait returns 204.
	b, _ := json.Marshal(nil)
	_ = b
	var w2 struct {
		Delivery *struct{ ID int64 } `json:"delivery"`
	}
	code := r.do(t, "GET", "/api/agent/wait?session=s1&timeout=1", nil, &w2)
	if code != http.StatusNoContent {
		t.Fatalf("expected 204 (no delivery) while waiting for Court's answer, got %d", code)
	}
}

// TestJobStepWrongSession verifies that a session that does not own the job
// receives a 403 error.
func TestJobStepWrongSession(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.attach(t, "s2")

	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Approve for s1, not s2.
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	step := job.Steps[0]

	// s2 tries to report a step: must get 403.
	var out map[string]any
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s2",
		"job":     job.ID,
		"step":    step.ID,
		"state":   "started",
	}, &out); code != http.StatusForbidden {
		t.Fatalf("wrong session: got %d, want 403", code)
	}
}

// TestJobAskWrongSession verifies that a session that does not own the job
// receives a 403 from POST /api/agent/job-ask.
func TestJobAskWrongSession(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.attach(t, "s2")

	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	var out map[string]any
	if code := r.do(t, "POST", "/api/agent/job-ask", map[string]any{
		"session":  "s2",
		"job":      job.ID,
		"step":     job.Steps[0].ID,
		"question": "ok?",
		"text":     "text",
	}, &out); code != http.StatusForbidden {
		t.Fatalf("wrong session: got %d, want 403", code)
	}
}

// TestStartedMovesApprovedJobToRunning verifies that the first "started" call
// atomically moves an approved job to running (fix 4).
func TestStartedMovesApprovedJobToRunning(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if job.State != apply.JobApproved {
		t.Fatalf("job state after approve = %q, want approved", job.State)
	}

	var out map[string]any
	if code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1",
		"job":     job.ID,
		"step":    job.Steps[0].ID,
		"state":   "started",
	}, &out); code != http.StatusOK {
		t.Fatalf("started: %d %v", code, out)
	}

	refreshed, err := r.s.Apply.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.State != apply.JobRunning {
		t.Fatalf("job state = %q after first started, want running", refreshed.State)
	}
}

// TestJobStepRefuses409ForInvalidJobState verifies that job-step returns 409
// when the job is in a terminal state (fix 4).
func TestJobStepRefuses409ForInvalidJobState(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := r.s.Apply.Finish(ctx, job.ID, apply.JobFailed); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	var out map[string]any
	code := r.do(t, "POST", "/api/agent/job-step", map[string]any{
		"session": "s1",
		"job":     job.ID,
		"step":    job.Steps[0].ID,
		"state":   "started",
	}, &out)
	if code != http.StatusConflict {
		t.Fatalf("job-step on failed job: got %d %v, want 409", code, out)
	}
}

// TestJobAskRefuses409ForInvalidJobState verifies that job-ask returns 409
// when the job is in a terminal state (fix 4).
func TestJobAskRefuses409ForInvalidJobState(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	job, err = r.s.Apply.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := r.s.Apply.Finish(ctx, job.ID, apply.JobDone); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	var out map[string]any
	code := r.do(t, "POST", "/api/agent/job-ask", map[string]any{
		"session":  "s1",
		"job":      job.ID,
		"step":     job.Steps[0].ID,
		"question": "ok?",
		"text":     "text",
	}, &out)
	if code != http.StatusConflict {
		t.Fatalf("job-ask on done job: got %d %v, want 409", code, out)
	}
}
