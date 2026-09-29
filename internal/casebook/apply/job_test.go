package apply

import (
	"context"
	"errors"
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
	t.Cleanup(func() { _ = d.Close() })
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

	// Advance job1 to approved via Approve (planned→approved must use Approve, not SetJobState).
	// job1 uses testPlan() which has agent-lane steps, so a session is required.
	if _, err := s.Approve(ctx, job1.ID, "sess-list"); err != nil {
		t.Fatalf("planned→approved via Approve: %v", err)
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

	// ClaimNext: create a fresh job, approve it, then claim steps.
	plan3 := testPlan()
	job3, err := s.Create(ctx, plan3, "macbook-pro")
	if err != nil {
		t.Fatal(err)
	}
	// Approve job3 so ClaimNext can proceed (testPlan has agent steps).
	if _, err := s.Approve(ctx, job3.ID, "sess-3"); err != nil {
		t.Fatalf("Approve job3: %v", err)
	}
	// ClaimNext for casebook lane should atomically claim and return steps[0].
	next, ok, err := s.ClaimNext(ctx, job3.ID, LaneCasebook)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ClaimNext: expected to find a runnable step")
	}
	if next.Action != "branch-delete-local" {
		t.Errorf("ClaimNext: action = %q; want branch-delete-local", next.Action)
	}
	if next.State != StepRunning {
		t.Errorf("ClaimNext: step.State = %q; want running", next.State)
	}

	// ClaimNext for agent lane.
	next, ok, err = s.ClaimNext(ctx, job3.ID, LaneAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ClaimNext agent: expected to find a runnable step")
	}
	if next.Action != "pr-close" {
		t.Errorf("ClaimNext agent: action = %q; want pr-close", next.Action)
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

// TestSetPausedAndClaimNextWhilePaused verifies pause/resume and that
// ClaimNext returns nothing while the job is paused.
func TestSetPausedAndClaimNextWhilePaused(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	// Use a casebook-only plan so Approve doesn't require a session.
	csPlan := Plan{
		Steps: []Step{
			{Key: "k1", Action: "step-x", Lane: LaneCasebook, Command: "echo x"},
			{Key: "k2", Action: "step-y", Lane: LaneCasebook, Command: "echo y"},
		},
	}
	job, err := s.Create(ctx, csPlan, "mac")
	if err != nil {
		t.Fatal(err)
	}
	// Approve so ClaimNext can operate.
	if _, err := s.Approve(ctx, job.ID, ""); err != nil {
		t.Fatalf("Approve: %v", err)
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

	// ClaimNext returns nothing while paused.
	_, ok, err := s.ClaimNext(ctx, job.ID, LaneCasebook)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("ClaimNext should return nothing while paused")
	}

	// Resume.
	if err := s.SetPaused(ctx, job.ID, false); err != nil {
		t.Fatalf("SetPaused false: %v", err)
	}
	_, ok, err = s.ClaimNext(ctx, job.ID, LaneCasebook)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("ClaimNext should return a step after resume")
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

		_ = d.Close()
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
		defer func() { _ = d.Close() }()
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

// ── Fix 1: Cancel planned jobs ───────────────────────────────────────────────

// TestCancelPlannedJob verifies Cancel transitions planned → cancelled and sets finished_at.
func TestCancelPlannedJob(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobCancelled {
		t.Errorf("state = %q; want cancelled", got.State)
	}
	if got.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Cancel")
	}
}

// TestCancelNonPlannedJobFails verifies Cancel on a non-planned job returns an error.
func TestCancelNonPlannedJobFails(t *testing.T) {
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

	if err := s.Cancel(ctx, job.ID); err == nil {
		t.Error("Cancel on approved job should fail")
	}
}

// TestFinishRefusesPlanned verifies Finish returns ErrInvalidTransition for a planned job.
func TestFinishRefusesPlanned(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}

	err = s.Finish(ctx, job.ID, JobDone)
	if err == nil {
		t.Fatal("Finish on planned job should fail")
	}
	var inv *ErrInvalidTransition
	if !isInvalidTransition(err, &inv) {
		t.Errorf("expected ErrInvalidTransition, got %T: %v", err, err)
	}
}

// TestFinishRefusesCancelledAndDone verifies Finish refuses already-terminal jobs.
func TestFinishRefusesCancelledAndDone(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	// cancelled
	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, job.ID, JobFailed); err == nil {
		t.Error("Finish on cancelled job should fail")
	}

	// done
	job2, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job2.ID, "sess-x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobState(ctx, job2.ID, JobRunning); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, job2.ID, JobDone); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, job2.ID, JobFailed); err == nil {
		t.Error("Finish on done job should fail")
	}
}

