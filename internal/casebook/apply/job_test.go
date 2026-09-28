package apply

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func testPlan() Plan {
	return Plan{
		BuiltAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		Head:    "abc123",
		Steps: []Step{
			{
				Key:          "branch:schuettc/hail@feat/thing",
				Action:       "branch-delete-local",
				Lane:         LaneCasebook,
				Command:      "git -C '/home/court/hail' branch -D 'feat/thing'",
				Precondition: "git -C '/home/court/hail' rev-parse --verify feat/thing",
				Posts:        false,
				ExpectedTip:  "deadbeef",
			},
			{
				Key:     "branch:schuettc/hail@feat/thing",
				Action:  "branch-delete-remote",
				Lane:    LaneCasebook,
				Command: "git -C '/home/court/hail' push origin --delete 'feat/thing'",
				Posts:   false,
			},
			{
				Key:     "pr:schuettc/hail#7",
				Action:  "pr-close",
				Lane:    LaneAgent,
				Command: "gh pr close 7 -R 'schuettc/hail' --comment 'Closing: decided to delete branch'",
				Posts:   true,
			},
		},
	}
}

func TestCreateJobPersistsPlanAndSteps(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	plan := testPlan()
	job, err := s.Create(ctx, plan, "macbook-pro")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Job should be in "planned" state.
	if job.State != JobPlanned {
		t.Errorf("job.State = %q; want %q", job.State, JobPlanned)
	}
	if job.ID == 0 {
		t.Error("job.ID should not be zero")
	}
	if job.CreatedAt.IsZero() {
		t.Error("job.CreatedAt should not be zero")
	}
	if len(job.Steps) != 3 {
		t.Fatalf("len(job.Steps) = %d; want 3", len(job.Steps))
	}

	// Verify steps are persisted correctly.
	step0 := job.Steps[0]
	if step0.Key != "branch:schuettc/hail@feat/thing" {
		t.Errorf("step0.Key = %q", step0.Key)
	}
	if step0.Action != "branch-delete-local" {
		t.Errorf("step0.Action = %q", step0.Action)
	}
	if step0.Lane != LaneCasebook {
		t.Errorf("step0.Lane = %q", step0.Lane)
	}
	if step0.State != StepPending {
		t.Errorf("step0.State = %q; want %q", step0.State, StepPending)
	}
	if step0.ExpectedTip != "deadbeef" {
		t.Errorf("step0.ExpectedTip = %q; want deadbeef", step0.ExpectedTip)
	}

	step2 := job.Steps[2]
	if step2.Lane != LaneAgent {
		t.Errorf("step2.Lane = %q; want agent", step2.Lane)
	}
	if !step2.Posts {
		t.Error("step2.Posts should be true")
	}

	// Get by ID should return the same job.
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != job.ID {
		t.Errorf("Get: ID mismatch")
	}
	if got.State != JobPlanned {
		t.Errorf("Get: State = %q", got.State)
	}
	if len(got.Steps) != 3 {
		t.Errorf("Get: len(Steps) = %d; want 3", len(got.Steps))
	}

	// Steps method should return the same steps.
	steps, err := s.Steps(ctx, job.ID)
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("Steps: len = %d; want 3", len(steps))
	}
	if steps[0].ExpectedTip != "deadbeef" {
		t.Errorf("Steps[0].ExpectedTip = %q; want deadbeef", steps[0].ExpectedTip)
	}
}

