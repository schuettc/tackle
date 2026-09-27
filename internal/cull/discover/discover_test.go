package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	_ "github.com/schuettc/tackle/internal/cull/extract/golang"
	_ "github.com/schuettc/tackle/internal/cull/extract/python"
	_ "github.com/schuettc/tackle/internal/cull/extract/ts"
)

// newGitRepo creates a temp git repo with a private global git config so
// tests don't depend on the machine's git identity.
func newGitRepo(t *testing.T) (dir, gitconfig string) {
	t.Helper()
	dir = t.TempDir()
	gitconfig = filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Cull Test\n\temail = cull-test@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, gitconfig, "init", "-q")
	return dir, gitconfig
}

func runGit(t *testing.T, dir, gitconfig string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitconfig, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	p := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSuiteFindsAndExcludes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/calc_test.go", "package pkg\n")
	writeFile(t, root, "pkg/calc.go", "package pkg\n")
	writeFile(t, root, "tests/test_calc.py", "def test_a(): pass\n")
	writeFile(t, root, "test/calc.test.ts", "test('a', () => {});\n")
	writeFile(t, root, "node_modules/pkg/pkg.test.ts", "test('skip', () => {});\n")
	writeFile(t, root, "vendor/pkg/vendor_test.go", "package pkg\n")
	writeFile(t, root, "testdata/fixture_test.go", "package pkg\n")
	writeFile(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	writeFile(t, root, ".worktrees/wt/calc_test.go", "package pkg\n")
	writeFile(t, root, "pkg/excluded_test.go", "package pkg\n")

	got, err := Suite(root, "", []string{"**/excluded_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pkg/calc_test.go",
		"test/calc.test.ts",
		"tests/test_calc.py",
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Suite() = %v, want %v", got, want)
	}
}

func TestSuiteSub(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/calc_test.go", "package pkg\n")
	writeFile(t, root, "other/calc_test.go", "package pkg\n")

	got, err := Suite(root, "pkg", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pkg/calc_test.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Suite() = %v, want %v", got, want)
	}
}

func TestDiffModifiedAndNew(t *testing.T) {
	dir, cfg := newGitRepo(t)

	writeFile(t, dir, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\tx := 1\n\t_ = x\n}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	// Modify a line in the committed test file (line 4).
	writeFile(t, dir, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\tx := 2\n\t_ = x\n}\n")

	// A new file committed after base.
	writeFile(t, dir, "pkg/new_test.go", "package pkg\n\nfunc TestB(t *testing.T) {}\n")
	runGit(t, dir, cfg, "add", "pkg/new_test.go")
	runGit(t, dir, cfg, "commit", "-q", "-m", "add new test")

	// An untracked test file.
	writeFile(t, dir, "pkg/untracked_test.go", "package pkg\n\nfunc TestC(t *testing.T) {}\n")

	changes, err := Diff(dir, base)
	if err != nil {
		t.Fatal(err)
	}

	modRanges, ok := changes["pkg/calc_test.go"]
	if !ok {
		t.Fatalf("changes missing pkg/calc_test.go: %v", changes)
	}
	found := false
	for _, r := range modRanges {
		if r[0] <= 4 && r[1] >= 4 {
			found = true
		}
	}
	if !found {
		t.Errorf("pkg/calc_test.go ranges = %v, want a range covering line 4", modRanges)
	}

	newRanges, ok := changes["pkg/new_test.go"]
	if !ok || newRanges == nil {
		t.Errorf("changes[pkg/new_test.go] = %v, want non-nil ranges (committed addition)", newRanges)
	}

	untrackedRanges, ok := changes["pkg/untracked_test.go"]
	if !ok {
		t.Fatalf("changes missing pkg/untracked_test.go: %v", changes)
	}
	if untrackedRanges != nil {
		t.Errorf("changes[pkg/untracked_test.go] = %v, want nil (whole file new)", untrackedRanges)
	}
}

func TestDiffUnicodeFilenames(t *testing.T) {
	dir, cfg := newGitRepo(t)

	writeFile(t, dir, "pkg/café_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\tx := 1\n\t_ = x\n}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	// Modify a line in the committed test file (line 4).
	writeFile(t, dir, "pkg/café_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\tx := 2\n\t_ = x\n}\n")

	// An untracked test file with a non-ASCII name.
	writeFile(t, dir, "pkg/niño_test.go", "package pkg\n\nfunc TestC(t *testing.T) {}\n")

	changes, err := Diff(dir, base)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := changes["pkg/café_test.go"]; !ok {
		t.Errorf("changes missing pkg/café_test.go: %v", changes)
	}
	untrackedRanges, ok := changes["pkg/niño_test.go"]
	if !ok {
		t.Fatalf("changes missing pkg/niño_test.go: %v", changes)
	}
	if untrackedRanges != nil {
		t.Errorf("changes[pkg/niño_test.go] = %v, want nil (whole file new)", untrackedRanges)
	}
}

func TestDiffIgnoresNonTestFiles(t *testing.T) {
	dir, cfg := newGitRepo(t)

	writeFile(t, dir, "pkg/calc.go", "package pkg\n\nfunc Add(a, b int) int { return a + b }\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	writeFile(t, dir, "pkg/calc.go", "package pkg\n\nfunc Add(a, b int) int { return a + b + 0 }\n")

	changes, err := Diff(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := changes["pkg/calc.go"]; ok {
		t.Errorf("changes contains non-test file pkg/calc.go: %v", changes)
	}
}

func TestDiffDeletionOnly(t *testing.T) {
	dir, cfg := newGitRepo(t)

	writeFile(t, dir, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\tx := 1\n\t_ = x\n}\n\nfunc TestB(t *testing.T) {}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	// Delete lines 4-5 (the body of TestA) without adding anything.
	writeFile(t, dir, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n}\n\nfunc TestB(t *testing.T) {}\n")

	changes, err := Diff(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	ranges, ok := changes["pkg/calc_test.go"]
	if !ok {
		t.Fatalf("changes missing pkg/calc_test.go: %v", changes)
	}
	// TestA spans lines 3-4 in the new file; the deletion-only hunk should
	// record a line within it so Touches finds it.
	c := Changes{"pkg/calc_test.go": ranges}
	if !c.Touches("pkg/calc_test.go", 3, 4) {
		t.Errorf("Touches(3,4) = false, want true for a deletion-only hunk within TestA")
	}
}

func TestTouches(t *testing.T) {
	c := Changes{
		"a_test.go": {{5, 10}},
		"b_test.go": nil,
	}
	cases := []struct {
		relpath    string
		start, end int
		want       bool
	}{
		{"a_test.go", 1, 4, false},
		{"a_test.go", 1, 5, true},
		{"a_test.go", 10, 20, true},
		{"a_test.go", 6, 7, true},
		{"a_test.go", 11, 20, false},
		{"b_test.go", 1, 1000, true},
		{"missing_test.go", 1, 1000, false},
	}
	for _, tc := range cases {
		if got := c.Touches(tc.relpath, tc.start, tc.end); got != tc.want {
			t.Errorf("Touches(%q, %d, %d) = %v, want %v", tc.relpath, tc.start, tc.end, got, tc.want)
		}
	}
}

func TestDiffBadBase(t *testing.T) {
	dir, cfg := newGitRepo(t)
	writeFile(t, dir, "a_test.go", "package a\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")

	_, err := Diff(dir, "not-a-real-base-ref")
	if err == nil {
		t.Fatal("Diff() with a bad base = nil error, want error")
	}
	if !strings.Contains(err.Error(), "not-a-real-base-ref") {
		t.Errorf("Diff() error = %q, want it to mention the base", err.Error())
	}
}

func TestDiffNotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Diff(dir, "HEAD")
	if err == nil {
		t.Fatal("Diff() outside a git repo = nil error, want error")
	}
	if !strings.Contains(err.Error(), "HEAD") {
		t.Errorf("Diff() error = %q, want it to mention the base", err.Error())
	}
}

func TestRoot(t *testing.T) {
	dir, cfg := newGitRepo(t)
	writeFile(t, dir, "a_test.go", "package a\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")

	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Root(sub)
	if err != nil {
		t.Fatal(err)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	resolvedGot, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedGot != resolvedDir {
		t.Errorf("Root(sub) = %q, want %q", got, dir)
	}

	notRepo := t.TempDir()
	got2, err := Root(notRepo)
	if err != nil {
		t.Fatal(err)
	}
	resolvedNotRepo, err := filepath.EvalSymlinks(notRepo)
	if err != nil {
		t.Fatal(err)
	}
	resolvedGot2, err := filepath.EvalSymlinks(got2)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedGot2 != resolvedNotRepo {
		t.Errorf("Root(notRepo) = %q, want %q (the absolute path itself)", got2, notRepo)
	}
}