func isInvalidTransition(err error, out **ErrInvalidTransition) bool {
	var e *ErrInvalidTransition
	if errors.As(err, &e) {
		if out != nil {
			*out = e
		}
		return true
	}
	return false
}

// ── Fix 2: ClaimNext atomic step claim ───────────────────────────────────────

// TestClaimNext verifies ClaimNext atomically moves a pending step to running.
func TestClaimNext(t *testing.T) {
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

	// Claim from casebook lane.
	step, ok, err := s.ClaimNext(ctx, job.ID, LaneCasebook)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if !ok {
		t.Fatal("ClaimNext: expected to find a step")
	}
	if step.Action != "branch-delete-local" {
		t.Errorf("action = %q; want branch-delete-local", step.Action)
	}
	if step.State != StepRunning {
		t.Errorf("step.State = %q; want running", step.State)
	}

	// The step should now be running; claiming again should return the next one.
	step2, ok, err := s.ClaimNext(ctx, job.ID, LaneCasebook)
	if err != nil {
		t.Fatalf("ClaimNext second: %v", err)
	}
	if !ok {
		t.Fatal("ClaimNext second: expected to find another step")
	}
	if step2.Action != "branch-delete-remote" {
		t.Errorf("action = %q; want branch-delete-remote", step2.Action)
	}
	if step2.ID == step.ID {
		t.Error("second claim returned the same step as the first")
	}
}

// TestClaimNextNotRunningOrApproved verifies ClaimNext returns nothing for planned/done jobs.
func TestClaimNextNotRunningOrApproved(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	// Planned job: ClaimNext should return nothing.
	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := s.ClaimNext(ctx, job.ID, LaneCasebook)
	if err != nil {
		t.Fatalf("ClaimNext on planned: %v", err)
	}
	if ok {
		t.Error("ClaimNext on planned job should return nothing")
	}
}

// TestClaimNextConcurrent verifies two goroutines each claim a different step.
func TestClaimNextConcurrent(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	// Build a plan with exactly two casebook steps.
	twoPlan := Plan{
		Steps: []Step{
			{Key: "k1", Action: "step-a", Lane: LaneCasebook, Command: "echo a"},
			{Key: "k2", Action: "step-b", Lane: LaneCasebook, Command: "echo b"},
		},
	}
	job, err := s.Create(ctx, twoPlan, "mac")
	if err != nil {
		t.Fatal(err)
	}
	// casebook-only, so session="" is OK.
	if _, err := s.Approve(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}

	results := make(chan JobStep, 2)
	for i := 0; i < 2; i++ {
		go func() {
			step, ok, err := s.ClaimNext(ctx, job.ID, LaneCasebook)
			if err != nil || !ok {
				results <- JobStep{} // sentinel
				return
			}
			results <- step
		}()
	}

	s1 := <-results
	s2 := <-results
	if s1.ID == 0 || s2.ID == 0 {
		t.Fatal("both goroutines should have claimed a step")
	}
	if s1.ID == s2.ID {
		t.Errorf("both goroutines claimed the same step (id=%d)", s1.ID)
	}

	// One pending step → exactly one goroutine succeeds.
	onePlan := Plan{
		Steps: []Step{
			{Key: "k1", Action: "only-step", Lane: LaneCasebook, Command: "echo x"},
		},
	}
	job2, err := s.Create(ctx, onePlan, "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job2.ID, ""); err != nil {
		t.Fatal(err)
	}

	results2 := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, ok, err := s.ClaimNext(ctx, job2.ID, LaneCasebook)
			results2 <- (err == nil && ok)
		}()
	}
	got1 := <-results2
	got2 := <-results2
	if got1 == got2 {
		t.Errorf("expected exactly one goroutine to claim: got1=%v got2=%v", got1, got2)
	}
}

