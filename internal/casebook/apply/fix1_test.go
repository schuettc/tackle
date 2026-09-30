package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// runRestore executes a recorded "git -C <dir> ..." restore command against the
// real (hermetic) repo, so a test can prove the restore actually restores.
func runRestore(t *testing.T, cmd string) {
	t.Helper()
	dir, args, err := gitCommand(cmd)
	if err != nil {
		t.Fatalf("parse restore %q: %v", cmd, err)
	}
	if _, err := gitx.Run(context.Background(), dir, args...); err != nil {
		t.Fatalf("run restore %q: %v", cmd, err)
	}
}

// scriptGh is a configurable fake gh for the agent-lane precondition tests.
type scriptGh struct {
	fn func(args []string) ([]byte, error)
}

func (g scriptGh) Gh(_ context.Context, args ...string) ([]byte, error) { return g.fn(args) }

// --- Ruling 1: the compare-and-delete command closes the check→delete race ---

// TestCompareAndDeleteRefusesMovedBranch: the precondition passes at the live
// tip, but the branch moves after the check and before the command (the spy
// injects a commit). The baked-in expected tip makes update-ref refuse, so the
// step fails and the branch survives at the NEW tip.
func TestCompareAndDeleteRefusesMovedBranch(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, tip := landedClone(t)

	spy := newSpy()
	cb := casebookRepo(t)
	spy.casebook = cb.Dir
	// Move the branch right before the destructive command runs.
	var newTip string
	spy.mutate = func() {
		testgit.Git(t, clone, "switch", "-q", "feat/x")
		newTip = testgit.Commit(t, clone, "raced", "raced")
		testgit.Git(t, clone, "switch", "-q", "main")
	}
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(clone, "feat/x", tip),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	}
	res, err := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StepFailed {
		t.Fatalf("state = %q, want failed (the delete must refuse a moved branch)", res.State)
	}
	if !branchExists(t, clone, "feat/x") {
		t.Fatal("branch was deleted despite moving after the check")
	}
	got := testgit.Git(t, clone, "rev-parse", "refs/heads/feat/x")
	if strings.TrimSpace(got) != newTip {
		t.Errorf("branch tip = %q, want the new (raced) tip %q", strings.TrimSpace(got), newTip)
	}
}

// worktree remove must never be forced (it refuses dirty/locked worktrees on
// its own). No --force anywhere.
func TestWorktreeRemoveNeverForced(t *testing.T) {
	if strings.Contains(worktreeRemoveCmd("/x", "/y"), "--force") {
		t.Error("worktree remove command must not carry --force")
	}
}

// --- Ruling 2: a non-repo or corrupt clone is not ok ---

// TestNonRepoCloneIsNotOK: a plain directory (exists, not a git repo) must not
// be treated as "branch gone → done"; the precondition is not ok.
func TestNonRepoCloneIsNotOK(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	plain := t.TempDir() // exists, but not a git repo

	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(plain, "feat/x", "deadbeef"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  "deadbeef",
	}
	res, _ := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if res.State != StepSkipped {
		t.Fatalf("state = %q, want skipped (non-repo is not ok, never done)", res.State)
	}
	if !strings.Contains(res.Detail, "not a git repo") {
		t.Errorf("reason = %q, want \"not a git repo\"", res.Detail)
	}
}

// --- Ruling 4: restores actually restore ---

func TestLocalRestoreRestoresBranch(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, tip := landedClone(t)
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(clone, "feat/x", tip),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	}
	res, err := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if err != nil || res.State != StepReported {
		t.Fatalf("RunStep = %+v, %v", res, err)
	}
	if branchExists(t, clone, "feat/x") {
		t.Fatal("branch should be deleted before restore")
	}
	runRestore(t, res.Restore)
	if !branchExists(t, clone, "feat/x") {
		t.Fatal("branch was not restored")
	}
	back := strings.TrimSpace(testgit.Git(t, clone, "rev-parse", "refs/heads/feat/x"))
	if back != tip {
		t.Errorf("restored tip = %q, want %q", back, tip)
	}
}

func TestRemoteRestoreRestoresBranch(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, _, tip := landedRemote(t)
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-remote",
		Command:      branchDeleteRemoteCmd(clone, "origin", "feat/x", tip),
		Precondition: "remote-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	}
	res, err := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if err != nil || res.State != StepReported {
		t.Fatalf("RunStep = %+v, %v", res, err)
	}
	if remoteHas(t, clone, "origin", "feat/x") {
		t.Fatal("remote branch should be deleted before restore")
	}
	runRestore(t, res.Restore)
	if !remoteHas(t, clone, "origin", "feat/x") {
		t.Fatal("remote branch was not restored")
	}
	back := strings.Fields(testgit.Git(t, clone, "ls-remote", "origin", "refs/heads/feat/x"))[0]
	if back != tip {
		t.Errorf("restored remote tip = %q, want %q", back, tip)
	}
}

func TestWorktreeRestoreRestoresWorktree(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone := testgit.NewRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	testgit.Git(t, clone, "worktree", "add", "-q", wt, "-b", "wtbranch")
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "worktree:mbp:" + wt,
		Action:       "worktree-remove",
		Command:      worktreeRemoveCmd(clone, wt),
		Precondition: "worktree-clean",
	}
	res, err := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if err != nil || res.State != StepReported {
		t.Fatalf("RunStep = %+v, %v", res, err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("worktree should be removed before restore")
	}
	runRestore(t, res.Restore)
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree was not restored: %v", err)
	}
}

