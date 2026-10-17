package engine

import "testing"

// TestFieldsWorktreeAndRelation: a worktree item reports its own worktree
// state, a branch its worktree's; a repo's relation is empty for matching
// (repos are matched with fork), while the item keeps its Relation.
func TestFieldsWorktreeAndRelation(t *testing.T) {
	r := Build(fixture())
	if got := mustFind(t, r, "worktree:mbp:/w/hail-client").Fields(now).Worktree; got != "dirty" {
		t.Errorf("dirty worktree item: worktree = %q, want dirty", got)
	}
	if got := mustFind(t, r, "branch:schuettc/hail@feat/client").Fields(now).Worktree; got != "dirty" {
		t.Errorf("branch with a dirty worktree: worktree = %q, want dirty", got)
	}
	if got := mustFind(t, r, "branch:schuettc/hail@fix/landed").Fields(now).Worktree; got != "none" {
		t.Errorf("branch with no worktree: worktree = %q, want none", got)
	}
	in := fixture()
	in.Snapshots[0].Clones[0].Worktrees[0].Dirty = false
	if got := mustFind(t, Build(in), "worktree:mbp:/w/hail-client").Fields(now).Worktree; got != "clean" {
		t.Errorf("clean worktree item: worktree = %q, want clean", got)
	}
	fork := mustFind(t, r, "repo:schuettc/pi-usage")
	if f := fork.Fields(now); f.Relation != "" || !f.Fork {
		t.Errorf("fork repo fields: relation %q fork %v, want empty and true", f.Relation, f.Fork)
	}
	if fork.Relation == "" {
		t.Error("the repo item itself should keep its relation")
	}
	if got := mustFind(t, r, "pr:schuettc/hail#3").Fields(now).Relation; got != "incoming" {
		t.Errorf("incoming PR relation = %q", got)
	}
}