// ── Fix 3: SetStepText and SetStepRestore update updated_at ──────────────────

// TestSetStepTextUpdatedAt verifies SetStepText updates updated_at.
func TestSetStepTextUpdatedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)

	s := &Store{DB: d, Now: func() time.Time { return t0 }}

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	stepID := job.Steps[0].ID

	// Advance Now so updated_at changes.
	s.Now = func() time.Time { return t1 }

	if err := s.SetStepText(ctx, stepID, "hello"); err != nil {
		t.Fatalf("SetStepText: %v", err)
	}

	var updatedAt int64
	if err := d.QueryRowContext(ctx, `SELECT updated_at FROM steps WHERE id = ?`, stepID).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if updatedAt != ms(t1) {
		t.Errorf("updated_at = %d; want %d (t1)", updatedAt, ms(t1))
	}
}

// TestSetStepRestoreUpdatedAt verifies SetStepRestore updates updated_at.
func TestSetStepRestoreUpdatedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)

	s := &Store{DB: d, Now: func() time.Time { return t0 }}

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	stepID := job.Steps[0].ID

	s.Now = func() time.Time { return t1 }

	if err := s.SetStepRestore(ctx, stepID, "git checkout main"); err != nil {
		t.Fatalf("SetStepRestore: %v", err)
	}

	var updatedAt int64
	if err := d.QueryRowContext(ctx, `SELECT updated_at FROM steps WHERE id = ?`, stepID).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if updatedAt != ms(t1) {
		t.Errorf("updated_at = %d; want %d (t1)", updatedAt, ms(t1))
	}
}

// ── Fix 4: not-found errors for SetPaused, SetStepText, SetStepRestore ───────

// TestSetPausedNotFound verifies SetPaused returns an error for a nonexistent job.
func TestSetPausedNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	if err := s.SetPaused(ctx, 9999, true); err == nil {
		t.Error("SetPaused on nonexistent job should return an error")
	}
}

// TestSetStepTextNotFound verifies SetStepText returns an error for a nonexistent step.
func TestSetStepTextNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	if err := s.SetStepText(ctx, 9999, "text"); err == nil {
		t.Error("SetStepText on nonexistent step should return an error")
	}
}

// TestSetStepRestoreNotFound verifies SetStepRestore returns an error for a nonexistent step.
func TestSetStepRestoreNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	if err := s.SetStepRestore(ctx, 9999, "restore"); err == nil {
		t.Error("SetStepRestore on nonexistent step should return an error")
	}
}

// ── Fix 5: planned → approved only through Approve ───────────────────────────

