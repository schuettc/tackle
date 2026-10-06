package clean

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

var ctx = context.Background()

// rig is a published repo with round branches pushed, and a store.
type rig struct {
	t    *testing.T
	repo string
	bare string
	s    *store.Store
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st.Env(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n"})
	bare := st.Publish(t, repo)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &rig{t: t, repo: repo, bare: bare, s: s}
}

// branch makes a branch off main with one commit, pushes it, and returns
// its commit.
func (g *rig) branch(name string) string {
	g.t.Helper()
	st.Git(g.t, g.repo, "checkout", "-q", "-b", name, "main")
	st.Commit(g.t, g.repo, map[string]string{"CLAUDE.md": "# App\n\n" + name + "\n"})
	st.Git(g.t, g.repo, "push", "-q", "origin", name)
	st.Git(g.t, g.repo, "checkout", "-q", "main")
	return st.Git(g.t, g.repo, "rev-parse", name)
}

// record records a branch as apply does.
func (g *rig) record(name, sha, pr string) {
	g.t.Helper()
	if _, err := g.s.AddCreated(ctx, store.Created{Kind: "branch", Round: 1, Repo: g.repo, Name: name, Base: "origin/main", Commit: sha, Pushed: true, PR: pr}); err != nil {
		g.t.Fatal(err)
	}
}

func (g *rig) has(ref string) bool {
	g.t.Helper()
	_, err := git(ctx, g.repo, "rev-parse", "--verify", "-q", ref)
	return err == nil
}

func (g *rig) remoteHas(name string) bool {
	g.t.Helper()
	_, err := git(ctx, g.bare, "rev-parse", "--verify", "-q", "refs/heads/"+name)
	return err == nil
}

// gh answers gh pr view with each pull request's state.
func gh(states map[string]string) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "pr" && args[1] == "view" {
			if s, ok := states[args[2]]; ok {
				return []byte(s + "\n"), nil
			}
		}
		return nil, errors.New("unexpected gh " + strings.Join(args, " "))
	}
}

func find(steps []Step, name string) *Step {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

// clean removes exactly what apply recorded creating: a branch whose pull
// request merged goes, local and remote, even past a failing pre-push
// hook; one whose pull request is open stays; a sift/ branch nobody
// recorded is never touched; a recorded leftover worktree goes. A dry run
// plans the same and changes nothing.
func TestCleanRemovesWhatWasRecordedOnce(t *testing.T) {
	g := newRig(t)
	g.record("sift/round-1", g.branch("sift/round-1"), "https://github.com/o/app/pull/1")
	g.record("sift/round-2", g.branch("sift/round-2"), "https://github.com/o/app/pull/2")
	g.branch("sift/round-9") // unrecorded
	wt := filepath.Join(t.TempDir(), "leftover")
	st.Git(t, g.repo, "worktree", "add", "-q", "--detach", wt, "main")
	if _, err := g.s.AddCreated(ctx, store.Created{Kind: "worktree", Round: 1, Repo: g.repo, Name: wt}); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(g.repo, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho slow gate >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Store: g.s, Gh: gh(map[string]string{"https://github.com/o/app/pull/1": "MERGED", "https://github.com/o/app/pull/2": "OPEN"})}

	dry := o
	dry.DryRun = true
	steps, err := Run(ctx, dry)
	if err != nil {
		t.Fatal(err)
	}
	if s := find(steps, "sift/round-1"); s == nil || s.Action != "remove" || !s.Local || !s.Remote || s.Done {
		t.Errorf("round-1 %+v", s)
	}
	if s := find(steps, "sift/round-2"); s == nil || s.Action != "keep" || !strings.Contains(s.Why, "open") {
		t.Errorf("round-2 %+v", s)
	}
	if s := find(steps, wt); s == nil || s.Action != "remove" || s.Done {
		t.Errorf("worktree %+v", s)
	}
	if find(steps, "sift/round-9") != nil {
		t.Error("an unrecorded branch is in the plan")
	}
	if !g.has("refs/heads/sift/round-1") || !g.remoteHas("sift/round-1") {
		t.Fatal("the dry run removed round-1")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("the dry run removed the worktree")
	}
	if made, _ := g.s.Created(ctx); len(made) != 3 {
		t.Fatalf("the dry run changed the records: %+v", made)
	}

	steps, err = Run(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if s := find(steps, "sift/round-1"); s == nil || !s.Done || s.Error != "" {
		t.Fatalf("round-1 %+v", s)
	}
	if g.has("refs/heads/sift/round-1") || g.remoteHas("sift/round-1") {
		t.Error("round-1 is still there")
	}
	if !g.has("refs/heads/sift/round-2") || !g.remoteHas("sift/round-2") {
		t.Error("round-2, still open, was removed")
	}
	if !g.has("refs/heads/sift/round-9") || !g.remoteHas("sift/round-9") {
		t.Error("the unrecorded round-9 was touched")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	made, _ := g.s.Created(ctx)
	if len(made) != 1 || made[0].Name != "sift/round-2" {
		t.Errorf("left on record %+v", made)
	}
}

// Without gh, a branch goes only once its commit is in the base.
func TestCleanWithoutGhRemovesOnlyWhatTheBaseContains(t *testing.T) {
	g := newRig(t)
	in := g.branch("sift/round-3")
	g.record("sift/round-3", in, "https://github.com/o/app/pull/3")
	st.Git(t, g.repo, "merge", "-q", "--ff-only", "sift/round-3")
	st.Git(t, g.repo, "push", "-q", "origin", "main")
	g.record("sift/round-4", g.branch("sift/round-4"), "")
	steps, err := Run(ctx, Options{Store: g.s})
	if err != nil {
		t.Fatal(err)
	}
	if s := find(steps, "sift/round-3"); s == nil || !s.Done || g.has("refs/heads/sift/round-3") || g.remoteHas("sift/round-3") {
		t.Errorf("round-3 %+v", s)
	}
	if s := find(steps, "sift/round-4"); s == nil || s.Action != "keep" || !g.has("refs/heads/sift/round-4") || !g.remoteHas("sift/round-4") {
		t.Errorf("round-4 %+v", s)
	}
}

// A recorded branch that is gone already (deleted by hand) is crossed off
// the record, and a branch name reused for another commit is not deleted.
func TestCleanCrossesOffWhatIsGoneAndKeepsAReusedName(t *testing.T) {
	g := newRig(t)
	sha := g.branch("sift/round-5")
	g.record("sift/round-5", sha, "https://github.com/o/app/pull/5")
	st.Git(t, g.repo, "branch", "-q", "-D", "sift/round-5")
	st.Git(t, g.repo, "push", "-q", "origin", "--delete", "sift/round-5")
	g.record("sift/round-6", "0123456789012345678901234567890123456789", "https://github.com/o/app/pull/6")
	g.branch("sift/round-6")
	steps, err := Run(ctx, Options{Store: g.s, Gh: gh(map[string]string{"https://github.com/o/app/pull/5": "MERGED", "https://github.com/o/app/pull/6": "CLOSED"})})
	if err != nil {
		t.Fatal(err)
	}
	if s := find(steps, "sift/round-5"); s == nil || !s.Done || s.Local || s.Remote {
		t.Errorf("round-5 %+v", s)
	}
	if !g.has("refs/heads/sift/round-6") || !g.remoteHas("sift/round-6") {
		t.Error("a branch at another commit than the recorded one was removed")
	}
	made, _ := g.s.Created(ctx)
	if len(made) != 1 || made[0].Name != "sift/round-6" {
		t.Errorf("left on record %+v", made)
	}
}
