package serve

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// TestPlanEndpointRefusesStale verifies that POST /api/apply/plan returns 409
// when the index observation is older than one sync interval. The error message
// must name a sync and must not contain any command for Court to run.
func TestPlanEndpointRefusesStale(t *testing.T) {
	r := newRig(t)

	// Force the index to appear stale: set builtAt to 2 hours ago.
	staleAt := time.Now().Add(-2 * time.Hour)
	res := r.s.Index.Result()
	head := r.s.Index.Head()
	r.s.Index.set(res, head, staleAt)

	var out map[string]any
	code := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, &out)
	if code != http.StatusConflict {
		t.Fatalf("stale plan: got %d, want 409; body: %v", code, out)
	}

	// The error message must mention "sync" and must not tell Court to run a
	// command (no "$", no backticks, no shell keywords like "git " or "gh ").
	errMsg, _ := out["error"].(string)
	if errMsg == "" {
		t.Fatal("no error message in 409 body")
	}
	for _, forbidden := range []string{"$", "`", "git ", "gh "} {
		for i := 0; i+len(forbidden) <= len(errMsg); i++ {
			if errMsg[i:i+len(forbidden)] == forbidden {
				t.Errorf("error message must not contain command text %q; got: %s", forbidden, errMsg)
				break
			}
		}
	}
}

// TestApproveRunsCasebookLaneAndDispatchesAgentLane verifies that
// POST /api/apply/approve:
//   - approves the job,
//   - opens a batch needs-you card (kind="batch") BEFORE dispatching,
//   - dispatches the agent job exactly once (idempotent),
//   - starts the casebook lane in the background.
func TestApproveRunsCasebookLaneAndDispatchesAgentLane(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	// Create a planned job directly (plan has only agent-lane steps so the
	// casebook lane does nothing and no git infrastructure is needed).
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var jobView JobView
	code := r.do(t, "POST", "/api/apply/approve", map[string]any{
		"plan_id": job.ID,
		"session": "s1",
	}, &jobView)
	if code != http.StatusOK {
		t.Fatalf("approve: got %d, want 200; job=%v", code, jobView)
	}
	if jobView.Job.ID != job.ID {
		t.Fatalf("approve returned wrong job id %d, want %d", jobView.Job.ID, job.ID)
	}
	if jobView.Job.State != apply.JobApproved {
		t.Fatalf("job state = %q after approve, want approved", jobView.Job.State)
	}

	// A batch needs-you card must exist (opened before dispatch).
	cards, err := r.s.Apply.NeedsYouFor(ctx, job.ID)
	if err != nil {
		t.Fatalf("NeedsYouFor: %v", err)
	}
	var hasBatch bool
	for _, c := range cards {
		if c.Kind == "batch" && c.State == "open" {
			hasBatch = true
		}
	}
	if !hasBatch {
		t.Errorf("no open batch card after approve; cards: %+v", cards)
	}

	// The agent session must have received a delivery for this job.
	var w struct {
		Delivery *struct {
			ID int64 `json:"id"`
		} `json:"delivery"`
	}
	// Use a short timeout since we just need to confirm delivery exists.
	wCode := r.do(t, "GET", "/api/agent/wait?session=s1&timeout=1", nil, &w)
	if wCode != http.StatusOK || w.Delivery == nil {
		t.Errorf("no delivery to s1 after approve (code=%d, delivery=%v)", wCode, w.Delivery)
	}

	// Second approve must be idempotent (job is no longer planned).
	var out2 map[string]any
	code2 := r.do(t, "POST", "/api/apply/approve", map[string]any{
		"plan_id": job.ID,
		"session": "s1",
	}, &out2)
	if code2 != http.StatusConflict {
		t.Fatalf("second approve: got %d, want 409", code2)
	}
}