// TestSetJobStatePlannedToApprovedFails verifies SetJobState cannot move planned → approved.
func TestSetJobStatePlannedToApprovedFails(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s := NewStore(d)

	job, err := s.Create(ctx, testPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetJobState(ctx, job.ID, JobApproved); err == nil {
		t.Error("SetJobState planned→approved should fail; use Approve() instead")
	}
}

// ── Fix round 1: Fix 2 – PauseStepWithCard atomicity ────────────────────────

// TestPauseStepWithCardIsAtomic verifies that PauseStepWithCard sets the step
// state and inserts the needs-you card in a single transaction: if the
// operation fails (simulated by dropping needs_you), the step state is
// unchanged.
func TestPauseStepWithCardIsAtomic(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "atomic.db")

	d, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(d)

	// Create an agent-lane job and move the first step to running.
	agentPlan := Plan{
		Steps: []Step{
			{Key: "pr:schuettc/hail#3", Action: "pr-close", Lane: LaneAgent,
				Command: "gh pr close 3 -R schuettc/hail"},
		},
	}
	job, err := s.Create(ctx, agentPlan, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.Approve(ctx, job.ID, "s1")
	if err != nil {
		t.Fatal(err)
	}
	step := job.Steps[0]

	if err := s.SetStepState(ctx, step.ID, StepRunning, ""); err != nil {
		t.Fatal(err)
	}

	// Drop the needs_you table so the INSERT in PauseStepWithCard fails.
	if _, err := d.ExecContext(ctx, "DROP TABLE needs_you"); err != nil {
		t.Fatalf("drop needs_you: %v", err)
	}

	// PauseStepWithCard must fail.
	_, err = s.PauseStepWithCard(ctx, step.ID, StepPaused, "paused", "forced failure")
	if err == nil {
		t.Fatal("expected error from PauseStepWithCard when needs_you is gone")
	}

	// Reopen the DB and verify the step state is still running (rollback).
	_ = d.Close()
	d2, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d2.Close() }()
	s2 := NewStore(d2)
	steps, err := s2.Steps(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.ID == step.ID {
			if st.State != StepRunning {
				t.Errorf("step state = %q after failed PauseStepWithCard, want running (rollback)", st.State)
			}
			return
		}
	}
	t.Fatal("step not found after reopen")
}

// ── Task 13: MarkUndone and ReleaseUndoClaim set updated_at ──────────────────

// undoPlan returns a casebook-lane-only plan suitable for undo tests
// (Approve accepts an empty session for plans with no agent-lane steps).
func undoPlan() Plan {
	return Plan{
		BuiltAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		Head:    "abc-undo",
		Steps: []Step{
			{
				Key:     "branch:schuettc/hail@feat/undo-x",
				Action:  "branch-delete-local",
				Lane:    LaneCasebook,
				Command: "git -C '/tmp/clone' branch -D feat/undo-x",
			},
		},
	}
}

// TestMarkUndoneUpdatesUpdatedAt verifies that MarkUndone sets updated_at.
func TestMarkUndoneUpdatesUpdatedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)

	s := &Store{DB: d, Now: func() time.Time { return t0 }}

	job, err := s.Create(ctx, undoPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	stepID := job.Steps[0].ID

	// Advance Now so updated_at changes.
	s.Now = func() time.Time { return t1 }

	if err := s.MarkUndone(ctx, stepID); err != nil {
		t.Fatalf("MarkUndone: %v", err)
	}

	var updatedAt int64
	if err := d.QueryRowContext(ctx, `SELECT updated_at FROM steps WHERE id = ?`, stepID).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if updatedAt != ms(t1) {
		t.Errorf("MarkUndone: updated_at = %d; want %d (t1)", updatedAt, ms(t1))
	}
}

// TestReleaseUndoClaimUpdatesUpdatedAt verifies that ReleaseUndoClaim sets
// updated_at (regression: confirm the SQL already carries it).
func TestReleaseUndoClaimUpdatesUpdatedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)

	s := &Store{DB: d, Now: func() time.Time { return t0 }}

	job, err := s.Create(ctx, undoPlan(), "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	stepID := job.Steps[0].ID

	// Claim the undo at t0.
	if err := s.MarkUndone(ctx, stepID); err != nil {
		t.Fatalf("MarkUndone: %v", err)
	}

	// Advance Now and release at t1.
	s.Now = func() time.Time { return t1 }

	if err := s.ReleaseUndoClaim(ctx, stepID, "test release"); err != nil {
		t.Fatalf("ReleaseUndoClaim: %v", err)
	}

	var updatedAt int64
	if err := d.QueryRowContext(ctx, `SELECT updated_at FROM steps WHERE id = ?`, stepID).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if updatedAt != ms(t1) {
		t.Errorf("ReleaseUndoClaim: updated_at = %d; want %d (t1)", updatedAt, ms(t1))
	}
}

// ── Final fix round: Issue 1 – Store.Settle drives jobs to terminal state ─────

