package check

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/rubric"

	_ "github.com/schuettc/tackle/internal/cull/extract/golang"
	_ "github.com/schuettc/tackle/internal/cull/extract/python"
	_ "github.com/schuettc/tackle/internal/cull/extract/ts"
)

// fakeEval answers every Jev question without a network call. A test named
// in cut (per-test states) or consolidate (any member of a group state)
// gets its act option boosted to 0.9; everything else is kept/kept-separate.
type fakeEval struct {
	calls       atomic.Int32
	cut         map[string]bool
	consolidate map[string]bool
	errOnTest   map[string]bool // per-test judge.State call for this test name errors
	errOnGroup  map[string]bool // group judge.GroupState call errors if any member matches
}

func f64(v float64) *float64 { return &v }

func (f *fakeEval) Evaluate(ctx context.Context, model string, state any, questions map[string]any) (jev.Response, error) {
	f.calls.Add(1)
	act := "keep"
	switch s := state.(type) {
	case judge.State:
		if f.errOnTest != nil && f.errOnTest[s.TestName] {
			return jev.Response{}, fmt.Errorf("fake error for %s", s.TestName)
		}
		if f.cut != nil && f.cut[s.TestName] {
			act = "cut"
		}
	case judge.GroupState:
		for _, t := range s.Tests {
			if f.errOnGroup != nil && f.errOnGroup[t.Name] {
				return jev.Response{}, fmt.Errorf("fake error for group with %s", t.Name)
			}
		}
		act = "keep_separate"
		for _, t := range s.Tests {
			if f.consolidate != nil && f.consolidate[t.Name] {
				act = "consolidate"
			}
		}
	}
	ans := map[string]jev.Answer{}
	for k, qv := range questions {
		q := qv.(map[string]any)
		switch q["type"] {
		case "noul":
			ans[k] = jev.Answer{Type: "noul", Noul: f64(0.1)}
		case "score":
			ans[k] = jev.Answer{Type: "score", Score: f64(1), Confidence: f64(0.8)}
		case "choice":
			opts, _ := q["criteria"].(map[string]string)
			probs := map[string]float64{}
			for o := range opts {
				probs[o] = 0.05
			}
			probs[act] = 0.9
			ans[k] = jev.Answer{Type: "choice", Choice: act, Probabilities: probs, Confidence: f64(0.85)}
		}
	}
	return jev.Response{Model: "fake-model", Answers: ans}, nil
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

func goModule(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
}

func withEgress(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, ".cull.toml", "egress = true\n")
}

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

