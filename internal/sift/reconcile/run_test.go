package reconcile_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/reconcile"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

// End to end: apply writes the branch, a writer then narrows one row and
// adds a line nobody approved next to it, and reconcile reports both; an approved row
// apply could not write is missing.
func TestRunAfterApply(t *testing.T) {
	ctx := context.Background()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\n- Never push to main.\n- Never force-push.\n"})
	st.Publish(t, repo)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	src := func(start int) row.Source {
		return row.Source{File: filepath.Join(repo, "CLAUDE.md"), Repo: repo, Ref: "origin/main", Path: "CLAUDE.md", Start: start, End: start}
	}
	rows := []row.Row{
		{ID: "a", Check: "negative-rule", Source: src(3), Passage: "- Never push to main.", Verdict: "rewrite", Text: "- Push to a branch and open a pull request."},
		{ID: "b", Check: "negative-rule", Source: src(4), Passage: "- Never force-push.", Verdict: "delete"},
		{ID: "c", Check: "negative-rule", Source: src(9), Passage: "- Never sleep.", Verdict: "delete"},
	}
	round, err := s.RecordRound(ctx, store.Round{Kind: "on-demand"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		_ = s.Decide(ctx, round, r.ID, row.Decision{Action: "accept"})
	}
	if _, err := s.Send(ctx, round, ""); err != nil {
		t.Fatal(err)
	}
	res, err := apply.Run(ctx, apply.Options{Store: s, WorktreeDir: t.TempDir()})
	if err != nil || res.Repos[0].State != "branch" {
		t.Fatalf("%+v %v", res, err)
	}
	_, reps, err := reconcile.Run(ctx, s, 0)
	if err != nil || len(reps) != 1 || reps[0].Problems() != 1 || reps[0].Rows[2].State != "missing" {
		t.Fatalf("straight after apply: %+v %v", reps, err)
	}
	// A writer narrows a and adds a line.
	wt := filepath.Join(t.TempDir(), "wt")
	st.Git(t, repo, "worktree", "add", "-q", wt, apply.Branch(round))
	st.Commit(t, wt, map[string]string{"CLAUDE.md": "# App\n\n- Push to a branch.\n- Ask before you merge.\n"})
	_, reps, err = reconcile.Run(ctx, s, round)
	if err != nil {
		t.Fatal(err)
	}
	r := reps[0]
	if r.Rows[0].State != "narrowed" || r.Rows[1].State != "ok" || r.Rows[2].State != "missing" ||
		len(r.Extra) != 1 || r.Extra[0].Lines[0] != "+- Ask before you merge." {
		t.Fatalf("%+v", r)
	}
}