// makeJob creates a casebook-only job, approves it, and runs it through the
// given step state sequence.
func makeJob(t *testing.T, s *Store, steps []Step) (Job, []JobStep) {
	t.Helper()
	ctx := context.Background()
	job, err := s.Create(ctx, Plan{BuiltAt: time.Now(), Steps: steps}, "mac")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Determine whether any agent lane steps exist.
	session := ""
	for _, st := range steps {
		if st.Lane == LaneAgent {
			session = "sess-settle"
			break
		}
	}
	job, err = s.Approve(ctx, job.ID, session)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	_ = s.SetJobState(ctx, job.ID, JobRunning)
	job, _ = s.Get(ctx, job.ID)
	return job, job.Steps
}

// TestSettleCasebookOnlyJobEndsDone verifies that Settle transitions a
// casebook-only job to done once all its steps are verified or skipped.
func TestSettleCasebookOnlyJobEndsDone(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openTestDB(t))

	steps := []Step{
		{Key: "k1", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D x"},
		{Key: "k2", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D y"},
	}
	job, jsteps := makeJob(t, s, steps)

	// Before settling: job stays running with mixed step states.
	state, changed, err := s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if changed {
		t.Error("Settle should not change state when steps are still pending")
	}
	if state != JobRunning {
		t.Errorf("state = %q; want running", state)
	}

	// Advance both steps to verified.
	for _, st := range jsteps {
		_ = s.SetStepState(ctx, st.ID, StepRunning, "")
		_ = s.SetStepState(ctx, st.ID, StepReported, "")
		_ = s.SetStepState(ctx, st.ID, StepVerified, "")
	}

	state, changed, err = s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !changed {
		t.Error("Settle should have changed state to done")
	}
	if state != JobDone {
		t.Errorf("state = %q; want done", state)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.State != JobDone {
		t.Errorf("persisted state = %q; want done", got.State)
	}
	if got.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Settle→done")
	}
}

// TestSettleAgentOnlyJobEndsDone verifies that Settle transitions an
// agent-only job to done after its last step is verified.
func TestSettleAgentOnlyJobEndsDone(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openTestDB(t))

	steps := []Step{
		{Key: "pr:schuettc/hail#1", Action: "pr-close", Lane: LaneAgent,
			Command: "gh pr close 1 -R schuettc/hail"},
	}
	job, jsteps := makeJob(t, s, steps)

	_ = s.SetStepState(ctx, jsteps[0].ID, StepRunning, "")
	_ = s.SetStepState(ctx, jsteps[0].ID, StepReported, "")
	_ = s.SetStepState(ctx, jsteps[0].ID, StepVerified, "")

	state, changed, err := s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !changed || state != JobDone {
		t.Errorf("Settle = (%q, %v); want (done, true)", state, changed)
	}
}

// TestSettleMixedJobStaysPendingUntilBothLanesDone verifies that a job with
// both casebook-lane and agent-lane steps stays running until all steps
// across both lanes are terminal.
func TestSettleMixedJobStaysPendingUntilBothLanesDone(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openTestDB(t))

	steps := []Step{
		{Key: "k1", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D x"},
		{Key: "pr:schuettc/hail#5", Action: "pr-close", Lane: LaneAgent, Command: "gh pr close 5 -R schuettc/hail"},
	}
	job, jsteps := makeJob(t, s, steps)

	// Finish only the casebook step.
	_ = s.SetStepState(ctx, jsteps[0].ID, StepRunning, "")
	_ = s.SetStepState(ctx, jsteps[0].ID, StepReported, "")
	_ = s.SetStepState(ctx, jsteps[0].ID, StepVerified, "")

	state, changed, err := s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if changed {
		t.Error("Settle should not finish a mixed job when the agent lane is still pending")
	}
	if state != JobRunning {
		t.Errorf("state = %q; want running", state)
	}

	// The agent reports its step, but casebook hasn't verified it yet: still not done.
	_ = s.SetStepState(ctx, jsteps[1].ID, StepRunning, "")
	_ = s.SetStepState(ctx, jsteps[1].ID, StepReported, "")
	if state, changed, err := s.Settle(ctx, job.ID); err != nil || changed || state != JobRunning {
		t.Fatalf("Settle with a reported-but-unverified step = (%q, %v, %v); want (running, false, nil)", state, changed, err)
	}

	// Verified: now the job is done.
	_ = s.SetStepState(ctx, jsteps[1].ID, StepVerified, "")

	state, changed, err = s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle mixed done: %v", err)
	}
	if !changed || state != JobDone {
		t.Errorf("Settle = (%q, %v); want (done, true)", state, changed)
	}
}

