package apply

import (
	"context"
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
	job, err := s.Create(ctx, plan)
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
	job, err := s.Create(ctx, plan)
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
	job1, err := s.Create(ctx, plan1)
	if err != nil {
		t.Fatal(err)
	}

	plan2 := Plan{
		BuiltAt: time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC),
		Steps: []Step{
			{Key: "repo:schuettc/old", Action: "repo-archive", Lane: LaneAgent, Command: "gh repo archive 'schuettc/old' --yes"},
		},
	}
	_, err = s.Create(ctx, plan2)
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
	job3, err := s.Create(ctx, plan3)
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
