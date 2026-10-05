package reconcile_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/reconcile"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/serve"
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

// Applying a round twice is held the second time, and that attempt never
// erases the first one's success: undo still says "already applied" and
// reconcile still checks the branch.
func TestApplyTwiceKeepsTheSuccess(t *testing.T) {
	ctx := context.Background()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\nSee `docs/gone.md`.\n- Never force-push.\n"})
	st.Publish(t, repo)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	src := func(start int) row.Source {
		return row.Source{File: filepath.Join(repo, "CLAUDE.md"), Repo: repo, Ref: "origin/main", Path: "CLAUDE.md", Start: start, End: start}
	}
	round, err := s.RecordRound(ctx, store.Round{Kind: "on-demand"}, []row.Row{
		{ID: "dead", Check: "dead-path", Certain: true, Source: src(3), Passage: "See `docs/gone.md`."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, round, ""); err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"branch", "held"} {
		res, err := apply.Run(ctx, apply.Options{Store: s, WorktreeDir: t.TempDir()})
		if err != nil || res.Repos[0].State != want {
			t.Fatalf("apply %d: %+v %v", i+1, res, err)
		}
	}
	as, err := s.Applies(ctx, round)
	if err != nil || len(as) != 1 || as[0].State != "branch" || as[0].Branch != apply.Branch(round) || len(as[0].Rows) != 1 ||
		as[0].Last.State != "held" || !strings.Contains(as[0].Last.Detail, "already exists") {
		t.Fatalf("applies %+v %v", as, err)
	}
	h := serve.New(s).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/undo", strings.NewReader(fmt.Sprintf(`{"round":%d,"id":"dead"}`, round))))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "already applied") {
		t.Fatalf("undo after two applies: %d %s", w.Code, w.Body)
	}
	_, reps, err := reconcile.Run(ctx, s, round)
	if err != nil || len(reps) != 1 || reps[0].Branch != apply.Branch(round) || reps[0].Problems() != 0 {
		t.Fatalf("reconcile %+v %v", reps, err)
	}
}

// reconcile approves what apply approves, with apply's path rules: a move
// to a section the file lacks (apply adds its heading) and a move to an
// absolute destination inside the repo reconcile clean; a decision made
// after the send is not expected on the branch.
func TestReconcileSharesApplysRules(t *testing.T) {
	ctx := context.Background()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{
		"CLAUDE.md":     "# App\n\n- Run the slow suite.\n- Use the staging bucket.\n- Never force-push.\n",
		"docs/other.md": "# Other\n\n## Notes\n\n- one\n",
	})
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
		{ID: "new", Check: "misplaced", Source: src(3), Passage: "- Run the slow suite.", Verdict: "move", Destination: "docs/other.md#Testing"},
		{ID: "abs", Check: "misplaced", Source: src(4), Passage: "- Use the staging bucket.", Verdict: "move", Destination: filepath.Join(repo, "docs", "other.md") + "#Notes"},
		{ID: "late", Check: "negative-rule", Source: src(5), Passage: "- Never force-push.", Verdict: "delete"},
	}
	round, err := s.RecordRound(ctx, store.Round{Kind: "on-demand"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Decide(ctx, round, "new", row.Decision{Action: "accept"})
	_ = s.Decide(ctx, round, "abs", row.Decision{Action: "accept"})
	if _, err := s.Send(ctx, round, ""); err != nil {
		t.Fatal(err)
	}
	_ = s.Decide(ctx, round, "late", row.Decision{Action: "accept"}) // after the send
	res, err := apply.Run(ctx, apply.Options{Store: s, WorktreeDir: t.TempDir()})
	if err != nil || res.Repos[0].State != "branch" || len(res.Repos[0].Applied) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	_, reps, err := reconcile.Run(ctx, s, round)
	if err != nil || len(reps) != 1 {
		t.Fatalf("%+v %v", reps, err)
	}
	if r := reps[0]; r.Problems() != 0 || len(r.Rows) != 2 {
		t.Fatalf("rows %+v extra %+v", r.Rows, r.Extra)
	}
}
