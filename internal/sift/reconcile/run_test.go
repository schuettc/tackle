package reconcile_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/reconcile"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

var ctx = context.Background()

// applied is a round whose two files (app's CLAUDE.md and docs/AGENTS.md)
// were accepted, sent and applied: the store, the round, the repo, the
// recommendations and the branch.
func applied(t *testing.T) (*store.Store, int64, string, []rec.Rec, string) {
	t.Helper()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\n- Never push to main.\n", "docs/AGENTS.md": "# Docs\n\n- Don't guess.\n"})
	st.Publish(t, repo)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	commit := st.Git(t, repo, "rev-parse", "origin/main")
	var files []rec.File
	var rows []row.Row
	for rel, body := range map[string]string{"CLAUDE.md": "# App\n\n- Never push to main.\n", "docs/AGENTS.md": "# Docs\n\n- Don't guess.\n"} {
		f := rec.NewFile(row.Source{File: filepath.Join(repo, rel), Repo: repo, Ref: "origin/main", Path: rel}, "repo", 6000, body)
		f.Commit, f.Rows = commit, []string{"n-" + rel}
		files = append(files, f)
		rows = append(rows, row.Row{ID: "n-" + rel, Check: "stale-status", Source: f.Source})
	}
	round, err := s.RecordAudit(ctx, store.Round{Kind: "on-demand"}, rows, files)
	if err != nil {
		t.Fatal(err)
	}
	var recs []rec.Rec
	for _, f := range files {
		recs = append(recs, rec.Rec{File: f.Key, Base: f.Base, Content: "# Rewritten " + f.Source.Path + "\n\n- Do it right.\n", Summary: "s",
			Findings: []rec.Account{{Row: f.Rows[0], Did: "fixed", How: "removed"}}})
	}
	if _, err := s.Propose(ctx, round, recs); err != nil {
		t.Fatal(err)
	}
	items, _ := s.Files(ctx, round)
	for _, it := range items {
		if _, err := s.DecideFile(ctx, round, it.Key, rec.Decision{Action: "accept"}, map[string]store.Seen{it.Key: {Fingerprint: it.Fingerprint}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Send(ctx, round, "", nil); err != nil {
		t.Fatal(err)
	}
	res, err := apply.Run(ctx, apply.Options{Store: s, WorktreeDir: t.TempDir()})
	if err != nil || res.Repos[0].State != "branch" {
		t.Fatalf("%+v %v", res, err)
	}
	return s, round, repo, recs, res.Repos[0].Branch
}

// End to end: apply writes two approved files; reconcile finds each equal
// to its approved content. A writer then changes one file by a character,
// deletes the other and adds a file nobody approved, and reconcile reports
// each.
func TestReconcileAfterApply(t *testing.T) {
	s, round, repo, _, branch := applied(t)
	_, reps, err := reconcile.Run(ctx, s, 0)
	if err != nil || len(reps) != 1 || reps[0].Problems() != 0 || len(reps[0].Files) != 2 || reps[0].Branch != branch {
		t.Fatalf("straight after apply: %+v %v", reps, err)
	}

	// A writer changes the branch.
	wt := filepath.Join(t.TempDir(), "wt")
	st.Git(t, repo, "worktree", "add", "-q", wt, branch)
	st.Write(t, wt, "CLAUDE.md", "# Rewritten CLAUDE.md\n\n- Do it right!\n")
	st.Git(t, wt, "rm", "-q", "docs/AGENTS.md")
	st.Write(t, wt, "NOTES.md", "extra\n")
	st.Git(t, wt, "add", "-A")
	st.Git(t, wt, "commit", "-q", "-m", "writer")
	_, reps, err = reconcile.Run(ctx, s, round)
	if err != nil || len(reps) != 1 {
		t.Fatalf("%+v %v", reps, err)
	}
	got := map[string]string{}
	for _, f := range reps[0].Files {
		got[f.Path] = f.State + " " + f.Detail
	}
	if !strings.HasPrefix(got["CLAUDE.md"], "changed") || !strings.Contains(got["CLAUDE.md"], "line 3") {
		t.Errorf("CLAUDE.md: %q", got["CLAUDE.md"])
	}
	if !strings.HasPrefix(got["docs/AGENTS.md"], "missing") {
		t.Errorf("docs/AGENTS.md: %q", got["docs/AGENTS.md"])
	}
	if strings.Join(reps[0].Extra, ",") != "NOTES.md" || reps[0].Problems() != 3 {
		t.Errorf("extra %v, problems %d", reps[0].Extra, reps[0].Problems())
	}
}

// A file apply has written keeps the approval it was written with: a new
// recommendation for it is refused, so reconcile still checks the branch
// against what was approved.
func TestReproposingAnAppliedFileIsRefused(t *testing.T) {
	s, round, _, recs, _ := applied(t)
	again := recs[0]
	again.Content = "# Something else\n"
	if _, err := s.Propose(ctx, round, []rec.Rec{again}); err == nil || !strings.Contains(err.Error(), "applied") {
		t.Fatalf("re-proposal after apply: %v", err)
	}
	_, reps, err := reconcile.Run(ctx, s, round)
	if err != nil || len(reps) != 1 || reps[0].Problems() != 0 || len(reps[0].Files) != 2 {
		t.Fatalf("%+v %v", reps, err)
	}
}
