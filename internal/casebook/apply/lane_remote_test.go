package apply

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// landedRemote makes a clone whose origin (a bare remote) has feat/x at the same
// commit as main (so the remote tip is landed). It returns the clone, the bare
// remote path and feat/x's tip.
func landedRemote(t *testing.T) (clone, bare, tip string) {
	t.Helper()
	clone = testgit.NewRepo(t)
	bare = testgit.NewBare(t)
	testgit.Git(t, clone, "remote", "add", "origin", bare)
	testgit.Git(t, clone, "push", "-q", "origin", "main")
	testgit.Git(t, clone, "branch", "feat/x", "main")
	testgit.Git(t, clone, "push", "-q", "origin", "feat/x")
	testgit.Git(t, clone, "fetch", "-q", "origin")
	tip = testgit.Git(t, clone, "rev-parse", "refs/heads/feat/x")
	return clone, bare, tip
}

func remoteHas(t *testing.T, clone, remote, branch string) bool {
	t.Helper()
	out := testgit.Git(t, clone, "ls-remote", remote, "refs/heads/"+branch)
	return strings.TrimSpace(out) != ""
}

func TestRemoteBranchDeletedThenVerified(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, _, tip := landedRemote(t)
	spy := newSpy()
	cb := casebookRepo(t)
	s, job := laneJob(t, ctx, JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-remote",
		Lane:         LaneCasebook,
		Command:      branchDeleteRemoteCmd(clone, "origin", "feat/x"),
		Precondition: "remote-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	})
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC) }}
	if err := s.RunCasebookLane(ctx, job, r, Env{RunGit: spy.run}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.Steps[0].State != StepVerified {
		t.Fatalf("step state = %q, want verified (detail %q)", got.Steps[0].State, got.Steps[0].Detail)
	}
	if remoteHas(t, clone, "origin", "feat/x") {
		t.Error("remote branch still present after delete")
	}
	want := "git -C '" + clone + "' push 'origin' " + tip + ":refs/heads/feat/x"
	if got.Steps[0].Restore != want {
		t.Errorf("restore = %q, want %q", got.Steps[0].Restore, want)
	}
}

func TestAbsentRemoteBranchIsDone(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone := testgit.NewRepo(t)
	bare := testgit.NewBare(t)
	testgit.Git(t, clone, "remote", "add", "origin", bare)
	testgit.Git(t, clone, "push", "-q", "origin", "main")
	// feat/x was never pushed: it is already absent on the remote.
	spy := newSpy()
	cb := casebookRepo(t)
	s, job := laneJob(t, ctx, JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-remote",
		Lane:         LaneCasebook,
		Command:      branchDeleteRemoteCmd(clone, "origin", "feat/x"),
		Precondition: "remote-tip-unchanged-and-landed",
	})
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	if err := s.RunCasebookLane(ctx, job, r, Env{RunGit: spy.run}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	// Already absent: verified, never skipped, never a command, never a restore.
	if got.Steps[0].State != StepVerified {
		t.Fatalf("step state = %q, want verified", got.Steps[0].State)
	}
	if spy.ran("--delete") {
		t.Error("a delete ran for an already-absent remote branch")
	}
	if got.Steps[0].Restore != "" {
		t.Error("a restore was recorded for an already-absent remote branch")
	}
}

func TestLiveRemoteTipDiffersFromExpected(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, _, liveTip := landedRemote(t)
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-remote",
		Command:      branchDeleteRemoteCmd(clone, "origin", "feat/x"),
		Precondition: "remote-tip-unchanged-and-landed",
		ExpectedTip:  "0000000000000000000000000000000000000000",
	}
	res, _ := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if res.State != StepSkipped {
		t.Fatalf("state = %q, want skipped (live tip %s)", res.State, liveTip)
	}
	if !strings.Contains(res.Detail, "remote tip moved") {
		t.Errorf("reason = %q, want remote-tip-moved", res.Detail)
	}
	if spy.ran("--delete") {
		t.Error("a delete ran despite the tip mismatch")
	}
}

func TestPauseStopsLaneAfterStep(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	cloneA, tipA := landedClone(t)
	cloneB, tipB := landedCloneAt(t, filepath.Join(t.TempDir(), "b"))
	spy := newSpy()
	cb := casebookRepo(t)
	s, job := laneJob(t, ctx,
		JobStep{Key: "branch:schuettc/hail@feat/x", Action: "branch-delete-local", Lane: LaneCasebook,
			Command: branchDeleteLocalCmd(cloneA, "feat/x"), Precondition: "branch-tip-unchanged-and-landed", ExpectedTip: tipA},
		JobStep{Key: "branch:schuettc/hail@feat/x", Action: "branch-delete-local", Lane: LaneCasebook,
			Command: branchDeleteLocalCmd(cloneB, "feat/x"), Precondition: "branch-tip-unchanged-and-landed", ExpectedTip: tipB},
	)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	// Pause after the first step.
	calls := 0
	pause := func() bool { calls++; return calls > 1 }
	if err := s.RunCasebookLane(ctx, job, r, Env{RunGit: spy.run}, pause); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.Steps[0].State != StepVerified {
		t.Fatalf("step0 = %q, want verified", got.Steps[0].State)
	}
	if got.Steps[1].State != StepPending {
		t.Errorf("step1 = %q, want pending (paused before it ran)", got.Steps[1].State)
	}
	if branchExists(t, cloneB, "feat/x") == false {
		t.Error("clone B branch should be untouched after pause")
	}
}

// landedCloneAt is landedClone but at a chosen directory.
func landedCloneAt(t *testing.T, dir string) (clone, tip string) {
	t.Helper()
	testgit.Git(t, filepath.Dir(dir), "init", "-q", "-b", "main", dir)
	testgit.Commit(t, dir, "README", "hi\n")
	testgit.Git(t, dir, "branch", "feat/x", "main")
	tip = testgit.Git(t, dir, "rev-parse", "refs/heads/feat/x")
	return dir, tip
}