// --- Ruling 5: TSV safety ---

// TestUnsafePathRefusedBeforeAnything: a tab in the command corrupts the TSV,
// so the step fails before the precondition or any command runs.
func TestUnsafePathRefusedBeforeAnything(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Command:      "git -C '/tmp/re\tpo' update-ref -d 'refs/heads/feat/x' deadbeef",
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  "deadbeef",
	}
	res, _ := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if res.State != StepFailed {
		t.Fatalf("state = %q, want failed (unsafe path)", res.State)
	}
	if !strings.Contains(res.Detail, "tab or newline") {
		t.Errorf("detail = %q, want tab-or-newline", res.Detail)
	}
	if len(spy.calls) != 0 {
		t.Errorf("no git should have run for an unsafe path; ran %v", spy.calls)
	}
}

// --- Ruling 3: agent-lane preconditions ---

func decisionAt(ts time.Time) func(string) *item.Decision {
	return func(string) *item.Decision { return &item.Decision{DecidedAt: ts} }
}

func TestPRNoActivityPrecondition(t *testing.T) {
	ctx := context.Background()
	decidedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	step := JobStep{Key: "pr:schuettc/hail#3", Action: "pr-close", Precondition: "pr-no-new-activity"}

	viewGh := func(updated string) scriptGh {
		return scriptGh{fn: func(args []string) ([]byte, error) {
			return []byte(`{"updatedAt":"` + updated + `"}`), nil
		}}
	}

	t.Run("activity before decided_at is ok", func(t *testing.T) {
		chk, err := Check(ctx, step, Env{Gh: viewGh("2026-08-15T00:00:00Z"), Decisions: decisionAt(decidedAt)})
		if err != nil || !chk.OK {
			t.Fatalf("chk = %+v, err %v; want ok", chk, err)
		}
	})
	t.Run("activity after decided_at is not ok", func(t *testing.T) {
		chk, _ := Check(ctx, step, Env{Gh: viewGh("2026-09-15T00:00:00Z"), Decisions: decisionAt(decidedAt)})
		if chk.OK {
			t.Fatal("want not ok for activity after the decision")
		}
	})
	t.Run("gh error is not ok", func(t *testing.T) {
		errGh := scriptGh{fn: func([]string) ([]byte, error) { return nil, context.DeadlineExceeded }}
		chk, _ := Check(ctx, step, Env{Gh: errGh, Decisions: decisionAt(decidedAt)})
		if chk.OK {
			t.Fatal("want not ok on gh error")
		}
	})
	t.Run("nil Decisions is not ok", func(t *testing.T) {
		chk, _ := Check(ctx, step, Env{Gh: viewGh("2026-08-15T00:00:00Z"), Decisions: nil})
		if chk.OK {
			t.Fatal("want not ok when there is no decision to compare against")
		}
	})
}

func TestRepoNoHumanPRsPrecondition(t *testing.T) {
	ctx := context.Background()
	step := JobStep{Key: "repo:schuettc/hail", Action: "repo-archive", Precondition: "repo-no-open-human-prs"}

	t.Run("bot-authored open PR is ok", func(t *testing.T) {
		botGh := scriptGh{fn: func([]string) ([]byte, error) {
			return []byte(`[{"author":{"login":"dependabot","is_bot":true}}]`), nil
		}}
		chk, err := Check(ctx, step, Env{Gh: botGh})
		if err != nil || !chk.OK {
			t.Fatalf("chk = %+v, err %v; want ok (only a bot PR is open)", chk, err)
		}
	})
	t.Run("human-authored open PR is not ok", func(t *testing.T) {
		humanGh := scriptGh{fn: func([]string) ([]byte, error) {
			return []byte(`[{"author":{"login":"alice","is_bot":false}}]`), nil
		}}
		chk, _ := Check(ctx, step, Env{Gh: humanGh})
		if chk.OK {
			t.Fatal("want not ok when a human PR is open")
		}
	})
	t.Run("gh error is not ok", func(t *testing.T) {
		errGh := scriptGh{fn: func([]string) ([]byte, error) { return nil, context.DeadlineExceeded }}
		chk, _ := Check(ctx, step, Env{Gh: errGh})
		if chk.OK {
			t.Fatal("want not ok on gh error")
		}
	})
	t.Run("apptest.FakeGh returns is_bot/login and hail has a human PR", func(t *testing.T) {
		chk, err := Check(ctx, step, Env{Gh: apptest.FakeGh{}})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if chk.OK {
			t.Fatal("want not ok: hail has an open human PR (#3 by bob)")
		}
	})
}

func TestBranchDeleteWithoutExpectedTipNeverRuns(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	for _, action := range []string{"branch-delete-local", "branch-delete-remote"} {
		step := JobStep{
			Key:          "branch:schuettc/hail@feat/x",
			Action:       action,
			Command:      "git -C '/tmp/repo' update-ref -d 'refs/heads/feat/x'",
			Precondition: "branch-tip-unchanged-and-landed",
		}
		res, _ := r.RunStep(ctx, step, Env{RunGit: spy.run})
		if res.State == StepReported || res.State == StepVerified {
			t.Fatalf("%s: %+v, a delete without an expected tip must never succeed", action, res)
		}
	}
	for _, c := range spy.calls {
		if j := strings.Join(c, " "); strings.Contains(j, "update-ref") || strings.Contains(j, "push") {
			t.Errorf("a delete command ran without an expected tip: %s", c)
		}
	}
}
