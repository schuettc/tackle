package serve

// Fix round 2 tests:
//   1. TestRacingResumeDoesNotStrandPendingStep – lost-wakeup in lane guard
//   2. TestConcurrentUndosRunRestoreOnce       – undo claims before executing
//   3. TestFailingRestoreLeavesStepUndoable    – failed restore releases claim

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// mkCasebookStep returns a casebook-lane step that verifies immediately
// (the spy RunGit reports the branch is already gone).
func mkCasebookStep(dir, branch, tip string) apply.Step {
	return apply.Step{
		Key:          "branch:schuettc/hail@" + branch,
		Action:       "branch-delete-local",
		Lane:         apply.LaneCasebook,
		Command:      "git -C '" + dir + "' update-ref -d refs/heads/" + branch + " " + tip,
		ExpectedTip:  tip,
		Precondition: "branch-tip-unchanged-and-landed",
	}
}

// TestRacingResumeDoesNotStrandPendingStep forces the lost-wakeup window:
//
//  1. Lane runs step1 (pauses itself via the RunGit spy).
//  2. While the lane is still alive (laneRun[id]=true) but about to exit,
//     a resume arrives – the single-flight guard sees laneRun=true and no-ops.
//  3. The lane exits, clears its entry, and the fix re-checks: it sees the
//     job is not paused and has a pending step → starts a new lane.
//  4. step2 eventually becomes verified.
func TestRacingResumeDoesNotStrandPendingStep(t *testing.T) {
	r := newRig(t)

	dir := t.TempDir()
	tip := strings.Repeat("a", 40)
	var jobID int64

	// Synchronisation: the hook blocks the lane from exiting until the test
	// has called resume (which will no-op because laneRun is still true).
	laneAboutToExit := make(chan struct{})  // lane signals it is about to exit
	testCalledResume := make(chan struct{}) // test signals resume was called

	// Spy: reports branch as gone (both steps verify immediately) and pauses
	// the job the moment step1 starts its precondition check.
	r.s.Runner.RunGit = func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--git-dir" {
			return ".git", nil
		}
		if len(args) >= 3 && args[0] == "rev-parse" && args[1] == "--verify" {
			// Pause when step1's branch (feat/p) is being checked.
			if strings.Contains(args[2], "feat/p") && jobID != 0 {
				_ = r.s.Apply.SetPaused(r.s.laneCtx(), jobID, true)
			}
			return "", errNoRef // branch is gone → precondition satisfied
		}
		return "", nil
	}

	// Hook: fires BEFORE the lane clears laneRun[id], inside the goroutine's
	// deferred cleanup. The test calls resume here (which no-ops because the
	// lane is still registered) and then unblocks the lane.
	// The hook is one-shot: it removes itself on first call so the second lane
	// (started by the fix) doesn't re-trigger it.
	var hookOnce sync.Once
	r.s.onBeforeLaneExit = func(id int64) {
		hookOnce.Do(func() {
			// Tell the test the lane is at the exit point.
			close(laneAboutToExit)
			// Wait for the test to call resume and confirm it no-oped.
			<-testCalledResume
		})
	}

	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-race",
		Steps:   []apply.Step{mkCasebookStep(dir, "feat/p", tip), mkCasebookStep(dir, "feat/q", tip)},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	jobID = job.ID

	// Approve via the endpoint (starts the casebook lane).
	var jv JobView
	if code := r.do(t, "POST", "/api/apply/approve", map[string]any{"plan_id": job.ID}, &jv); code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}

	// Wait until the lane is about to exit (step1 ran, pause was set).
	select {
	case <-laneAboutToExit:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: lane never reached exit point")
	}

	// Call resume while the lane is still alive (laneRun[id]=true).
	// The single-flight guard must no-op here.
	if code := r.do(t, "POST", "/api/jobs/resume", map[string]any{"id": job.ID}, nil); code != http.StatusOK {
		t.Fatalf("resume: %d", code)
	}
	close(testCalledResume) // unblock the lane to finish its cleanup

	// The fix must now start a new lane for step2. Wait for it to complete.
	deadline := time.Now().Add(5 * time.Second)
	var step2 apply.JobStep
	step2ID := job.Steps[1].ID
	for time.Now().Before(deadline) {
		step2, _ = r.s.Apply.GetStep(ctx, step2ID)
		if step2.State == apply.StepVerified || step2.State == apply.StepFailed ||
			step2.State == apply.StepSkipped {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if step2.State != apply.StepVerified {
		t.Fatalf("step2 state = %q after racing resume; want verified (fix: lane must restart)", step2.State)
	}
}

// errNoRef is used in the RunGit spy to signal "branch not found".
var errNoRef = &errNoRefType{}

type errNoRefType struct{}

func (e *errNoRefType) Error() string { return "fatal: no such ref" }

// TestConcurrentUndosRunRestoreOnce: two goroutines call POST /api/jobs/undo
// for the same step at the same time. The restore command must run exactly once;
// the second caller must get 409.
func TestConcurrentUndosRunRestoreOnce(t *testing.T) {
	r := newRig(t)

	var restoreCount int
	var mu sync.Mutex

	// A blocking restore that we can release after both goroutines start.
	restoreStarted := make(chan struct{}, 2)
	releaseRestore := make(chan struct{})

	r.s.Runner.RunGit = func(_ context.Context, _ string, args ...string) (string, error) {
		// The restore command is "git -C <dir> branch feat/x <tip>" which uses "branch".
		if len(args) >= 1 && args[0] == "branch" {
			mu.Lock()
			restoreCount++
			mu.Unlock()
			restoreStarted <- struct{}{}
			<-releaseRestore // block until the test releases
		}
		return "", nil
	}

	// Create a job with a verified casebook-lane step that has an auto restore.
	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-concurrent",
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
	if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	step := job.Steps[0]
	for _, st := range []apply.StepState{apply.StepRunning, apply.StepReported, apply.StepVerified} {
		if err := r.s.Apply.SetStepState(ctx, step.ID, st, ""); err != nil {
			t.Fatalf("→%s: %v", st, err)
		}
	}
	autoRestore := "git -C '/tmp/clone' branch feat/x abc123abc123abc123abc123abc123abc123abc123"
	if err := r.s.Apply.SetStepRestore(ctx, step.ID, autoRestore); err != nil {
		t.Fatalf("SetStepRestore: %v", err)
	}

	// Fire two concurrent undos.
	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			var out map[string]any
			codes[i] = r.do(t, "POST", "/api/jobs/undo", map[string]any{"step": step.ID}, &out)
		}()
	}

	// Wait for at most one restore to start, then release.
	select {
	case <-restoreStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for restore to start")
	}
	close(releaseRestore)
	wg.Wait()

	// Exactly one must succeed (200) and exactly one must fail (409).
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Errorf("concurrent undo: got ok=%d conflict=%d, want ok=1 conflict=1", ok, conflict)
	}

	// The restore must have run exactly once.
	mu.Lock()
	count := restoreCount
	mu.Unlock()
	if count != 1 {
		t.Errorf("restore command ran %d times, want 1", count)
	}
}