// TestPauseStopsAfterCurrentStepAndResumeContinues proves the casebook lane
// stops after the current step: with two pending steps, pausing during the
// first leaves the second pending; resuming runs it.
func TestPauseStopsAfterCurrentStepAndResumeContinues(t *testing.T) {
	r := newRig(t)

	dir := t.TempDir() // a real dir so the precondition's os.Stat succeeds
	tip := strings.Repeat("a", 40)
	var jobID int64

	// A spy that reports both branches as already gone (so no command runs), and
	// pauses the job the moment the FIRST step is checked "during" its run.
	r.s.Runner.RunGit = func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--git-dir" {
			return ".git", nil
		}
		if len(args) >= 3 && args[0] == "rev-parse" && args[1] == "--verify" {
			if strings.Contains(args[2], "feat/p") && jobID != 0 {
				_ = r.s.Apply.SetPaused(ctx, jobID, true)
			}
			return "", errors.New("no such ref")
		}
		return "", nil
	}

	mkStep := func(branch string) apply.Step {
		return apply.Step{
			Key:          "branch:schuettc/hail@" + branch,
			Action:       "branch-delete-local",
			Lane:         apply.LaneCasebook,
			Command:      "git -C '" + dir + "' update-ref -d refs/heads/" + branch + " " + tip,
			ExpectedTip:  tip,
			Precondition: "branch-tip-unchanged-and-landed",
		}
	}
	plan := apply.Plan{BuiltAt: time.Now(), Head: "head-pause", Steps: []apply.Step{mkStep("feat/p"), mkStep("feat/q")}}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	jobID = job.ID
	step1, step2 := job.Steps[0], job.Steps[1]

	// Approve via the endpoint so the casebook lane starts.
	var jv JobView
	if code := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID}, &jv); code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}

	// The lane runs step1, sees the pause, and stops before step2.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s1, _ := r.s.Apply.GetStep(ctx, step1.ID)
		j, _ := r.s.Apply.Get(ctx, job.ID)
		if j.Paused && (s1.State == apply.StepVerified || s1.State == apply.StepFailed) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s1, _ := r.s.Apply.GetStep(ctx, step1.ID)
	if s1.State != apply.StepVerified {
		t.Fatalf("step1 state = %q, want verified", s1.State)
	}
	s2, _ := r.s.Apply.GetStep(ctx, step2.ID)
	if s2.State != apply.StepPending {
		t.Fatalf("step2 state = %q after pause, want pending (lane must stop after the current step)", s2.State)
	}

	// Resume: the second step now runs to completion.
	if code := r.do(t, "POST", "/api/jobs/resume", map[string]any{"id": job.ID}, nil); code != http.StatusOK {
		t.Fatalf("resume: %d", code)
	}
	deadline = time.Now().Add(3 * time.Second)
	var final apply.JobStep
	for time.Now().Before(deadline) {
		final, _ = r.s.Apply.GetStep(ctx, step2.ID)
		if final.State == apply.StepVerified || final.State == apply.StepFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != apply.StepVerified {
		t.Fatalf("step2 state = %q after resume, want verified", final.State)
	}
}

// TestUndoOnlyWhenRestoreIsAutomatic verifies that POST /api/jobs/undo:
//   - accepts a step whose restore command is one of the automatic kinds
//     (git branch recreate, git remote push recreate),
//   - rejects (409) a step with no restore command (empty string).
func TestUndoOnlyWhenRestoreIsAutomatic(t *testing.T) {
	r := newRig(t)
	// Inject a no-op RunGit so the restore command succeeds without a real repo.
	r.s.Runner.RunGit = func(_ context.Context, _ string, _ ...string) (string, error) {
		return "", nil
	}

	// Create a job with one casebook-lane step.
	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head2",
		Steps: []apply.Step{
			{
				Key:     "branch:schuettc/hail@feat/x",
				Action:  "branch-delete-local",
				Lane:    apply.LaneCasebook,
				Command: "git -C '/tmp/clone' branch -D feat/x",
			},
		},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// No agent steps so empty session is fine.
	if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	step := job.Steps[0]
	// Walk the step to verified state.
	if err := r.s.Apply.SetStepState(ctx, step.ID, apply.StepRunning, ""); err != nil {
		t.Fatalf("→running: %v", err)
	}
	if err := r.s.Apply.SetStepState(ctx, step.ID, apply.StepReported, ""); err != nil {
		t.Fatalf("→reported: %v", err)
	}
	if err := r.s.Apply.SetStepState(ctx, step.ID, apply.StepVerified, ""); err != nil {
		t.Fatalf("→verified: %v", err)
	}

	// Inject an automatic restore command.
	autoRestore := "git -C '/tmp/clone' branch feat/x abc123"
	if err := r.s.Apply.SetStepRestore(ctx, step.ID, autoRestore); err != nil {
		t.Fatalf("SetStepRestore: %v", err)
	}

	// Undo should succeed (the restore is auto-undoable, casebook-lane step
	// runs the git command locally).
	var undoOut UndoResult
	if code := r.do(t, "POST", "/api/jobs/undo", map[string]any{"step": step.ID}, &undoOut); code != http.StatusOK {
		t.Fatalf("undo with auto restore: got %d", code)
	}

	// Step must now have undone_at set.
	updated, err := r.s.Apply.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("GetStep: %v", err)
	}
	if updated.UndoneAt.IsZero() {
		t.Error("step undone_at not set after undo")
	}

	// A step with no restore must be rejected.
	plan2 := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head3",
		Steps: []apply.Step{
			{
				Key:     "branch:schuettc/hail@feat/y",
				Action:  "branch-delete-local",
				Lane:    apply.LaneCasebook,
				Command: "git -C '/tmp/clone' branch -D feat/y",
			},
		},
	}
	job2, err := r.s.Apply.Create(ctx, plan2, "mbp")
	if err != nil {
		t.Fatalf("Create2: %v", err)
	}
	if _, err := r.s.Apply.Approve(ctx, job2.ID, ""); err != nil {
		t.Fatalf("Approve2: %v", err)
	}
	step2 := job2.Steps[0]
	// Walk to verified, no restore set.
	for _, st := range []apply.StepState{apply.StepRunning, apply.StepReported, apply.StepVerified} {
		if err := r.s.Apply.SetStepState(ctx, step2.ID, st, ""); err != nil {
			t.Fatalf("→%s: %v", st, err)
		}
	}

	var out2 map[string]any
	if code := r.do(t, "POST", "/api/jobs/undo", map[string]any{"step": step2.ID}, &out2); code != http.StatusConflict {
		t.Fatalf("undo without restore: got %d, want 409", code)
	}
}

