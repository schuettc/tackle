package check

import (
	"bytes"
	"context"
	"encoding/json"
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
}

func f64(v float64) *float64 { return &v }

func (f *fakeEval) Evaluate(ctx context.Context, model string, state any, questions map[string]any) (jev.Response, error) {
	f.calls.Add(1)
	act := "keep"
	switch s := state.(type) {
	case judge.State:
		if f.cut != nil && f.cut[s.TestName] {
			act = "cut"
		}
	case judge.GroupState:
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
	os.Setenv("CULL_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
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
	if cfg.Model != "jev-latest" || cfg.MaxContextBytes != 24000 || cfg.Concurrency != 6 ||
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
	report, err := Run(context.Background(), f, Options{Path: root})
	if err != nil {
		t.Fatal(err)
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