// TestMain isolates every test in this package from the developer's real
// answer cache (~/.cache/cull/answers): Run always builds its Cache from
// tools.CacheDir("cull"), and without this, fake-evaluator answers from
// tests would be written to, and read back from, that real cache.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cull-check-cache")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("CULL_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestConfigDefaultsAndUnknownKey(t *testing.T) {
	root := t.TempDir()
	cfg, found, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("found = true with no .cull.toml")
	}
	if cfg.Model != "jev-latest" || cfg.MaxContextBytes != 64000 || cfg.Concurrency != 6 ||
		len(cfg.Exclude) != 0 || cfg.TestRubric != rubric.DefaultTest || cfg.GroupRubric != rubric.DefaultGroup || cfg.Egress {
		t.Fatalf("defaults = %+v", cfg)
	}

	writeFile(t, root, ".cull.toml", "egress = true\n")
	cfg, found, err = LoadConfig(root)
	if err != nil || !found || !cfg.Egress || cfg.Model != "jev-latest" {
		t.Fatalf("cfg = %+v, found = %v, err = %v", cfg, found, err)
	}

	writeFile(t, root, ".cull.toml", "egress = true\ntest_command = \"go test ./...\"\n")
	cfg, found, err = LoadConfig(root)
	if err != nil || !found || cfg.TestCommand != "go test ./..." {
		t.Fatalf("cfg = %+v, found = %v, err = %v", cfg, found, err)
	}

	writeFile(t, root, ".cull.toml", "egress = true\nbogus_key = 1\n")
	if _, _, err := LoadConfig(root); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestCheckRefusesWithoutEgress(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	writeFile(t, root, "a_test.go", "package a\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	f := &fakeEval{}
	_, err := Run(context.Background(), f, Options{Path: root})
	if err == nil || !strings.Contains(err.Error(), Endpoint) || !strings.Contains(err.Error(), "egress = true") {
		t.Fatalf("err = %v", err)
	}
	if f.calls.Load() != 0 {
		t.Errorf("evaluator called %d times", f.calls.Load())
	}
}

func TestCheckDryRunNoConfigNoKey(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	writeFile(t, root, "a_test.go", "package a\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	var stdout, stderr bytes.Buffer
	report, err := Run(context.Background(), nil, Options{Path: root, DryRun: true, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "suite" || report.Root == "" {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(stdout.String(), `"test_name":"TestA"`) {
		t.Errorf("stdout = %q, want a printed state for TestA", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want nothing", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".cull", "last.json")); !os.IsNotExist(err) {
		t.Errorf("last.json written during --dry-run: %v", err)
	}
}

func TestCheckSuiteMixedLanguages(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	withEgress(t, root)
	writeFile(t, root, "pkg/calc_test.go", "package pkg\n\nfunc TestAdd(t *testing.T) {\n\tif 1+1 != 2 {\n\t\tt.Fail()\n\t}\n}\n")
	if _, err := exec.LookPath("python3"); err == nil {
		writeFile(t, root, "tests/test_calc.py", "def test_add():\n    assert 1 + 1 == 2\n")
	} else {
		t.Log("python3 not found: skipping the python fixture half of this test")
	}

	f := &fakeEval{}
	report, err := Run(context.Background(), f, Options{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	langs := map[string]bool{}
	for _, tc := range report.Tests {
		langs[tc.Lang] = true
	}
	if !langs["go"] {
		t.Errorf("langs = %v, want go present", langs)
	}
	if _, err := exec.LookPath("python3"); err == nil && !langs["python"] {
		t.Errorf("langs = %v, want python present", langs)
	}
}

func TestCheckDiffOnlyChangedTests(t *testing.T) {
	dir, cfg := newGitRepo(t)
	goModule(t, dir)
	withEgress(t, dir)
	writeFile(t, dir, "pkg/calc_test.go",
		"package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	// Only TestA's body changes.
	writeFile(t, dir, "pkg/calc_test.go",
		"package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 99\n}\n\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n")

	f := &fakeEval{}
	report, err := Run(context.Background(), f, Options{Path: dir, Diff: base})
	if err != nil {
		t.Fatal(err)
	}
	if report.Mode != "diff" || report.Base != base {
		t.Fatalf("report mode/base = %q/%q", report.Mode, report.Base)
	}
	if len(report.Tests) != 1 || report.Tests[0].Name != "TestA" {
		t.Fatalf("tests = %+v, want only TestA", report.Tests)
	}
}

func TestCheckDiffGroupAnchoredByUnchangedSibling(t *testing.T) {
	dir, cfg := newGitRepo(t)
	goModule(t, dir)
	withEgress(t, dir)
	src := "package pkg\n\n" +
		"func TestA1(t *testing.T) {\n\tx := 1\n\ty := 2\n\tz := 3\n\tw := 4\n\tv := 5\n\tu := 6\n\t_ = u\n}\n\n" +
		"func TestA2(t *testing.T) {\n\tx := 1\n\ty := 2\n\tz := 3\n\tw := 4\n\tv := 5\n\tu := 7\n\t_ = u\n}\n"
	writeFile(t, dir, "pkg/calc_test.go", src)
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	// Only TestA1's body changes; TestA2 (its near-duplicate sibling) does not.
	src2 := "package pkg\n\n" +
		"func TestA1(t *testing.T) {\n\tx := 1\n\ty := 2\n\tz := 3\n\tw := 4\n\tv := 5\n\tu := 66\n\t_ = u\n}\n\n" +
		"func TestA2(t *testing.T) {\n\tx := 1\n\ty := 2\n\tz := 3\n\tw := 4\n\tv := 5\n\tu := 7\n\t_ = u\n}\n"
	writeFile(t, dir, "pkg/calc_test.go", src2)

	f := &fakeEval{}
	report, err := Run(context.Background(), f, Options{Path: dir, Diff: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("groups = %+v, want 1 group anchored by the touched sibling", report.Groups)
	}
	if len(report.Groups[0].Members) != 2 {
		t.Fatalf("group members = %v, want both TestA1 and TestA2", report.Groups[0].Members)
	}
}

func TestCheckSkippedFilesReported(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	withEgress(t, root)
	writeFile(t, root, "pkg/good_test.go", "package pkg\n\nfunc TestGood(t *testing.T) {\n\t_ = 1\n}\n")
	writeFile(t, root, "pkg/broken_test.go", "package pkg\n\nfunc TestBroken( {\n")

	f := &fakeEval{}
	var stderr bytes.Buffer
	report, err := Run(context.Background(), f, Options{Path: root, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "cull: skipped pkg/broken_test.go: parse") {
		t.Errorf("stderr = %q, want a skip line for pkg/broken_test.go", stderr.String())
	}
	found := false
	for _, s := range report.Skipped {
		if s.File == "pkg/broken_test.go" {
			found = true
			if !strings.Contains(s.Reason, "parse") {
				t.Errorf("reason = %q, want it to mention parse", s.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("skipped = %+v, want pkg/broken_test.go", report.Skipped)
	}
	goodFound := false
	for _, tc := range report.Tests {
		if tc.Name == "TestGood" {
			goodFound = true
		}
	}
	if !goodFound {
		t.Error("TestGood not extracted alongside a broken sibling file")
	}
	if report.Summary["skipped"] != len(report.Skipped) {
		t.Errorf("summary skipped = %d, want %d", report.Summary["skipped"], len(report.Skipped))
	}
}

func TestCheckWritesLastJSON(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	withEgress(t, root)
	writeFile(t, root, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")

	f := &fakeEval{}
	if _, err := Run(context.Background(), f, Options{Path: root}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, ".cull", "last.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("last.json mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Join(root, ".cull"))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf(".cull mode = %v, want 0700", dirInfo.Mode().Perm())
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tests) != 1 || got.Tests[0].Hash == "" || got.Tests[0].Span.End <= got.Tests[0].Span.Start {
		t.Fatalf("last.json tests = %+v", got.Tests)
	}
}

func TestCheckBadDiffBase(t *testing.T) {
	dir, cfg := newGitRepo(t)
	goModule(t, dir)
	withEgress(t, dir)
	writeFile(t, dir, "a_test.go", "package a\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")

	f := &fakeEval{}
	_, err := Run(context.Background(), f, Options{Path: dir, Diff: "not-a-real-base-ref"})
	if err == nil || !strings.Contains(err.Error(), "not-a-real-base-ref") {
		t.Fatalf("err = %v", err)
	}
	if f.calls.Load() != 0 {
		t.Errorf("evaluator called %d times on a bad diff base", f.calls.Load())
	}
}

func TestCheckPathThroughSymlink(t *testing.T) {
	dir, cfg := newGitRepo(t)
	goModule(t, dir)
	withEgress(t, dir)
	writeFile(t, dir, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}

	f := &fakeEval{}
	report, err := Run(context.Background(), f, Options{Path: link})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tests) != 1 || report.Tests[0].Name != "TestA" {
		t.Fatalf("tests = %+v, want TestA found through a symlinked path", report.Tests)
	}
}

// TestCheckDiffAppliesSuiteFilter: --diff must never judge a file suite mode
// wouldn't: files under skip dirs (testdata/, vendor/), files matching
// exclude, and files outside the [path] argument — tracked or untracked.
func TestCheckDiffAppliesSuiteFilter(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}
	dir, cfg := newGitRepo(t)
	writeFile(t, dir, ".cull.toml", "exclude = [\"svc/gen/**\"]\n")
	py := func(name string) string { return "def " + name + "():\n    assert 1 == 1\n" }
	writeFile(t, dir, "svc/tests/test_in.py", py("test_in_old"))
	writeFile(t, dir, "svc/testdata/test_fixture.py", py("test_fixture_old"))
	writeFile(t, dir, "other/test_out.py", py("test_out_old"))
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	// Tracked changes.
	writeFile(t, dir, "svc/tests/test_in.py", py("test_in"))
	writeFile(t, dir, "svc/testdata/test_fixture.py", py("test_fixture"))
	writeFile(t, dir, "other/test_out.py", py("test_out"))
	// Untracked files.
	writeFile(t, dir, "svc/tests/test_new.py", py("test_new"))
	writeFile(t, dir, "svc/gen/test_generated.py", py("test_generated"))
	writeFile(t, dir, "svc/vendor/test_vendored.py", py("test_vendored"))

	var stdout bytes.Buffer
	report, err := Run(context.Background(), nil, Options{Path: filepath.Join(dir, "svc"), Diff: base, DryRun: true, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"test_in", "test_new"} {
		if !strings.Contains(out, `"test_name":"`+want+`"`) {
			t.Errorf("dry-run output missing %s:\n%s", want, out)
		}
	}
	for _, bad := range []string{"test_fixture", "test_out", "test_generated", "test_vendored"} {
		if strings.Contains(out, bad) {
			t.Errorf("dry-run output includes %s, which suite mode would never send:\n%s", bad, out)
		}
	}
	if len(report.Skipped) != 0 {
		t.Errorf("skipped = %+v", report.Skipped)
	}
}

func TestSubPathDotDotPrefixedDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "..foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := subPath(root, filepath.Join(root, "..foo"))
	if err != nil || got != "..foo" {
		t.Fatalf("subPath(..foo) = %q, %v; want \"..foo\"", got, err)
	}
	if got, _ := subPath(root, filepath.Dir(root)); got != "" {
		t.Errorf("subPath(parent) = %q, want \"\"", got)
	}
}

// TestCheckNoTypescriptReported (RF3): with no typescript available, TS
// files are skipped and said so on stderr (dry-run too); Go still runs.
func TestCheckNoTypescriptReported(t *testing.T) {
	t.Setenv("CULL_TS", "")
	root := t.TempDir()
	goModule(t, root)
	writeFile(t, root, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	writeFile(t, root, "web/calc.test.ts", "test('adds', () => { expect(1).toBe(1); });\n")
	var stdout, stderr bytes.Buffer
	report, err := Run(context.Background(), nil, Options{Path: root, DryRun: true, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"test_name":"TestA"`) {
		t.Errorf("stdout = %q, want TestA state", stdout.String())
	}
	if len(report.Skipped) != 1 || report.Skipped[0].File != "web/calc.test.ts" {
		t.Fatalf("skipped = %+v", report.Skipped)
	}
	line := "cull: skipped web/calc.test.ts: " + report.Skipped[0].Reason + "\n"
	if stderr.String() != line {
		t.Errorf("stderr = %q, want %q", stderr.String(), line)
	}
}

// TestCheckMonorepoNoRootGoMod: a Go module in a subdirectory plus a Python
// test at the root, with no root go.mod — both are extracted, and a stray
// Go test with no go.mod is skipped, not fatal.
// TestReportFilesInventory: on a suite check, every extracted test appears
// under its file in Files, with its id and hash, and the file's sha256
// matches the file's actual bytes.
func TestReportFilesInventory(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	withEgress(t, root)
	content := "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n"
	writeFile(t, root, "pkg/calc_test.go", content)

	f := &fakeEval{}
	report, err := Run(context.Background(), f, Options{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	fi, ok := report.Files["pkg/calc_test.go"]
	if !ok {
		t.Fatalf("Files = %+v, want pkg/calc_test.go present", report.Files)
	}
	sum := sha256.Sum256([]byte(content))
	if fi.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %q, want %q", fi.SHA256, hex.EncodeToString(sum[:]))
	}
	names := map[string]string{} // id -> hash
	for _, ft := range fi.Tests {
		names[ft.ID] = ft.Hash
	}
	if len(names) != 2 {
		t.Fatalf("fi.Tests = %+v, want 2 entries", fi.Tests)
	}
	for _, tr := range report.Tests {
		h, ok := names[tr.ID]
		if !ok || h != tr.Hash || h == "" {
			t.Errorf("file inventory for %s = %q, want %q", tr.ID, h, tr.Hash)
		}
	}
}

// TestFileChangedDuringCheckIsSkipped: if a file's bytes change between
// extraction and fileInventory's hashing (racing with an editor, e.g.), the
// hash check catches it: the file is dropped entirely (not in Files or
// Tests) and reported as Skipped, while an unrelated file is unaffected.
func TestFileChangedDuringCheckIsSkipped(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	withEgress(t, root)
	writeFile(t, root, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	writeFile(t, root, "pkg/other_test.go", "package pkg\n\nfunc TestOther(t *testing.T) {\n\t_ = 2\n}\n")

	testHookBeforeHash = func(relpath string) {
		if relpath == "pkg/calc_test.go" {
			writeFile(t, root, relpath, "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 999\n}\n")
		}
	}
	t.Cleanup(func() { testHookBeforeHash = nil })

	f := &fakeEval{}
	var stderr bytes.Buffer
	report, err := Run(context.Background(), f, Options{Path: root, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := report.Files["pkg/calc_test.go"]; ok {
		t.Errorf("Files = %+v, want pkg/calc_test.go absent", report.Files)
	}
	for _, tr := range report.Tests {
		if tr.File == "pkg/calc_test.go" {
			t.Errorf("Tests = %+v, want no test from pkg/calc_test.go", report.Tests)
		}
	}
	for _, g := range report.Groups {
		if g.File == "pkg/calc_test.go" {
			t.Errorf("Groups = %+v, want no group from pkg/calc_test.go", report.Groups)
		}
	}
	found := false
	for _, s := range report.Skipped {
		if s.File == "pkg/calc_test.go" {
			found = true
			if s.Reason != "changed during check; run cull check again" {
				t.Errorf("reason = %q, want %q", s.Reason, "changed during check; run cull check again")
			}
		}
	}
	if !found {
		t.Fatalf("skipped = %+v, want pkg/calc_test.go", report.Skipped)
	}

	if _, ok := report.Files["pkg/other_test.go"]; !ok {
		t.Errorf("Files = %+v, want pkg/other_test.go present", report.Files)
	}
	otherFound := false
	for _, tr := range report.Tests {
		if tr.Name == "TestOther" {
			otherFound = true
		}
	}
	if !otherFound {
		t.Errorf("tests = %+v, want TestOther judged", report.Tests)
	}
}

// TestDiffModeInventoryHasUnjudgedTests: in diff mode, an untouched sibling
// test in a touched file shows up in Files (inventory), even though it is
// not in Tests (not judged).
func TestDiffModeInventoryHasUnjudgedTests(t *testing.T) {
	dir, cfg := newGitRepo(t)
	goModule(t, dir)
	withEgress(t, dir)
	writeFile(t, dir, "pkg/calc_test.go",
		"package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))

	// Only TestA's body changes; TestB is untouched.
	writeFile(t, dir, "pkg/calc_test.go",
		"package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 99\n}\n\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n")

	f := &fakeEval{}
	report, err := Run(context.Background(), f, Options{Path: dir, Diff: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tests) != 1 || report.Tests[0].Name != "TestA" {
		t.Fatalf("tests = %+v, want only TestA judged", report.Tests)
	}
	fi, ok := report.Files["pkg/calc_test.go"]
	if !ok {
		t.Fatalf("Files = %+v, want pkg/calc_test.go present", report.Files)
	}
	hasB := false
	for _, ft := range fi.Tests {
		if strings.HasSuffix(ft.ID, ":TestB") {
			hasB = true
		}
	}
	if !hasB {
		t.Errorf("file tests = %+v, want TestB present though not judged", fi.Tests)
	}
}

// TestGroupRowsRecorded: a consolidate group's MemberHashes and Rows are
// aligned with Members, and Rows carries the literal values that tell the
// members apart.
func TestGroupRowsRecorded(t *testing.T) {
	root := t.TempDir()
	goModule(t, root)
	withEgress(t, root)
	src := "package pkg\n\n" +
		"func TestA1(t *testing.T) {\n\tcfg := \"cfg\"\n\t_ = cfg\n\tx := \"one\"\n\t_ = x\n\ty := 1\n\t_ = y\n}\n\n" +
		"func TestA2(t *testing.T) {\n\tcfg := \"cfg\"\n\t_ = cfg\n\tx := \"two\"\n\t_ = x\n\ty := 2\n\t_ = y\n}\n"
	writeFile(t, root, "pkg/calc_test.go", src)

	f := &fakeEval{consolidate: map[string]bool{"TestA1": true, "TestA2": true}}
	report, err := Run(context.Background(), f, Options{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("groups = %+v, want 1", report.Groups)
	}
	g := report.Groups[0]
	if len(g.Members) != 2 || len(g.MemberHashes) != len(g.Members) || len(g.Rows) != len(g.Members) {
		t.Fatalf("group = %+v, want MemberHashes/Rows aligned with Members", g)
	}
	for i, h := range g.MemberHashes {
		if h == "" {
			t.Errorf("MemberHashes[%d] empty", i)
		}
	}
	for i, row := range g.Rows {
		if len(row) == 0 {
			t.Errorf("Rows[%d] = %v, want non-empty (a distinguishing literal)", i, row)
		}
	}
}

func TestCheckMonorepoNoRootGoMod(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}
	root := t.TempDir()
	writeFile(t, root, "services/api/go.mod", "module example.com/api\n\ngo 1.22\n")
	writeFile(t, root, "services/api/calc_test.go", "package api\n\nfunc TestGoSide(t *testing.T) {\n\t_ = 1\n}\n")
	writeFile(t, root, "scripts/stray_test.go", "package scripts\n\nfunc TestStray(t *testing.T) {\n\t_ = 1\n}\n")
	writeFile(t, root, "test_root.py", "def test_py_side():\n    assert 1 == 1\n")

	var stdout, stderr bytes.Buffer
	report, err := Run(context.Background(), nil, Options{Path: root, DryRun: true, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"test_name":"TestGoSide"`, `"test_name":"test_py_side"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %s:\n%s", want, stdout.String())
		}
	}
	if len(report.Skipped) != 1 || report.Skipped[0].File != "scripts/stray_test.go" || !strings.Contains(report.Skipped[0].Reason, "no go.mod") {
		t.Errorf("skipped = %+v, want scripts/stray_test.go (no go.mod)", report.Skipped)
	}
}
