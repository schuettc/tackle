package apply_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/audit"
	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/reconcile"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

// A repo whose flow is feat → dev → main has base = "dev" in a [[repo]]:
// the audit reads it at origin/dev, and apply cuts the round's branch from
// origin/dev and opens the pull request against dev, never main.
func TestARepoBaseOverrideIsReadAndAppliedThere(t *testing.T) {
	ctx := context.Background()
	st.Env(t)
	st.Home(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "muster"), map[string]string{"CLAUDE.md": "# Muster\n\n- main's rule\n"})
	bare := st.Publish(t, repo)
	st.Git(t, repo, "checkout", "-q", "-b", "dev")
	st.Commit(t, repo, map[string]string{"CLAUDE.md": "# Muster\n\nSee `docs/gone.md`.\n- dev's rule\n"})
	st.Git(t, repo, "push", "-q", "-u", "origin", "dev")
	st.Git(t, repo, "remote", "set-url", "origin", "https://github.com/owner/muster.git")
	st.Git(t, repo, "config", "url."+bare+".insteadOf", "https://github.com/owner/muster.git")

	cfg := config.Default()
	cfg.Profiles = []string{"claude-code"}
	cfg.Roots = []config.Root{{Path: repo}}
	cfg.Repos = []config.Repo{{Path: repo, Base: "dev"}}
	rep, err := audit.Run(ctx, audit.Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	items, err := s.Files(ctx, rep.Round)
	if err != nil || len(items) != 1 {
		t.Fatalf("files %+v %v", items, err)
	}
	f := items[0]
	if f.Source.Ref != "origin/dev" || f.Base != rec.Hash("# Muster\n\nSee `docs/gone.md`.\n- dev's rule\n") {
		t.Fatalf("read at %s", f.Source.Ref)
	}
	var accounts []rec.Account
	for _, r := range f.Rows {
		accounts = append(accounts, rec.Account{Row: r, Did: "fixed", How: "removed"})
	}
	if _, err := s.Propose(ctx, rep.Round, []rec.Rec{{File: f.Key, Base: f.Base, Content: "# Muster\n\n- dev's rule\n", Summary: "s", Findings: accounts}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, rep.Round, f.Key, rec.Decision{Action: "accept"}, map[string]store.Seen{f.Key: {Fingerprint: printOf(t, s, rep.Round, f.Key)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, rep.Round, "", nil); err != nil {
		t.Fatal(err)
	}
	var create []string
	gh := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		create = args
		return []byte("https://github.com/owner/muster/pull/7\n"), nil
	}
	res, err := apply.Run(ctx, apply.Options{Store: s, Gh: gh, WorktreeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Repos[0]
	if r.State != "pr" || r.Base != "origin/dev" {
		t.Fatalf("%+v", r)
	}
	if i := slices.Index(create, "--base"); i < 0 || create[i+1] != "dev" {
		t.Errorf("gh pr create %q: not against dev", create)
	}
	if got := st.Git(t, bare, "rev-parse", r.Branch+"^"); got != st.Git(t, bare, "rev-parse", "dev") {
		t.Errorf("the branch's parent is %s, not dev", got)
	}
}

// reconcile finds a file apply wrote in the repo its symlink points into on
// that repo's branch, at its path there.
func TestReconcileFindsASymlinkedFileOnItsRepoBranch(t *testing.T) {
	ctx := context.Background()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	dots := st.Repo(t, filepath.Join(t.TempDir(), "dotfiles"), map[string]string{"skills/x/SKILL.md": "# X\n\n- waiting on it\n"})
	st.Publish(t, dots)
	p := filepath.Join(t.TempDir(), "x", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dots, "skills/x/SKILL.md"), p); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f := rec.NewFile(row.Source{File: p, Canon: row.Resolve(p)}, "skill", 10000, "# X\n\n- waiting on it\n")
	f.Rows = []string{"n"}
	round, err := s.RecordAudit(ctx, store.Round{Kind: "on-demand"}, []row.Row{{ID: "n", Check: "stale-status", Source: f.Source}}, []rec.File{f})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Propose(ctx, round, []rec.Rec{{File: f.Key, Base: f.Base, Content: "# X\n", Summary: "s",
		Findings: []rec.Account{{Row: "n", Did: "fixed", How: "removed"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, round, f.Key, rec.Decision{Action: "accept"}, map[string]store.Seen{f.Key: {Fingerprint: printOf(t, s, round, f.Key)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, round, "", nil); err != nil {
		t.Fatal(err)
	}
	res, err := apply.Run(ctx, apply.Options{Store: s, WorktreeDir: t.TempDir()})
	if err != nil || len(res.Repos) != 1 || res.Repos[0].State != "branch" {
		t.Fatalf("%+v %v", res, err)
	}
	_, reps, err := reconcile.Run(ctx, s, round)
	if err != nil || len(reps) != 1 || reps[0].Problems() != 0 || reps[0].Files[0].Path != "skills/x/SKILL.md" {
		t.Fatalf("reconcile %+v %v", reps, err)
	}
}