func TestStepStateTransitions(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	plan := testPlan()
	job, err := s.Create(ctx, plan, "macbook-pro")
	if err != nil {
		t.Fatal(err)
	}
	stepID := job.Steps[0].ID

	// pending → running: valid.
	if err := s.SetStepState(ctx, stepID, StepRunning, ""); err != nil {
		t.Fatalf("pending→running: %v", err)
	}

	// running → verified: invalid (must go through reported first).
	if err := s.SetStepState(ctx, stepID, StepVerified, ""); err == nil {
		t.Error("running→verified should fail")
	}

	// running → reported: valid.
	if err := s.SetStepState(ctx, stepID, StepReported, "done"); err != nil {
		t.Fatalf("running→reported: %v", err)
	}

	// reported → verified: valid.
	if err := s.SetStepState(ctx, stepID, StepVerified, ""); err != nil {
		t.Fatalf("reported→verified: %v", err)
	}

	// verified → pending: invalid (verified is terminal).
	if err := s.SetStepState(ctx, stepID, StepPending, ""); err == nil {
		t.Error("verified→pending should fail")
	}

	// verified → running: invalid (terminal).
	if err := s.SetStepState(ctx, stepID, StepRunning, ""); err == nil {
		t.Error("verified→running should fail")
	}

	// Test failed terminal.
	stepID2 := job.Steps[1].ID
	if err := s.SetStepState(ctx, stepID2, StepRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStepState(ctx, stepID2, StepFailed, "network error"); err != nil {
		t.Fatalf("running→failed: %v", err)
	}
	if err := s.SetStepState(ctx, stepID2, StepRunning, ""); err == nil {
		t.Error("failed→running should fail")
	}

	// Test needs_you → running.
	stepID3 := job.Steps[2].ID
	if err := s.SetStepState(ctx, stepID3, StepRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStepState(ctx, stepID3, StepNeedsYou, "need confirmation"); err != nil {
		t.Fatalf("running→needs_you: %v", err)
	}
	if err := s.SetStepState(ctx, stepID3, StepRunning, ""); err != nil {
		t.Fatalf("needs_you→running: %v", err)
	}
}

func TestListJobsReturnsStateAndSteps(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	// Create two jobs.
	plan1 := testPlan()
	job1, err := s.Create(ctx, plan1, "macbook-pro")
	if err != nil {
		t.Fatal(err)
	}

	plan2 := Plan{
		BuiltAt: time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC),
		Steps: []Step{
			{Key: "repo:schuettc/old", Action: "repo-archive", Lane: LaneAgent, Command: "gh repo archive 'schuettc/old' --yes"},
		},
	}
	_, err = s.Create(ctx, plan2, "macbook-pro")
	if err != nil {
		t.Fatal(err)
	}

	// Both jobs should appear in List.
	jobs, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("List: got %d jobs; want 2", len(jobs))
	}

	// Advance job1 to approved via SetJobState.
	if err := s.SetJobState(ctx, job1.ID, JobApproved); err != nil {
		t.Fatalf("planned→approved: %v", err)
	}

	// Verify state is reflected in List.
	jobs, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, j := range jobs {
		if j.ID == job1.ID {
			if j.State != JobApproved {
				t.Errorf("job1 state = %q; want approved", j.State)
			}
			if len(j.Steps) != 3 {
				t.Errorf("job1 steps = %d; want 3", len(j.Steps))
			}
			found = true
		}
	}
	if !found {
		t.Error("job1 not found in List")
	}

	// Invalid job-state transition: approved → done (must go through running).
	if err := s.SetJobState(ctx, job1.ID, JobDone); err == nil {
		t.Error("approved→done should fail")
	}

	// approved → running: valid.
	if err := s.SetJobState(ctx, job1.ID, JobRunning); err != nil {
		t.Fatalf("approved→running: %v", err)
	}

	// running → done: valid.
	if err := s.SetJobState(ctx, job1.ID, JobDone); err != nil {
		t.Fatalf("running→done: %v", err)
	}

	// done → running: invalid (terminal).
	if err := s.SetJobState(ctx, job1.ID, JobRunning); err == nil {
		t.Error("done→running should fail")
	}

	// NextRunnable: create a fresh job and advance steps.
	plan3 := testPlan()
	job3, err := s.Create(ctx, plan3, "macbook-pro")
	if err != nil {
		t.Fatal(err)
	}
	// All steps are pending; NextRunnable for casebook lane should return steps[0].
	next, ok, err := s.NextRunnable(ctx, job3.ID, LaneCasebook)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("NextRunnable: expected to find a runnable step")
	}
	if next.Action != "branch-delete-local" {
		t.Errorf("NextRunnable: action = %q; want branch-delete-local", next.Action)
	}

	// NextRunnable for agent lane with all steps pending.
	next, ok, err = s.NextRunnable(ctx, job3.ID, LaneAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("NextRunnable agent: expected to find a runnable step")
	}
	if next.Action != "pr-close" {
		t.Errorf("NextRunnable agent: action = %q; want pr-close", next.Action)
	}
}

// TestJobMachineAndSession verifies the Machine field is persisted by Create
// and that the Job struct carries Machine, Session, Paused, and FinishedAt.
func TestJobMachineAndSession(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	plan := testPlan()
	job, err := s.Create(ctx, plan, "my-machine")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.Machine != "my-machine" {
		t.Errorf("job.Machine = %q; want my-machine", job.Machine)
	}
	if job.Paused {
		t.Error("new job should not be paused")
	}
	if !job.FinishedAt.IsZero() {
		t.Error("new job FinishedAt should be zero")
	}
	if job.Session != "" {
		t.Error("new job Session should be empty")
	}

	// Verify Get round-trips Machine.
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Machine != "my-machine" {
		t.Errorf("Get Machine = %q; want my-machine", got.Machine)
	}
}