// TestSettleFailedStepWithNoCardEndsFailed verifies that a job whose step
// failed and has no open needs-you card transitions to failed.
func TestSettleFailedStepWithNoCardEndsFailed(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openTestDB(t))

	steps := []Step{
		{Key: "k1", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D x"},
	}
	job, jsteps := makeJob(t, s, steps)

	_ = s.SetStepState(ctx, jsteps[0].ID, StepRunning, "")
	_ = s.SetStepState(ctx, jsteps[0].ID, StepFailed, "network error")

	state, changed, err := s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !changed || state != JobFailed {
		t.Errorf("Settle = (%q, %v); want (failed, true)", state, changed)
	}
}

// TestSettleJobWithOpenCardStaysRunning verifies that a job with a failed step
// and an open needs-you card does not transition (Court must act first).
func TestSettleJobWithOpenCardStaysRunning(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openTestDB(t))

	steps := []Step{
		{Key: "k1", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D x"},
	}
	job, jsteps := makeJob(t, s, steps)

	_ = s.SetStepState(ctx, jsteps[0].ID, StepRunning, "")
	// PauseStepWithCard fails→paused, opening a card atomically.
	_, err := s.PauseStepWithCard(ctx, jsteps[0].ID, StepFailed, "failed", "network error")
	if err != nil {
		t.Fatalf("PauseStepWithCard: %v", err)
	}

	state, changed, err := s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if changed {
		t.Error("Settle should not change state while an open needs-you card exists")
	}
	if state != JobRunning {
		t.Errorf("state = %q; want running", state)
	}
}

// TestSettleSkippedStepsCountAsDone verifies that skipped steps count as done
// for the purposes of job completion.
func TestSettleSkippedStepsCountAsDone(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openTestDB(t))

	steps := []Step{
		{Key: "k1", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D x"},
		{Key: "k2", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D y"},
	}
	job, jsteps := makeJob(t, s, steps)

	// First step verified, second step skipped.
	_ = s.SetStepState(ctx, jsteps[0].ID, StepRunning, "")
	_ = s.SetStepState(ctx, jsteps[0].ID, StepReported, "")
	_ = s.SetStepState(ctx, jsteps[0].ID, StepVerified, "")
	_ = s.SetStepState(ctx, jsteps[1].ID, StepRunning, "")
	_ = s.SetStepState(ctx, jsteps[1].ID, StepSkipped, "precondition failed")

	state, changed, err := s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if !changed || state != JobDone {
		t.Errorf("Settle = (%q, %v); want (done, true)", state, changed)
	}
}

// TestSettleAlreadyTerminalIsNoop verifies that Settle on a terminal job
// returns changed=false and the current state.
func TestSettleAlreadyTerminalIsNoop(t *testing.T) {
	ctx := context.Background()
	s := NewStore(openTestDB(t))

	job, err := s.Create(ctx, Plan{BuiltAt: time.Now(), Steps: []Step{
		{Key: "k1", Action: "branch-delete-local", Lane: LaneCasebook, Command: "git -C /x branch -D x"},
	}}, "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(ctx, job.ID, JobDone); err != nil {
		t.Fatal(err)
	}

	state, changed, err := s.Settle(ctx, job.ID)
	if err != nil {
		t.Fatalf("Settle on done: %v", err)
	}
	if changed {
		t.Error("Settle on done job should not change state")
	}
	if state != JobDone {
		t.Errorf("state = %q; want done", state)
	}
}
