package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/observe"
	"github.com/schuettc/tackle/internal/casebook/store"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// gitSpy wraps real git so tests record command order without a general shell,
// and can intercept the destructive command (record-only, no effect) to prove
// verification catches drift. Reads always pass through to real git.
type gitSpy struct {
	calls                    [][]string
	casebook                 string // casebook-data dir; checked at delete time
	restoreCommittedAtDelete int    // -1 unknown, 0 no, 1 yes
	intercept                bool   // when true, the destructive command is a no-op
}

func newSpy() *gitSpy { return &gitSpy{restoreCommittedAtDelete: -1} }

// laneJob builds an approved job holding the given casebook-lane steps.
func laneJob(t *testing.T, ctx context.Context, steps ...JobStep) (*Store, Job) {
	t.Helper()
	s := NewStore(openTestDB(t))
	ps := make([]Step, len(steps))
	for i, js := range steps {
		ps[i] = Step{
			Key:          js.Key,
			Action:       js.Action,
			Lane:         js.Lane,
			Command:      js.Command,
			Precondition: js.Precondition,
			Posts:        js.Posts,
			ExpectedTip:  js.ExpectedTip,
		}
	}
	job, err := s.Create(ctx, Plan{BuiltAt: time.Unix(0, 0), Steps: ps}, "mbp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, job.ID, ""); err != nil {
		t.Fatal(err)
	}
	return s, job
}

func isDestructive(args []string) bool {
	switch {
	case len(args) >= 2 && args[0] == "branch" && args[1] == "-D":
		return true
	case len(args) >= 1 && args[0] == "push":
		for _, a := range args {
			if a == "--delete" {
				return true
			}
		}
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "remove":
		return true
	}
	return false
}

func (g *gitSpy) run(ctx context.Context, dir string, args ...string) (string, error) {
	g.calls = append(g.calls, append([]string{dir}, args...))
	if isDestructive(args) {
		if g.casebook != "" {
			out, _ := gitx.Run(ctx, g.casebook, "log", "--oneline")
			if strings.Contains(out, "restore record") {
				g.restoreCommittedAtDelete = 1
			} else {
				g.restoreCommittedAtDelete = 0
			}
		}
		if g.intercept {
			return "", nil
		}
	}
	return gitx.Run(ctx, dir, args...)
}

func (g *gitSpy) ran(sub string) bool {
	for _, c := range g.calls {
		if strings.Contains(strings.Join(c, " "), sub) {
			return true
		}
	}
	return false
}

// casebookRepo returns a fresh, empty git repo to hold restore records.
func casebookRepo(t *testing.T) *store.Repo {
	t.Helper()
	return &store.Repo{Dir: testgit.NewRepo(t)}
}

// landedClone makes a clone on main@c1 with feat/x pointing at c1 (so its tip
// is an ancestor of main = landed via the default branch). It returns the clone
// path and feat/x's tip.
func landedClone(t *testing.T) (clone, tip string) {
	t.Helper()
	clone = testgit.NewRepo(t)
	testgit.Git(t, clone, "branch", "feat/x", "main")
	tip = testgit.Git(t, clone, "rev-parse", "refs/heads/feat/x")
	return clone, tip
}

