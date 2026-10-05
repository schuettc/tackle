package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
)

// primary is what the primary clone's working tree and index hold, to show
// apply left them alone.
func (g *rig) primary() string {
	g.t.Helper()
	b, err := os.ReadFile(filepath.Join(g.repo, "CLAUDE.md"))
	if err != nil {
		g.t.Fatal(err)
	}
	return st.Git(g.t, g.repo, "status", "--porcelain", "--untracked-files=all") + "\x00" +
		st.Git(g.t, g.repo, "ls-files", "-s") + "\x00" + string(b)
}

// tree lists every file under dir, so a test can see nothing appeared.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// A move's destination, a source path and a merge target are each confined
// to the repo: no "..", nothing under .git, nothing absolute outside it.
// Each such row is skipped with the reason, and nothing outside the
// worktree is written.
func TestApplyRefusesPathsOutsideTheRepo(t *testing.T) {
	g := newRig(t)
	outside := filepath.Join(filepath.Dir(g.repo), "outside.md")
	if err := os.WriteFile(outside, []byte("# Outside\n\n## Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := g.at("src", "intake", 1, "x")
	src.Source.Path = "../outside.md"
	merged := g.at("mtarget", "duplicate", 15, "- Keep the changelog current.")
	merged.Source.Path = "../../elsewhere.md"
	g.record(
		g.at("up", "misplaced", 5, "- Never push to main."),
		g.at("deep", "misplaced", 6, "- Never force-push."),
		g.at("gitdir", "misplaced", 10, "- Run the slow suite before a release."),
		src, merged,
		g.at("dup2", "duplicate", 16, "- Keep the changelog up to date."),
		g.at("ok", "dead-path", 14, "See `docs/gone.md` for the layout."),
	)
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{
		{ID: "up", Verdict: "move", Destination: "../outside.md#Notes"},
		{ID: "deep", Verdict: "move", Destination: "docs/../../../deep.md#Notes"},
		{ID: "gitdir", Verdict: "move", Destination: ".git/config#core"},
		{ID: "src", Verdict: "delete"},
		{ID: "dup2", Verdict: "merge:mtarget", Text: "- Keep the changelog current with every change."},
		{ID: "ok", Verdict: "delete"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"up", "deep", "gitdir", "src", "dup2", "ok"} {
		g.decide(id, row.Decision{Action: "accept"})
	}
	before := g.primary()
	wtDir := t.TempDir()
	res := g.run(Options{WorktreeDir: wtDir})
	r := res.Repos[0]
	if r.State != "branch" || len(r.Applied) != 1 || r.Applied[0].Row != "ok" || len(r.Skipped) != 5 {
		t.Fatalf("%+v", r)
	}
	for _, it := range r.Skipped {
		if !strings.Contains(it.Why, "outside the repo") && !strings.Contains(it.Why, ".git") {
			t.Errorf("%s skipped for %q, want a path refusal", it.Row, it.Why)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "# Outside\n\n## Notes\n" {
		t.Errorf("the outside file changed:\n%s", b)
	}
	if files := tree(t, wtDir); len(files) != 0 {
		t.Errorf("files left beside the worktree: %v", files)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(wtDir), "deep.md")); err == nil {
		t.Error("deep.md was written above the worktree")
	}
	if g.primary() != before {
		t.Error("the primary clone's working tree or index changed")
	}
	if got := st.Git(t, g.repo, "ls-tree", "-r", "--name-only", r.Branch); got != "CLAUDE.md\ndocs/other.md" {
		t.Errorf("branch tree:\n%s", got)
	}
}

// A tracked symlink at the destination, or a tracked symlinked directory on
// the way to it, that points outside the repo is refused: the outside file
// is not written through it.
func TestApplyRefusesSymlinksOutOfTheRepo(t *testing.T) {
	g := newRig(t)
	out := t.TempDir()
	target := filepath.Join(out, "target.md")
	if err := os.WriteFile(target, []byte("# Target\n\n## Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(g.repo, "docs", "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(g.repo, "ext")); err != nil {
		t.Fatal(err)
	}
	st.Git(t, g.repo, "add", "docs/link.md", "ext")
	st.Git(t, g.repo, "commit", "-q", "-m", "links")
	st.Git(t, g.repo, "push", "-q", "origin", "main")
	g.record(
		g.at("leaf", "misplaced", 5, "- Never push to main."),
		g.at("parent", "misplaced", 6, "- Never force-push."),
	)
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{
		{ID: "leaf", Verdict: "move", Destination: "docs/link.md#Notes"},
		{ID: "parent", Verdict: "move", Destination: "ext/notes.md#Notes"},
	}); err != nil {
		t.Fatal(err)
	}
	g.decide("leaf", row.Decision{Action: "accept"})
	g.decide("parent", row.Decision{Action: "accept"})
	before := g.primary()
	r := g.run(Options{}).Repos[0]
	if r.State != "nothing" || len(r.Skipped) != 2 {
		t.Fatalf("%+v", r)
	}
	for _, it := range r.Skipped {
		if !strings.Contains(it.Why, "symlink") {
			t.Errorf("%s skipped for %q, want a symlink refusal", it.Row, it.Why)
		}
	}
	if b, _ := os.ReadFile(target); string(b) != "# Target\n\n## Notes\n" {
		t.Errorf("written through the symlink:\n%s", b)
	}
	if files := tree(t, out); len(files) != 1 {
		t.Errorf("files outside: %v", files)
	}
	if g.primary() != before {
		t.Error("the primary clone's working tree or index changed")
	}
}

// The write itself stays inside the worktree, whatever the plan said: a
// symlinked leaf, a symlinked parent (pointing out, or anywhere inside),
// "..", .git and an absolute path are refused; new directories are made.
func TestWriteFile(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	target := filepath.Join(out, "target.md")
	if err := os.WriteFile(target, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "leaf.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(root, "ext")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "docs"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"leaf.md", "ext/new.md", "ext/sub/new.md", "../up.md", "a/../../up.md", ".git/config", "/abs.md", "alias/inside.md"} {
		if err := writeAt(root, rel, "x\n"); err == nil {
			t.Errorf("%s: written", rel)
		}
		if err := removeAt(root, rel); err == nil && (rel == "leaf.md" || rel == "alias/inside.md") {
			t.Errorf("%s: removed through a symlink", rel)
		}
	}
	if b, _ := os.ReadFile(target); string(b) != "outside\n" {
		t.Errorf("target changed: %q", b)
	}
	if files := tree(t, out); len(files) != 1 {
		t.Errorf("files outside: %v", files)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "inside.md")); err == nil {
		t.Error("written through the alias")
	}
	if err := writeAt(root, "new/dir/file.md", "in\n"); err != nil {
		t.Errorf("a new directory: %v", err)
	}
	if err := writeAt(root, "new/dir/file.md", "again\n"); err != nil {
		t.Errorf("an existing file: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "new", "dir", "file.md")); string(b) != "again\n" {
		t.Errorf("file.md %q", b)
	}
	if err := removeAt(root, "new/dir/file.md"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := removeAt(root, "gone/file.md"); err != nil {
		t.Errorf("remove a missing file: %v", err)
	}
}

func TestRepoPath(t *testing.T) {
	for in, want := range map[string]string{
		"CLAUDE.md": "CLAUDE.md", "docs/./a.md": "docs/a.md", "./x.md": "x.md",
	} {
		if got, err := RepoPath(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", ".", "../a.md", "a/../../b.md", "docs/../../b", ".git/config", "sub/.git/x", "/etc/passwd", ".GIT/config"} {
		if got, err := RepoPath(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

// linkAt replaces dir/leaf, once, with a hard link to to, the moment apply
// has checked dir: a destination that shares its inode with a file outside.
func linkAt(t *testing.T, base, leaf, to string) {
	t.Helper()
	done := false
	afterCheck = func(dir string) {
		if done || filepath.Base(dir) != base {
			return
		}
		done = true
		p := filepath.Join(dir, leaf)
		if err := os.Remove(p); err != nil {
			t.Error(err)
			return
		}
		if err := os.Link(to, p); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterCheck = nil })
}

// A destination hard-linked to a file outside the worktree (a sentinel, or a
// copy of a .git/config) is replaced, not written into: the outside file is
// byte-identical after apply, and the branch holds the new content.
func TestApplyReplacesAHardLinkedDestination(t *testing.T) {
	for name, mk := range map[string]func(g *rig) string{
		"sentinel": func(*rig) string { return "outside\n" },
		"git config": func(g *rig) string {
			b, err := os.ReadFile(filepath.Join(g.repo, ".git", "config"))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := newRig(t)
			out := t.TempDir()
			outFile := filepath.Join(out, "linked")
			want := mk(g)
			if err := os.WriteFile(outFile, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			r := g.at("mv", "misplaced", 5, "- Never push to main.")
			g.record(r)
			if _, err := g.s.AddRows(ctx, g.round, []row.Row{{ID: "mv", Verdict: "move", Destination: "docs/other.md#Notes"}}); err != nil {
				t.Fatal(err)
			}
			g.decide("mv", row.Decision{Action: "accept"})
			linkAt(t, "docs", "other.md", outFile)
			res := g.run(Options{}).Repos[0]
			if res.State != "branch" {
				t.Fatalf("state %q (%s)", res.State, res.Detail)
			}
			if b, err := os.ReadFile(outFile); err != nil || string(b) != want {
				t.Errorf("the outside file changed: %q %v", b, err)
			}
			if got := g.show(res.Branch, "docs/other.md"); !strings.Contains(got, "- Never push to main.") {
				t.Errorf("docs/other.md on the branch:\n%s", got)
			}
		})
	}
}

// writeFile replaces an existing file with a new inode and keeps its mode:
// a hard link to it elsewhere keeps the old bytes.
func TestWriteFileReplacesTheInode(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	outFile := filepath.Join(out, "sentinel")
	if err := os.WriteFile(outFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "f.md")
	if err := os.Link(outFile, dest); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeAt(root, "f.md", "new\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outFile); string(b) != "outside\n" {
		t.Errorf("the linked file changed: %q", b)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new\n" {
		t.Errorf("f.md %q", b)
	}
	if fi, err := os.Stat(dest); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v %v, want 0640", fi.Mode().Perm(), err)
	}
	if files := tree(t, root); len(files) != 1 {
		t.Errorf("files left: %v", files)
	}
}