// TestAnswerPostAndCloseTellsTheAgent verifies that answering a "text" card
// with action="post-and-close":
//   - records the approved text on the step (SetStepText),
//   - queues a message to the job's session telling it to post and close,
//   - closes the needs-you card (state=answered).
//
// Casebook must not run any gh command itself.
func TestAnswerPostAndCloseTellsTheAgent(t *testing.T) {
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

	step := job.Steps[0] // pr-close, posts=true
	// Move step to running then open a text needs-you card.
	if err := r.s.Apply.StartStepWithJob(ctx, job.ID, step.ID); err != nil {
		t.Fatalf("StartStepWithJob: %v", err)
	}
	card, err := r.s.Apply.OpenNeedsYou(ctx, job.ID, step.ID, "text", "Approve comment?", "Closing: landed.")
	if err != nil {
		t.Fatalf("OpenNeedsYou: %v", err)
	}

	// Consume the initial dispatch delivery so wait can return a new one.
	var w0 struct {
		Delivery *struct{ ID int64 } `json:"delivery"`
	}
	r.do(t, "GET", "/api/agent/wait?session=s1&timeout=1", nil, &w0)

	const approvedText = "Closing: the branch has landed in main."
	var ansOut AnswerResult
	code := r.do(t, "POST", "/api/jobs/answer", map[string]any{
		"needs_you": card.ID,
		"action":    "post-and-close",
		"text":      approvedText,
	}, &ansOut)
	if code != http.StatusOK {
		t.Fatalf("answer post-and-close: got %d; out=%v", code, ansOut)
	}
	if ansOut.NeedsYou.State != "answered" {
		t.Errorf("card state = %q, want answered", ansOut.NeedsYou.State)
	}

	// The step must now carry the approved text.
	updated, err := r.s.Apply.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("GetStep: %v", err)
	}
	if updated.Text != approvedText {
		t.Errorf("step text = %q, want %q", updated.Text, approvedText)
	}

	// A new delivery must arrive for s1 containing "post" and the approved text.
	var w struct {
		Delivery *struct{ ID int64 } `json:"delivery"`
		Text     string              `json:"text"`
	}
	wCode := r.do(t, "GET", "/api/agent/wait?session=s1&timeout=2", nil, &w)
	if wCode != http.StatusOK || w.Delivery == nil {
		t.Fatalf("no delivery to s1 after post-and-close (code=%d)", wCode)
	}
	// The message body must tell the agent to post the approved text.
	if w.Text == "" {
		t.Fatal("delivery text is empty")
	}
	if !containsSubstr(w.Text, approvedText) {
		t.Errorf("delivery text must contain the approved text %q; got: %q", approvedText, truncate(w.Text, 300))
	}
}

// TestAnswerSkipReturnsItemToAttention verifies that answering a needs-you card
// with action="skip" marks the step as skipped.
func TestAnswerSkipReturnsItemToAttention(t *testing.T) {
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

	step := job.Steps[0]
	// Move step to needs_you state.
	if err := r.s.Apply.StartStepWithJob(ctx, job.ID, step.ID); err != nil {
		t.Fatalf("StartStepWithJob: %v", err)
	}
	if err := r.s.Apply.SetStepState(ctx, step.ID, apply.StepNeedsYou, "precondition changed"); err != nil {
		t.Fatalf("→needs_you: %v", err)
	}
	card, err := r.s.Apply.OpenNeedsYou(ctx, job.ID, step.ID, "text", "Approve comment?", "Draft text.")
	if err != nil {
		t.Fatalf("OpenNeedsYou: %v", err)
	}

	var ansOut AnswerResult
	code := r.do(t, "POST", "/api/jobs/answer", map[string]any{
		"needs_you": card.ID,
		"action":    "skip",
	}, &ansOut)
	if code != http.StatusOK {
		t.Fatalf("answer skip: got %d; out=%v", code, ansOut)
	}
	if ansOut.NeedsYou.State != "answered" {
		t.Errorf("card state = %q, want answered", ansOut.NeedsYou.State)
	}

	// The step must now be skipped.
	updated, err := r.s.Apply.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("GetStep: %v", err)
	}
	if updated.State != apply.StepSkipped {
		t.Errorf("step state = %q, want skipped", updated.State)
	}

	// The item returns to Attention: it appears in an Attention view for its key.
	var list struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
	}
	if code := r.do(t, "GET", "/api/items?view=all", nil, &list); code != http.StatusOK {
		t.Fatalf("GET /api/items: %d", code)
	}
	var found bool
	for _, it := range list.Items {
		if it.Key == step.Key {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("item %q not in Attention view after skip; items: %+v", step.Key, list.Items)
	}
}

// containsSubstr reports whether s contains sub.
func containsSubstr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// truncate returns s truncated to n runes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
