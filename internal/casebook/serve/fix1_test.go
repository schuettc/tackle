package serve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
)

// mustGit runs a git command in dir, failing the test on error.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// Fix A: the agent-lane job body tells the agent, in plain words, to wait for
// Court's batch confirmation before running any step.
func TestBuildJobBodyTellsAgentToWaitForBatch(t *testing.T) {
	r := newRig(t)
	job, err := r.s.Apply.Create(ctx, agentTestPlan(), "mbp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	body, err := buildJobBody(job)
	if err != nil {
		t.Fatalf("buildJobBody: %v", err)
	}
	for _, want := range []string{
		"Do not run any step until Court confirms the batch.",
		"The confirmation arrives as a message in this thread.",
		"If Court skips the batch, run nothing.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("job body missing %q; got:\n%s", want, body)
		}
	}
}

// Fix B: autoUndoable accepts a worktree-restore command and POST /api/jobs/undo
// runs it, bringing a removed worktree back (hermetic repo, real git).
func TestUndoWorktreeRestoreBringsItBack(t *testing.T) {
	r := newRig(t)

	// Hermetic git repo with one commit and a worktree checked out on feat/x.
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q", "-b", "main")
	mustGit(t, repo, "config", "user.email", "t@example.com")
	mustGit(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-q", "-m", "init")

	wt := filepath.Join(t.TempDir(), "wt")
	mustGit(t, repo, "worktree", "add", "-q", "-b", "feat/x", wt)

	// Simulate the step already ran: the worktree was removed.
	mustGit(t, repo, "worktree", "remove", wt)
	if _, err := os.Stat(wt); err == nil {
		t.Fatalf("worktree %s should be gone before undo", wt)
	}

	// A verified casebook-lane step whose restore re-adds the worktree.
	restore := "git -C '" + repo + "' worktree add '" + wt + "' 'feat/x'"
	if !autoUndoable(restore) {
		t.Fatalf("autoUndoable(%q) = false, want true (worktree restore)", restore)
	}
	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-wt",
		Steps: []apply.Step{{
			Key:     "worktree:mbp:" + wt,
			Action:  "worktree-remove",
			Lane:    apply.LaneCasebook,
			Command: "git -C '" + repo + "' worktree remove '" + wt + "'",
		}},
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
	if err := r.s.Apply.SetStepRestore(ctx, step.ID, restore); err != nil {
		t.Fatalf("SetStepRestore: %v", err)
	}

	var undoOut UndoResult
	if code := r.do(t, "POST", "/api/jobs/undo", map[string]any{"step": step.ID}, &undoOut); code != 200 {
		t.Fatalf("undo worktree restore: got %d", code)
	}

	// The worktree is back.
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree %s not restored: %v", wt, err)
	}
	list := mustGit(t, repo, "worktree", "list", "--porcelain")
	if !strings.Contains(list, wt) {
		t.Fatalf("worktree list does not include %s:\n%s", wt, list)
	}
	updated, err := r.s.Apply.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("GetStep: %v", err)
	}
	if updated.UndoneAt.IsZero() {
		t.Error("step undone_at not set after undo")
	}
}

// secondServer opens a fresh Server over the same app and database, simulating a
// serve restart.
func secondServer(t *testing.T, r *rig) *Server {
	t.Helper()
	s2, err := New(context.Background(), r.App, r.s.DB)
	if err != nil {
		t.Fatalf("New (restart): %v", err)
	}
	return s2
}

// casebookOnlyPlan is a plan with a single casebook-lane branch-delete step.
func casebookOnlyPlan(key, command string) apply.Plan {
	return apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-cb",
		Steps: []apply.Step{{
			Key:     key,
			Action:  "branch-delete-local",
			Lane:    apply.LaneCasebook,
			Command: command,
		}},
	}
}