// TestApproveJob verifies the Approve method: planned → approved, session set.
func TestApproveJob(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	// Plan with only casebook steps: session="" is allowed.
	csPlan := Plan{
		Steps: []Step{
			{Key: "k1", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git branch -D foo"},
		},
	}
	job, err := s.Create(ctx, csPlan, "mac")
	if err != nil {
		t.Fatal(err)
	}

	approved, err := s.Approve(ctx, job.ID, "")
	if err != nil {
		t.Fatalf("Approve(casebook-only, session=''): %v", err)
	}
	if approved.State != JobApproved {
		t.Errorf("approved.State = %q; want approved", approved.State)
	}
	if approved.ApprovedAt.IsZero() {
		t.Error("ApprovedAt should be set")
	}

	// Approving again (not planned) should error.
	if _, err := s.Approve(ctx, job.ID, ""); err == nil {
		t.Error("second Approve should fail")
	}

	// Plan with agent step: session="" should fail.
	agentJob, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, agentJob.ID, ""); err == nil {
		t.Error("Approve with agent steps and empty session should fail")
	}

	// session provided: should succeed.
	approved2, err := s.Approve(ctx, agentJob.ID, "sess-abc")
	if err != nil {
		t.Fatalf("Approve with session: %v", err)
	}
	if approved2.Session != "sess-abc" {
		t.Errorf("Session = %q; want sess-abc", approved2.Session)
	}
}

// TestSetPausedAndNextRunnableWhilePaused verifies pause/resume and that
// NextRunnable returns nothing while the job is paused.
func TestSetPausedAndNextRunnableWhilePaused(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}

	// SetPaused true.
	if err := s.SetPaused(ctx, job.ID, true); err != nil {
		t.Fatalf("SetPaused true: %v", err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Paused {
		t.Error("job should be paused after SetPaused(true)")
	}

	// NextRunnable returns nothing while paused.
	_, ok, err := s.NextRunnable(ctx, job.ID, LaneCasebook)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("NextRunnable should return nothing while paused")
	}

	// Resume.
	if err := s.SetPaused(ctx, job.ID, false); err != nil {
		t.Fatalf("SetPaused false: %v", err)
	}
	_, ok, err = s.NextRunnable(ctx, job.ID, LaneCasebook)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("NextRunnable should return a step after resume")
	}
}

// TestFinishJob verifies Finish sets the terminal state and FinishedAt.
func TestFinishJob(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job.ID, "sess-x"); err != nil {
		t.Fatal(err)
	}

	if err := s.Finish(ctx, job.ID, JobDone); err != nil {
		t.Fatalf("Finish done: %v", err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobDone {
		t.Errorf("state = %q; want done", got.State)
	}
	if got.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Finish")
	}

	// Finishing a done job should fail.
	if err := s.Finish(ctx, job.ID, JobFailed); err == nil {
		t.Error("Finish on done job should fail")
	}

	// Finish with invalid state (not done/failed) should error.
	job2, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job2.ID, "sess-x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, job2.ID, JobRunning); err == nil {
		t.Error("Finish with running state should fail")
	}
}