func branchExists(t *testing.T, clone, branch string) bool {
	t.Helper()
	_, err := gitx.Run(context.Background(), clone, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

func TestPreconditionFailSkipsAndReturnsToAttention(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, oldTip := landedClone(t)
	// The branch moves after the snapshot's tip was recorded.
	testgit.Git(t, clone, "switch", "-q", "feat/x")
	newTip := testgit.Commit(t, clone, "moved", "x")
	if newTip == oldTip {
		t.Fatal("tip did not move")
	}
	spy := newSpy()
	cb := casebookRepo(t)
	spy.casebook = cb.Dir
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Lane:         LaneCasebook,
		Command:      branchDeleteLocalCmd(clone, "feat/x"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  oldTip,
	}
	res, err := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StepSkipped {
		t.Fatalf("state = %q, want skipped (reason %q)", res.State, res.Detail)
	}
	if spy.ran("branch -D") {
		t.Error("branch -D ran despite precondition failure")
	}
	if _, err := os.Stat(filepath.Join(cb.Dir, "restores")); err == nil {
		t.Error("a restore record was written on a skipped step")
	}
	if !branchExists(t, clone, "feat/x") {
		t.Error("branch was deleted on a skipped step")
	}
}

func TestPreconditionReadsTheRepoNotTheSnapshot(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, snapTip := landedClone(t)
	// A new commit moved the branch to Y after the snapshot said X.
	testgit.Git(t, clone, "switch", "-q", "feat/x")
	testgit.Commit(t, clone, "y", "y")
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(clone, "feat/x"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  snapTip,
	}
	res, _ := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if res.State != StepSkipped {
		t.Fatalf("state = %q, want skipped", res.State)
	}
	if !strings.Contains(res.Detail, "tip moved") {
		t.Errorf("reason = %q, want tip-moved", res.Detail)
	}
}

func TestRestoreRecordCommittedBeforeStep(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, tip := landedClone(t)
	spy := newSpy()
	cb := casebookRepo(t)
	spy.casebook = cb.Dir
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(clone, "feat/x"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	}
	res, err := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StepReported {
		t.Fatalf("state = %q, want reported (detail %q)", res.State, res.Detail)
	}
	if spy.restoreCommittedAtDelete != 1 {
		t.Errorf("restore record was not committed before branch -D (flag=%d)", spy.restoreCommittedAtDelete)
	}
	if !spy.ran("branch -D") {
		t.Error("branch -D never ran")
	}
	if branchExists(t, clone, "feat/x") {
		t.Error("branch still exists after delete")
	}
	// The restore command is the deterministic recreate.
	want := "git -C '" + clone + "' branch 'feat/x' " + tip
	if res.Restore != want {
		t.Errorf("restore = %q, want %q", res.Restore, want)
	}
	// The restore file is present and committed.
	b, err := os.ReadFile(filepath.Join(cb.Dir, "restores", "2026-09-27.tsv"))
	if err != nil {
		t.Fatalf("restore file: %v", err)
	}
	if !strings.HasPrefix(string(b), "key\taction\tbefore\trestore-command\n") {
		t.Error("restore file missing header")
	}
	if !strings.Contains(string(b), "branch 'feat/x' "+tip) {
		t.Errorf("restore file missing command: %s", b)
	}
}

func TestStepAbortedWhenRestoreCommitFails(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, tip := landedClone(t)
	spy := newSpy()
	// A casebook repo that is not a git repo: the restore commit fails.
	cb := &store.Repo{Dir: t.TempDir()}
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(clone, "feat/x"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	}
	res, err := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StepFailed {
		t.Fatalf("state = %q, want failed", res.State)
	}
	if spy.ran("branch -D") {
		t.Error("branch -D ran even though the restore commit failed")
	}
	if !branchExists(t, clone, "feat/x") {
		t.Error("branch was deleted despite the restore commit failure")
	}
}

func TestBranchDeletedThenVerifiedByObservation(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, tip := landedClone(t)
	spy := newSpy()
	cb := casebookRepo(t)
	s, job := laneJob(t, ctx, JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Lane:         LaneCasebook,
		Command:      branchDeleteLocalCmd(clone, "feat/x"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	})
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	if err := s.RunCasebookLane(ctx, job, r, Env{RunGit: spy.run}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[0].State != StepVerified {
		t.Fatalf("step state = %q, want verified", got.Steps[0].State)
	}
	if branchExists(t, clone, "feat/x") {
		t.Error("branch still exists after a verified delete")
	}
}

func TestVerificationMismatchIsDrift(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone, tip := landedClone(t)
	spy := newSpy()
	spy.intercept = true // the delete "runs" but has no effect
	cb := casebookRepo(t)
	s, job := laneJob(t, ctx, JobStep{
		Key:          "branch:schuettc/hail@feat/x",
		Action:       "branch-delete-local",
		Lane:         LaneCasebook,
		Command:      branchDeleteLocalCmd(clone, "feat/x"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	})
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	if err := s.RunCasebookLane(ctx, job, r, Env{RunGit: spy.run}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.Steps[0].State != StepFailed {
		t.Fatalf("step state = %q, want failed (drift)", got.Steps[0].State)
	}
	if got.Steps[0].Detail != "drift" {
		t.Errorf("detail = %q, want drift", got.Steps[0].Detail)
	}
	if !branchExists(t, clone, "feat/x") {
		t.Error("branch should still exist (intercepted delete)")
	}
}

func TestWorktreeRemoveRequiresClean(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone := testgit.NewRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	testgit.Git(t, clone, "worktree", "add", "-q", wt, "-b", "wtbranch")
	// Dirty it with an untracked file.
	if err := os.WriteFile(filepath.Join(wt, "junk"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "worktree:mbp:" + wt,
		Action:       "worktree-remove",
		Command:      worktreeRemoveCmd(clone, wt),
		Precondition: "worktree-clean",
	}
	res, _ := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if res.State != StepSkipped {
		t.Fatalf("state = %q, want skipped", res.State)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Error("worktree was removed despite being dirty")
	}
}

func TestWorktreeRemoveSkipsWhenLocked(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone := testgit.NewRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	testgit.Git(t, clone, "worktree", "add", "-q", wt, "-b", "wtbranch")
	testgit.Git(t, clone, "worktree", "lock", wt)
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "worktree:mbp:" + wt,
		Action:       "worktree-remove",
		Command:      worktreeRemoveCmd(clone, wt),
		Precondition: "worktree-clean",
	}
	res, _ := r.RunStep(ctx, step, Env{RunGit: spy.run})
	if res.State != StepSkipped {
		t.Fatalf("state = %q, want skipped", res.State)
	}
	if !strings.Contains(res.Detail, "locked") {
		t.Errorf("reason = %q, want locked", res.Detail)
	}
}

func TestWorktreeRemovedThenVerified(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone := testgit.NewRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	testgit.Git(t, clone, "worktree", "add", "-q", wt, "-b", "wtbranch")
	spy := newSpy()
	cb := casebookRepo(t)
	s, job := laneJob(t, ctx, JobStep{
		Key:          "worktree:mbp:" + wt,
		Action:       "worktree-remove",
		Lane:         LaneCasebook,
		Command:      worktreeRemoveCmd(clone, wt),
		Precondition: "worktree-clean",
	})
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	if err := s.RunCasebookLane(ctx, job, r, Env{RunGit: spy.run}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, job.ID)
	if got.Steps[0].State != StepVerified {
		t.Fatalf("step state = %q, want verified", got.Steps[0].State)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Error("worktree still present after verified remove")
	}
	if !strings.Contains(got.Steps[0].Restore, "worktree add") {
		t.Errorf("restore = %q, want worktree add", got.Steps[0].Restore)
	}
}

func TestMergedPRListUnavailableIsNotOK(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone := testgit.NewRepo(t)
	// feat/y has its own commit, so it is NOT an ancestor of main.
	testgit.Git(t, clone, "switch", "-q", "-c", "feat/y")
	tip := testgit.Commit(t, clone, "z", "z")
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/y",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(clone, "feat/y"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	}
	// MergedPRs returns not-fetched: the landed check cannot be answered.
	env := Env{
		RunGit:    spy.run,
		MergedPRs: func(string) (observe.MergedPRList, bool) { return observe.MergedPRList{Fetched: false}, true },
	}
	res, _ := r.RunStep(ctx, step, env)
	if res.State != StepSkipped {
		t.Fatalf("state = %q, want skipped", res.State)
	}
	if !strings.Contains(res.Detail, "merged-PR list unavailable") {
		t.Errorf("reason = %q, want merged-PR list unavailable", res.Detail)
	}
	if spy.ran("branch -D") {
		t.Error("branch -D ran when landed could not be verified")
	}
}

func TestBranchLandedViaMergedPR(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	clone := testgit.NewRepo(t)
	testgit.Git(t, clone, "switch", "-q", "-c", "feat/y")
	tip := testgit.Commit(t, clone, "z", "z")
	testgit.Git(t, clone, "switch", "-q", "main")
	spy := newSpy()
	cb := casebookRepo(t)
	r := Runner{Repo: cb, RunGit: spy.run, Now: func() time.Time { return time.Unix(0, 0) }}
	step := JobStep{
		Key:          "branch:schuettc/hail@feat/y",
		Action:       "branch-delete-local",
		Command:      branchDeleteLocalCmd(clone, "feat/y"),
		Precondition: "branch-tip-unchanged-and-landed",
		ExpectedTip:  tip,
	}
	env := Env{
		RunGit: spy.run,
		MergedPRs: func(string) (observe.MergedPRList, bool) {
			return observe.MergedPRList{Fetched: true, PRs: []observe.MergedPR{
				{Number: 9, HeadRefName: "feat/y", HeadRefOid: tip},
			}}, true
		},
	}
	res, _ := r.RunStep(ctx, step, env)
	if res.State != StepReported {
		t.Fatalf("state = %q, want reported (detail %q)", res.State, res.Detail)
	}
	if branchExists(t, clone, "feat/y") {
		t.Error("branch not deleted after landed-via-merged-PR")
	}
}