// Fix C: one casebook lane per job at a time.
func TestOneCasebookLanePerJob(t *testing.T) {
	// Resume on a running, never-paused job starts no second lane.
	t.Run("resume on never-paused job starts no lane", func(t *testing.T) {
		r := newRig(t)
		var starts int32
		r.s.onLaneStart = func(int64) { atomic.AddInt32(&starts, 1) }

		job, err := r.s.Apply.Create(ctx, casebookOnlyPlan("branch:schuettc/hail@feat/a", "git -C '/tmp/none' branch -D feat/a"), "mbp")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if err := r.s.Apply.SetJobState(ctx, job.ID, apply.JobRunning); err != nil {
			t.Fatalf("SetJobState running: %v", err)
		}

		if code := r.do(t, "POST", "/api/jobs/resume", map[string]any{"id": job.ID}, nil); code != 200 {
			t.Fatalf("resume: %d", code)
		}
		if got := atomic.LoadInt32(&starts); got != 0 {
			t.Fatalf("resume on never-paused job started %d lane(s), want 0", got)
		}
	})

	// pause → resume → resume starts at most one lane.
	t.Run("pause then resume twice starts at most one", func(t *testing.T) {
		r := newRig(t)
		// A no-op runner so the relaunched lane does no real git work.
		r.s.Runner.RunGit = func(context.Context, string, ...string) (string, error) { return "", nil }
		var starts int32
		r.s.onLaneStart = func(int64) { atomic.AddInt32(&starts, 1) }

		job, err := r.s.Apply.Create(ctx, casebookOnlyPlan("branch:schuettc/hail@feat/b", "git -C '/tmp/none' branch -D feat/b"), "mbp")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if err := r.s.Apply.SetJobState(ctx, job.ID, apply.JobRunning); err != nil {
			t.Fatalf("SetJobState running: %v", err)
		}
		if err := r.s.Apply.SetPaused(ctx, job.ID, true); err != nil {
			t.Fatalf("SetPaused: %v", err)
		}

		if code := r.do(t, "POST", "/api/jobs/resume", map[string]any{"id": job.ID}, nil); code != 200 {
			t.Fatalf("first resume: %d", code)
		}
		if code := r.do(t, "POST", "/api/jobs/resume", map[string]any{"id": job.ID}, nil); code != 200 {
			t.Fatalf("second resume: %d", code)
		}
		if got := atomic.LoadInt32(&starts); got != 1 {
			t.Fatalf("pause+resume+resume started %d lane(s), want 1", got)
		}
	})
}