// TestSetStepTextAndRestore verifies SetStepText and SetStepRestore persist
// the text and restore fields on a step.
func TestSetStepTextAndRestore(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	stepID := job.Steps[2].ID // agent step that posts

	if err := s.SetStepText(ctx, stepID, "Closing PR #7"); err != nil {
		t.Fatalf("SetStepText: %v", err)
	}
	if err := s.SetStepRestore(ctx, stepID, "gh pr reopen 7"); err != nil {
		t.Fatalf("SetStepRestore: %v", err)
	}

	steps, err := s.Steps(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found JobStep
	for _, st := range steps {
		if st.ID == stepID {
			found = st
		}
	}
	if found.Text != "Closing PR #7" {
		t.Errorf("Text = %q; want 'Closing PR #7'", found.Text)
	}
	if found.Restore != "gh pr reopen 7" {
		t.Errorf("Restore = %q; want 'gh pr reopen 7'", found.Restore)
	}
}

// TestNeedsYou verifies the full needs-you lifecycle.
func TestNeedsYou(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	stepID := job.Steps[0].ID

	// OpenNeedsYou with a step.
	card, err := s.OpenNeedsYou(ctx, job.ID, stepID, "text", "Confirm?", "Please confirm the deletion.")
	if err != nil {
		t.Fatalf("OpenNeedsYou: %v", err)
	}
	if card.State != "open" {
		t.Errorf("card.State = %q; want open", card.State)
	}
	if card.JobID != job.ID {
		t.Errorf("card.JobID = %d", card.JobID)
	}
	if card.StepID != stepID {
		t.Errorf("card.StepID = %d; want %d", card.StepID, stepID)
	}
	if card.Kind != "text" {
		t.Errorf("card.Kind = %q; want text", card.Kind)
	}
	if card.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	// OpenNeedsYou with no step (stepID=0).
	card2, err := s.OpenNeedsYou(ctx, job.ID, 0, "batch", "Approve batch?", "")
	if err != nil {
		t.Fatalf("OpenNeedsYou(stepID=0): %v", err)
	}
	if card2.StepID != 0 {
		t.Errorf("card2.StepID = %d; want 0", card2.StepID)
	}

	// NeedsYouFor returns both cards.
	cards, err := s.NeedsYouFor(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("NeedsYouFor: got %d; want 2", len(cards))
	}

	// OpenNeedsYouAll returns all open cards across all jobs.
	all, err := s.OpenNeedsYouAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("OpenNeedsYouAll: got %d; want 2", len(all))
	}

	// EditNeedsYouText updates text while open.
	if err := s.EditNeedsYouText(ctx, card.ID, "Updated question text."); err != nil {
		t.Fatalf("EditNeedsYouText: %v", err)
	}

	// AnswerNeedsYou transitions open → answered.
	answered, err := s.AnswerNeedsYou(ctx, card.ID, "yes")
	if err != nil {
		t.Fatalf("AnswerNeedsYou: %v", err)
	}
	if answered.State != "answered" {
		t.Errorf("answered.State = %q; want answered", answered.State)
	}
	if answered.Answer != "yes" {
		t.Errorf("answered.Answer = %q; want yes", answered.Answer)
	}
	if answered.AnsweredAt.IsZero() {
		t.Error("AnsweredAt should be set")
	}

	// Answering twice should fail.
	if _, err := s.AnswerNeedsYou(ctx, card.ID, "no"); err == nil {
		t.Error("second AnswerNeedsYou should fail")
	}

	// EditNeedsYouText on answered card should fail.
	if err := s.EditNeedsYouText(ctx, card.ID, "late edit"); err == nil {
		t.Error("EditNeedsYouText on answered card should fail")
	}

	// After answering card, OpenNeedsYouAll should return 1 (card2 still open).
	all, err = s.OpenNeedsYouAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("OpenNeedsYouAll after answer: got %d; want 1", len(all))
	}
}

// TestRestartRoundTrip creates a job, approves it, advances some states,
// opens a needs-you card, then closes and reopens the database and verifies
// everything reads back identically.
func TestRestartRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "casebook.db")

	ctx := context.Background()

	var (
		jobID  int64
		stepID int64
		cardID int64
	)

	// Phase 1: populate.
	{
		d, err := db.Open(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		s := NewStore(d)
		job, err := s.Create(ctx, testPlan(), "restart-machine")
		if err != nil {
			t.Fatal(err)
		}
		jobID = job.ID
		stepID = job.Steps[0].ID

		if _, err := s.Approve(ctx, job.ID, "sess-restart"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetStepState(ctx, stepID, StepRunning, ""); err != nil {
			t.Fatal(err)
		}
		card, err := s.OpenNeedsYou(ctx, job.ID, stepID, "text", "Please confirm", "some details")
		if err != nil {
			t.Fatal(err)
		}
		cardID = card.ID

		d.Close()
	}

	// Verify the file exists.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file missing: %v", err)
	}

	// Phase 2: reopen and verify.
	{
		d, err := db.Open(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		s := NewStore(d)

		job, err := s.Get(ctx, jobID)
		if err != nil {
			t.Fatalf("Get after restart: %v", err)
		}
		if job.Machine != "restart-machine" {
			t.Errorf("Machine = %q; want restart-machine", job.Machine)
		}
		if job.State != JobApproved {
			t.Errorf("State = %q; want approved", job.State)
		}
		if job.Session != "sess-restart" {
			t.Errorf("Session = %q; want sess-restart", job.Session)
		}
		if job.ApprovedAt.IsZero() {
			t.Error("ApprovedAt should be set")
		}
		if len(job.Steps) != 3 {
			t.Fatalf("Steps len = %d; want 3", len(job.Steps))
		}

		// Step 0 should be running.
		var st0 JobStep
		for _, st := range job.Steps {
			if st.ID == stepID {
				st0 = st
			}
		}
		if st0.State != StepRunning {
			t.Errorf("step0.State = %q; want running", st0.State)
		}

		// Needs-you card should still be open.
		cards, err := s.NeedsYouFor(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if len(cards) != 1 {
			t.Fatalf("NeedsYouFor after restart: got %d; want 1", len(cards))
		}
		if cards[0].ID != cardID {
			t.Errorf("card.ID = %d; want %d", cards[0].ID, cardID)
		}
		if cards[0].State != "open" {
			t.Errorf("card.State = %q; want open", cards[0].State)
		}
		if cards[0].Text != "some details" {
			t.Errorf("card.Text = %q; want 'some details'", cards[0].Text)
		}
	}
}
