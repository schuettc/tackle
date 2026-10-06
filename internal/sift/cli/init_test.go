package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
)

func termSeam(t *testing.T, term bool) {
	t.Helper()
	old := isTerminal
	isTerminal = func(any) bool { return term }
	t.Cleanup(func() { isTerminal = old })
}

func TestInitDetectsHarnessesAndWritesTheConfig(t *testing.T) {
	home := siftEnv(t)
	termSeam(t, false)
	for _, d := range []string{".claude", ".pi/agent"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	code, out, errw := run(t, "", "init", "--yes", "--root", root, "--root", "~/work")
	if code != 0 {
		t.Fatalf("code %d: %s %s", code, out, errw)
	}
	c, err := config.Load(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Profiles, []string{"claude-code", "pi"}) {
		t.Errorf("profiles %v", c.Profiles)
	}
	want := []config.Root{{Path: root}, {Path: filepath.Join(home, "work")}}
	if !reflect.DeepEqual(c.Roots, want) {
		t.Errorf("roots %+v", c.Roots)
	}
	if c.Budgets != config.Default().Budgets {
		t.Errorf("budgets %+v", c.Budgets)
	}
	// The pattern lists are left to the shipped defaults, so a better default
	// reaches an existing config.
	body, _ := os.ReadFile(config.Path())
	if strings.Contains(string(body), "patterns") || strings.Contains(string(body), "phrases") {
		t.Errorf("config pins the default patterns:\n%s", body)
	}
	if !reflect.DeepEqual(c.Stale, config.Default().Stale) {
		t.Errorf("patterns not the defaults: %+v", c.Stale)
	}
	for _, w := range []string{`Claude Code:\s+found`, `Codex:\s+not found`, `pi:\s+found`, regexp.QuoteMeta(root), regexp.QuoteMeta(config.Path())} {
		if !regexp.MustCompile(w).MatchString(out) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// Without --yes, init asks; with no terminal to ask on, it refuses.
func TestInitAsksOrRefuses(t *testing.T) {
	siftEnv(t)
	termSeam(t, false)
	if code, _, errw := run(t, "", "init", "--root", t.TempDir()); code != 2 || !strings.Contains(errw, "--yes") {
		t.Fatalf("code %d %q", code, errw)
	}
	termSeam(t, true)
	if code, out, _ := run(t, "n\n", "init", "--root", t.TempDir()); code != 0 || !strings.Contains(out, "not written") {
		t.Fatalf("code %d %q", code, out)
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Fatal("written after no")
	}
	if code, _, errw := run(t, "\n", "init", "--root", t.TempDir()); code != 0 {
		t.Fatalf("code %d %q", code, errw)
	}
	if _, err := os.Stat(config.Path()); err != nil {
		t.Fatal("not written after the default yes")
	}
}

// An existing config is kept unless --force.
func TestInitKeepsAnExistingConfig(t *testing.T) {
	siftEnv(t)
	termSeam(t, false)
	first := t.TempDir()
	if code, _, errw := run(t, "", "init", "--yes", "--root", first); code != 0 {
		t.Fatal(errw)
	}
	before, _ := os.ReadFile(config.Path())
	code, out, _ := run(t, "", "init", "--yes", "--root", t.TempDir())
	if code != 0 || !strings.Contains(out, "--force") {
		t.Fatalf("code %d %q", code, out)
	}
	if after, _ := os.ReadFile(config.Path()); string(after) != string(before) {
		t.Fatal("config rewritten without --force")
	}
	second := t.TempDir()
	if code, _, errw := run(t, "", "init", "--yes", "--force", "--root", second); code != 0 {
		t.Fatal(errw)
	}
	c, _ := config.Load(config.Path())
	if len(c.Roots) != 1 || c.Roots[0].Path != second {
		t.Fatalf("roots %+v", c.Roots)
	}
}

// With no --root, init proposes the repo it runs in (else the directory).
func TestInitProposesTheCurrentRepo(t *testing.T) {
	siftEnv(t)
	termSeam(t, false)
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	t.Chdir(sub)
	if code, _, errw := run(t, "", "init", "--yes"); code != 0 {
		t.Fatal(errw)
	}
	c, _ := config.Load(config.Path())
	real, _ := filepath.EvalSymlinks(dir)
	if len(c.Roots) != 1 || (c.Roots[0].Path != dir && c.Roots[0].Path != real) {
		t.Fatalf("roots %+v, want %s", c.Roots, dir)
	}
}

// Run from a git hook, init sees GIT_DIR and the like for another repo; it
// still proposes the repo it runs in.
func TestInitIgnoresRepoSelectingVariables(t *testing.T) {
	siftEnv(t)
	termSeam(t, false)
	other, here := t.TempDir(), t.TempDir()
	gitInit(t, other)
	gitInit(t, here)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	t.Chdir(here)
	if code, _, errw := run(t, "", "init", "--yes"); code != 0 {
		t.Fatal(errw)
	}
	c, _ := config.Load(config.Path())
	real, _ := filepath.EvalSymlinks(here)
	if len(c.Roots) != 1 || (c.Roots[0].Path != here && c.Roots[0].Path != real) {
		t.Fatalf("roots %+v, want %s", c.Roots, here)
	}
}