// Fix D: a serve restart resumes a casebook-lane step left running; the step
// ends verified with no command run (the target is already gone).
func TestServeRestartResumesCasebookLane(t *testing.T) {
	r := newRig(t)

	// Hermetic repo on main with one commit; feat/gone is never created, so the
	// delete's target is already absent.
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q", "-b", "main")
	mustGit(t, repo, "config", "user.email", "t@example.com")
	mustGit(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "commit", "-q", "-m", "init")
	headBefore := mustGit(t, repo, "rev-parse", "main")

	plan := apply.Plan{
		BuiltAt: time.Now(),
		Head:    "head-restart",
		Steps: []apply.Step{{
			Key:          "branch:schuettc/hail@feat/gone",
			Action:       "branch-delete-local",
			Lane:         apply.LaneCasebook,
			Command:      "git -C '" + repo + "' update-ref -d refs/heads/feat/gone " + headBefore,
			ExpectedTip:  headBefore,
			Precondition: "branch-tip-unchanged-and-landed",
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
	step := job.Steps[0]
	// Simulate a serve that died with the step mid-run.
	if err := r.s.Apply.SetStepState(ctx, step.ID, apply.StepRunning, ""); err != nil {
		t.Fatalf("→running: %v", err)
	}

	// Restart: New() requeues the running casebook step to pending and relaunches
	// the lane.
	s2 := secondServer(t, r)

	deadline := time.Now().Add(3 * time.Second)
	var final apply.JobStep
	for time.Now().Before(deadline) {
		final, err = s2.Apply.GetStep(ctx, step.ID)
		if err != nil {
			t.Fatalf("GetStep: %v", err)
		}
		if final.State == apply.StepVerified || final.State == apply.StepFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != apply.StepVerified {
		t.Fatalf("step state = %q (detail %q) after restart, want verified", final.State, final.Detail)
	}
	// No command ran: main is untouched and feat/gone still does not exist.
	if got := mustGit(t, repo, "rev-parse", "main"); got != headBefore {
		t.Fatalf("main moved: %q -> %q", headBefore, got)
	}
	if _, err := git(t, repo, "rev-parse", "--verify", "refs/heads/feat/gone"); err == nil {
		t.Fatal("feat/gone unexpectedly exists")
	}
}

// Fix E: answering a paused card with resume is lane-aware.
func TestAnswerPausedResumeIsLaneAware(t *testing.T) {
	// Casebook-lane step: goes back to pending and the lane is relaunched.
	t.Run("casebook step requeues and relaunches", func(t *testing.T) {
		r := newRig(t)
		var starts int32
		r.s.onLaneStart = func(int64) { atomic.AddInt32(&starts, 1) }

		job, err := r.s.Apply.Create(ctx, casebookOnlyPlan("branch:schuettc/hail@feat/c", "git -C '/tmp/none' branch -D feat/c"), "mbp")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := r.s.Apply.Approve(ctx, job.ID, ""); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		step := job.Steps[0]
		if err := r.s.Apply.SetStepState(ctx, step.ID, apply.StepRunning, ""); err != nil {
			t.Fatalf("→running: %v", err)
		}
		card, err := r.s.Apply.PauseStepWithCard(ctx, step.ID, apply.StepPaused, "paused", "precondition failed")
		if err != nil {
			t.Fatalf("PauseStepWithCard: %v", err)
		}
		// Pause the job so the relaunched lane is a deterministic no-op; we only
		// assert the step requeued and the lane was relaunched.
		if err := r.s.Apply.SetPaused(ctx, job.ID, true); err != nil {
			t.Fatalf("SetPaused: %v", err)
		}

		var out AnswerResult
		if code := r.do(t, "POST", "/api/jobs/answer", map[string]any{"needs_you": card.ID, "action": "resume"}, &out); code != 200 {
			t.Fatalf("answer resume: %d", code)
		}
		updated, err := r.s.Apply.GetStep(ctx, step.ID)
		if err != nil {
			t.Fatalf("GetStep: %v", err)
		}
		if updated.State != apply.StepPending {
			t.Errorf("casebook step state = %q after resume, want pending", updated.State)
		}
		if got := atomic.LoadInt32(&starts); got != 1 {
			t.Errorf("lane relaunched %d times, want 1", got)
		}
	})

	// Agent-lane step: gets a message to the job's session to resume it.
	t.Run("agent step gets a resume message", func(t *testing.T) {
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
		step := job.Steps[0] // agent lane
		if err := r.s.Apply.StartStepWithJob(ctx, job.ID, step.ID); err != nil {
			t.Fatalf("StartStepWithJob: %v", err)
		}
		card, err := r.s.Apply.PauseStepWithCard(ctx, step.ID, apply.StepPaused, "paused", "agent paused")
		if err != nil {
			t.Fatalf("PauseStepWithCard: %v", err)
		}

		var out AnswerResult
		if code := r.do(t, "POST", "/api/jobs/answer", map[string]any{"needs_you": card.ID, "action": "resume"}, &out); code != 200 {
			t.Fatalf("answer resume: %d", code)
		}
		var w struct {
			Delivery *struct{ ID int64 } `json:"delivery"`
			Text     string              `json:"text"`
		}
		if code := r.do(t, "GET", "/api/agent/wait?session=s1&timeout=2", nil, &w); code != 200 || w.Delivery == nil {
			t.Fatalf("no resume delivery to s1 (code=%d)", code)
		}
		if !strings.Contains(w.Text, "Resume that step") {
			t.Errorf("resume message missing instruction; got: %q", truncate(w.Text, 300))
		}
	})

}