// TestFailingRestoreLeavesStepUndoable: when the restore command fails, the
// step's undo claim is released so Court can retry. The step must not be left
// in an undone state.
func TestFailingRestoreLeavesStepUndoable(t *testing.T) {
	r := newRig(t)

	r.s.Runner.RunGit = func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) >= 1 && args[0] == "branch" {
			return "", errNoRef // simulate restore failure
		}
		return "", nil
	}

	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-fail-restore",
		Steps: []apply.Step{
			{
				Key:     "branch:schuettc/hail@feat/z",
				Action:  "branch-delete-local",
				Lane:    apply.LaneCasebook,
				Command: "git -C '/tmp/clone' branch -D feat/z",
			},
		},
	}
	job, err := r.s.Apply.Create(ctx, plan, "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	step := job.Steps[0]
	for _, st := range []apply.StepState{apply.StepRunning, apply.StepReported, apply.StepVerified} {
		if err := r.s.Apply.SetStepState(ctx, step.ID, st, ""); err != nil {
			t.Fatalf("→%s: %v", st, err)
		}
	}
	autoRestore := "git -C '/tmp/clone' branch feat/z abc123abc123abc123abc123abc123abc123abc123"
	if err := r.s.Apply.SetStepRestore(ctx, step.ID, autoRestore); err != nil {
		t.Fatalf("SetStepRestore: %v", err)
	}

	// Undo should fail (restore command fails).
	var out map[string]any
	code := r.do(t, "POST", "/api/jobs/undo", map[string]any{"step": step.ID}, &out)
	if code == http.StatusOK {
		t.Fatalf("undo with failing restore: got 200, want error")
	}

	// The step must NOT be undone (undone_at must be zero — claim was released).
	updated, err := r.s.Apply.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("GetStep: %v", err)
	}
	if !updated.UndoneAt.IsZero() {
		t.Error("step.UndoneAt is set after a failing restore; claim must be released so Court can retry")
	}

	// A subsequent undo attempt (e.g. with a working restore) must be possible.
	// Swap in a working RunGit and retry.
	r.s.Runner.RunGit = func(_ context.Context, _ string, args ...string) (string, error) {
		return "", nil // restore succeeds now
	}
	var out2 UndoResult
	if code := r.do(t, "POST", "/api/jobs/undo", map[string]any{"step": step.ID}, &out2); code != http.StatusOK {
		t.Fatalf("retry undo after release: got %d, want 200", code)
	}
	retried, err := r.s.Apply.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("GetStep after retry: %v", err)
	}
	if retried.UndoneAt.IsZero() {
		t.Error("step.UndoneAt not set after successful retry")
	}
}
