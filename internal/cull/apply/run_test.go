package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/extract"
	"github.com/schuettc/tackle/internal/cull/verify"

	_ "github.com/schuettc/tackle/internal/cull/extract/golang"
)

// fixtureFile writes content to root/relpath (parents created) with mode.
func fixtureFile(t *testing.T, root, relpath, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(relpath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// goProject makes a temp Go module with the given files (relpath ->
// content) and returns its root.
func goProject(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	root := t.TempDir()
	fixtureFile(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n", 0o644)
	for rel, content := range files {
		fixtureFile(t, root, rel, content, 0o644)
	}
	return root
}

// writeReport extracts the tests in relpaths with the real extractors (so
// ids, hashes and spans are real), marks those whose name is in cut with
// verdict "cut" (the rest "keep"), records every file's sha256 and test
// inventory, and writes it as <root>/.cull/last.json.
func writeReport(t *testing.T, root string, relpaths []string, cut ...string) check.Report {
	t.Helper()
	cutSet := map[string]bool{}
	for _, c := range cut {
		cutSet[c] = true
	}
	r := check.Report{Root: root, Mode: "suite", Files: map[string]check.FileInfo{}}
	for _, rel := range relpaths {
		ex := extract.ForFile(rel)
		if ex == nil {
			t.Fatalf("no extractor for %s", rel)
		}
		res, err := ex.Extract(root, []string{rel}, 24000)
		if err != nil {
			t.Fatalf("extract %s: %v", rel, err)
		}
		if len(res.Cases) == 0 {
			t.Fatalf("extract %s: no cases (skipped: %+v)", rel, res.Skipped)
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		fi := check.FileInfo{SHA256: hex.EncodeToString(sum[:])}
		for _, c := range res.Cases {
			v := "keep"
			if cutSet[c.Name] || cutSet[c.ID] {
				v = "cut"
			}
			r.Tests = append(r.Tests, check.TestResult{TestCase: c, Verdict: v})
			fi.Tests = append(fi.Tests, check.FileTest{ID: c.ID, Hash: c.Hash})
		}
		r.Files[rel] = fi
	}
	writeLastJSON(t, root, r)
	return r
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

func runApply(t *testing.T, opt Options) (Outcome, error) {
	t.Helper()
	if opt.Timeout == 0 {
		opt.Timeout = 5 * time.Minute
	}
	return Run(context.Background(), opt)
}

const calcGo = `package calc

func Add(a, b int) int { return a + b }
`

const calcTestGo = `package calc

import (
	"strings"
	"testing"
)

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}

func TestUpper(t *testing.T) {
	if strings.ToUpper("a") != "A" {
		t.Fatal("bad")
	}
}
`

func TestApplyRemovesAndVerifies(t *testing.T) {
	for _, forced := range []bool{false, true} {
		t.Run(withFallback(forced), func(t *testing.T) {
			forceGoTidyFallback = forced
			defer func() { forceGoTidyFallback = false }()
			testApplyRemovesAndVerifies(t)
		})
	}
}

func testApplyRemovesAndVerifies(t *testing.T) {
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": calcTestGo})
	writeReport(t, root, []string{"calc_test.go"}, "TestUpper")

	out, err := runApply(t, Options{Root: root, VerdictCut: true})
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit %d: %v\n%+v", code, err, out)
	}
	want := `package calc

import (
	"testing"
)

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}
`
	if got := readFile(t, root, "calc_test.go"); got != want {
		t.Errorf("calc_test.go =\n%s\nwant\n%s", got, want)
	}
	if len(out.Applied) != 1 || out.Applied[0] != "go:calc_test.go:TestUpper" {
		t.Errorf("Applied = %v", out.Applied)
	}
	if len(out.Files) != 1 || out.Files[0] != "calc_test.go" {
		t.Errorf("Files = %v", out.Files)
	}
	if got := out.ImportsRemoved["calc_test.go"]; len(got) != 1 || got[0] != "strings" {
		t.Errorf("ImportsRemoved = %v", out.ImportsRemoved)
	}
	if len(out.Baseline) != 1 || !out.Baseline[0].OK || len(out.After) != 1 || !out.After[0].OK {
		t.Errorf("Baseline = %+v After = %+v", out.Baseline, out.After)
	}
	if out.RolledBack {
		t.Error("RolledBack = true")
	}
	if out.Snapshot == "" {
		t.Error("Snapshot empty")
	}
}

func TestApplyRollsBackOnFailure(t *testing.T) {
	src := `package calc

import "testing"

func TestA(t *testing.T) {
	if Add(1, 1) != 2 {
		t.Fatal("bad")
	}
}

func TestB(t *testing.T) {
	TestA(t)
}
`
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": src})
	writeReport(t, root, []string{"calc_test.go"}, "TestA")

	out, err := runApply(t, Options{Root: root, IDs: []string{"go:calc_test.go:TestA"}})
	if code := exitCode(err); code != 1 {
		t.Fatalf("exit %d, want 1: %v", code, err)
	}
	if got := readFile(t, root, "calc_test.go"); got != src {
		t.Errorf("not restored byte-for-byte:\n%s", got)
	}
	if !out.RolledBack {
		t.Error("RolledBack = false")
	}
	if len(out.After) != 1 || out.After[0].OK || !strings.Contains(out.After[0].OutputTail, "TestA") {
		t.Errorf("After = %+v, want a failure whose tail mentions TestA", out.After)
	}
	if out.Snapshot == "" || !strings.Contains(err.Error(), out.Snapshot) {
		t.Errorf("error %q should name snapshot %q", err, out.Snapshot)
	}
}

func TestApplyRefusesWhenBaselineFails(t *testing.T) {
	src := `package calc

import "testing"

func TestA(t *testing.T) {}

func TestBroken(t *testing.T) {
	t.Fatal("already broken")
}
`
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": src})
	writeReport(t, root, []string{"calc_test.go"}, "TestA")

	out, err := runApply(t, Options{Root: root, VerdictCut: true})
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d, want 2: %v", code, err)
	}
	if !strings.Contains(err.Error(), "tests already fail before any change") {
		t.Errorf("err = %v", err)
	}
	if got := readFile(t, root, "calc_test.go"); got != src {
		t.Errorf("file changed:\n%s", got)
	}
	if out.Snapshot != "" || out.RolledBack {
		t.Errorf("Snapshot = %q RolledBack = %v; want nothing written", out.Snapshot, out.RolledBack)
	}
	if _, err := os.Stat(filepath.Join(root, ".cull", "rollback")); !os.IsNotExist(err) {
		t.Errorf("rollback dir exists (err %v); nothing should be written", err)
	}
	if len(out.Baseline) != 1 || out.Baseline[0].OK {
		t.Errorf("Baseline = %+v", out.Baseline)
	}
}

// afterOnly swaps runVerify so the baseline runs the real commands but the
// after-run runs cmds instead (e.g. a missing binary that fails to start).
func afterOnly(t *testing.T, after []verify.Command) {
	t.Helper()
	orig := runVerify
	calls := 0
	runVerify = func(ctx context.Context, cmds []verify.Command, timeout time.Duration) []verify.Result {
		calls++
		if calls == 1 {
			return orig(ctx, cmds, timeout)
		}
		return orig(ctx, after, timeout)
	}
	t.Cleanup(func() { runVerify = orig })
}

func TestApplyRestoresOnStartFailure(t *testing.T) {
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": calcTestGo})
	fixtureFile(t, root, ".cull.toml", "test_command = \"go test ./...\"\n", 0o644)
	writeReport(t, root, []string{"calc_test.go"}, "TestUpper")
	afterOnly(t, []verify.Command{{Dir: root, Argv: []string{filepath.Join(root, "no-such-test-binary")}}})

	out, err := runApply(t, Options{Root: root, VerdictCut: true, TestCommand: "go test ./..."})
	if code := exitCode(err); code != 1 {
		t.Fatalf("exit %d, want 1: %v", code, err)
	}
	if got := readFile(t, root, "calc_test.go"); got != calcTestGo {
		t.Errorf("not restored:\n%s", got)
	}
	if !out.RolledBack || len(out.After) != 1 || out.After[0].OK || out.After[0].ExitCode != -1 {
		t.Errorf("RolledBack = %v After = %+v", out.RolledBack, out.After)
	}
	if len(out.Baseline) != 1 || out.Baseline[0].Command != "go test ./..." {
		t.Errorf("Baseline = %+v, want the test_command", out.Baseline)
	}
}

// failWrites swaps writeFile so each call for which fail(call number,
// 1-based) is true writes garbage and returns an error; the rest write
// normally.
func failWrites(t *testing.T, fail func(call int) bool) {
	t.Helper()
	orig := writeFile
	calls := 0
	writeFile = func(path string, data []byte, mode os.FileMode) error {
		calls++
		if fail(calls) {
			_ = orig(path, []byte("garbage"), mode)
			return errors.New("injected write failure")
		}
		return orig(path, data, mode)
	}
	t.Cleanup(func() { writeFile = orig })
}

func twoFileProject(t *testing.T) (root, a, b string) {
	t.Helper()
	a = calcTestGo
	b = strings.Replace(strings.Replace(calcTestGo, "TestAdd", "TestAdd2", 1), "TestUpper", "TestUpper2", 1)
	root = goProject(t, map[string]string{"calc.go": calcGo, "a_test.go": a, "b_test.go": b})
	writeReport(t, root, []string{"a_test.go", "b_test.go"}, "TestUpper", "TestUpper2")
	return root, a, b
}

func TestApplyRestoresOnWriteError(t *testing.T) {
	root, a, b := twoFileProject(t)
	// The second edit write fails (after leaving garbage); restores work.
	failWrites(t, func(call int) bool { return call == 2 })

	out, err := runApply(t, Options{Root: root, VerdictCut: true})
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d, want 2: %v", code, err)
	}
	if !strings.Contains(err.Error(), "injected write failure") {
		t.Errorf("err = %v", err)
	}
	if readFile(t, root, "a_test.go") != a || readFile(t, root, "b_test.go") != b {
		t.Error("files not restored")
	}
	if !out.RolledBack || out.Snapshot == "" {
		t.Errorf("RolledBack = %v Snapshot = %q", out.RolledBack, out.Snapshot)
	}
	if len(out.After) != 0 {
		t.Errorf("After = %+v, want no after-run", out.After)
	}
}

func TestApplyRestoreMismatchIsHardError(t *testing.T) {
	root, _, _ := twoFileProject(t)
	// The edit writes (2) succeed; the restore writes corrupt the file.
	failWrites(t, func(call int) bool { return call > 2 })
	afterOnly(t, []verify.Command{{Dir: root, Argv: []string{"false"}}})

	out, err := runApply(t, Options{Root: root, VerdictCut: true})
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d, want 2: %v", code, err)
	}
	if out.Snapshot == "" || !strings.Contains(err.Error(), out.Snapshot) {
		t.Errorf("err %q should name the snapshot dir %q", err, out.Snapshot)
	}
	snap, rerr := os.ReadFile(filepath.Join(out.Snapshot, "a_test.go"))
	if rerr != nil || string(snap) != calcTestGo {
		t.Errorf("snapshot copy = %q, %v", snap, rerr)
	}
}

func TestApplyRefusesChangedFile(t *testing.T) {
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": calcTestGo})
	writeReport(t, root, []string{"calc_test.go"}, "TestUpper")
	changed := calcTestGo + "\n// edited after check\n"
	fixtureFile(t, root, "calc_test.go", changed, 0o644)

	t.Run("ids", func(t *testing.T) {
		out, err := runApply(t, Options{Root: root, IDs: []string{"go:calc_test.go:TestUpper"}})
		if code := exitCode(err); code != 2 {
			t.Fatalf("exit %d, want 2: %v", code, err)
		}
		if len(out.Refused) != 1 || !strings.Contains(out.Refused[0].Reason, "file changed since cull check") {
			t.Errorf("Refused = %+v", out.Refused)
		}
		if len(out.Applied) != 0 || out.Snapshot != "" {
			t.Errorf("Applied = %v Snapshot = %q", out.Applied, out.Snapshot)
		}
	})
	t.Run("verdict", func(t *testing.T) {
		out, err := runApply(t, Options{Root: root, VerdictCut: true})
		if code := exitCode(err); code != 0 {
			t.Fatalf("exit %d, want 0: %v", code, err)
		}
		if len(out.Refused) != 1 || out.Refused[0].ID != "go:calc_test.go:TestUpper" {
			t.Errorf("Refused = %+v", out.Refused)
		}
		if len(out.Applied) != 0 || out.Snapshot != "" || len(out.Baseline) != 0 {
			t.Errorf("Applied = %v Snapshot = %q Baseline = %+v", out.Applied, out.Snapshot, out.Baseline)
		}
	})
	if got := readFile(t, root, "calc_test.go"); got != changed {
		t.Errorf("file touched:\n%s", got)
	}
}

func TestApplyRefusesUnknownID(t *testing.T) {
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": calcTestGo})
	writeReport(t, root, []string{"calc_test.go"}, "TestUpper")
	out, err := runApply(t, Options{Root: root, IDs: []string{"go:calc_test.go:TestUpper", "go:calc_test.go:TestNope"}})
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d, want 2: %v", code, err)
	}
	if len(out.Refused) != 1 || out.Refused[0].Reason != "not in last.json" {
		t.Errorf("Refused = %+v", out.Refused)
	}
	if got := readFile(t, root, "calc_test.go"); got != calcTestGo {
		t.Errorf("file touched:\n%s", got)
	}
}

func TestApplyNeedsAgentForSubtests(t *testing.T) {
	src := `package calc

import "testing"

func TestAdd(t *testing.T) {
	t.Run("one", func(t *testing.T) {
		if Add(1, 0) != 1 {
			t.Fatal("bad")
		}
	})
	t.Run("two", func(t *testing.T) {
		if Add(1, 1) != 2 {
			t.Fatal("bad")
		}
	})
}
`
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": src})
	r := writeReport(t, root, []string{"calc_test.go"})
	var sub string
	for i, tc := range r.Tests {
		if tc.Parent != "" {
			r.Tests[i].Verdict = "cut"
			sub = tc.ID
			break
		}
	}
	if sub == "" {
		t.Fatalf("no subtest extracted: %+v", r.Tests)
	}
	writeLastJSON(t, root, r)

	out, err := runApply(t, Options{Root: root, VerdictCut: true})
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit %d, want 0: %v", code, err)
	}
	if len(out.NeedsAgent) != 1 || out.NeedsAgent[0] != sub || len(out.Applied) != 0 {
		t.Errorf("NeedsAgent = %v Applied = %v", out.NeedsAgent, out.Applied)
	}

	out, err = runApply(t, Options{Root: root, IDs: []string{sub}})
	if code := exitCode(err); code != 2 {
		t.Fatalf("--ids subtest: exit %d, want 2: %v", code, err)
	}
	if len(out.Refused) != 1 || !strings.Contains(out.Refused[0].Reason, "edit it by hand") {
		t.Errorf("Refused = %+v", out.Refused)
	}
	if got := readFile(t, root, "calc_test.go"); got != src {
		t.Errorf("file touched:\n%s", got)
	}
}

func TestApplyNoVerify(t *testing.T) {
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": calcTestGo})
	writeReport(t, root, []string{"calc_test.go"}, "TestUpper")
	orig := runVerify
	runVerify = func(context.Context, []verify.Command, time.Duration) []verify.Result {
		t.Error("verify ran under NoVerify")
		return nil
	}
	defer func() { runVerify = orig }()

	// A test command that could never pass: NoVerify must not run it.
	out, err := runApply(t, Options{Root: root, VerdictCut: true, NoVerify: true, TestCommand: "exit 1"})
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit %d: %v", code, err)
	}
	if strings.Contains(readFile(t, root, "calc_test.go"), "TestUpper") {
		t.Error("TestUpper not removed")
	}
	if len(out.Applied) != 1 || out.Baseline != nil || out.After != nil {
		t.Errorf("Applied = %v Baseline = %+v After = %+v", out.Applied, out.Baseline, out.After)
	}
}

func TestApplySnapshotWritten(t *testing.T) {
	root := goProject(t, map[string]string{"pkg/calc.go": calcGo})
	fixtureFile(t, root, "pkg/calc_test.go", calcTestGo, 0o640)
	writeReport(t, root, []string{"pkg/calc_test.go"}, "TestUpper")

	out, err := runApply(t, Options{Root: root, VerdictCut: true})
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit %d: %v", code, err)
	}
	rollback := filepath.Join(root, ".cull", "rollback")
	if filepath.Dir(out.Snapshot) != rollback {
		t.Fatalf("Snapshot = %q, want a dir under %s", out.Snapshot, rollback)
	}
	if _, err := time.Parse("20060102T150405Z", filepath.Base(out.Snapshot)); err != nil {
		t.Errorf("snapshot dir name %q: %v", filepath.Base(out.Snapshot), err)
	}
	for _, d := range []string{rollback, out.Snapshot, filepath.Join(out.Snapshot, "pkg")} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 700", d, fi.Mode().Perm())
		}
	}
	snap := filepath.Join(out.Snapshot, "pkg", "calc_test.go")
	fi, err := os.Stat(snap)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("snapshot file mode = %o, want 600", fi.Mode().Perm())
	}
	if got := readFile(t, root, filepath.ToSlash(filepath.Join(".cull", "rollback", filepath.Base(out.Snapshot), "pkg", "calc_test.go"))); got != calcTestGo {
		t.Errorf("snapshot contents = %q, want the pre-edit file", got)
	}
	// The edited file keeps its own permission bits.
	efi, err := os.Stat(filepath.Join(root, "pkg", "calc_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if efi.Mode().Perm() != 0o640 {
		t.Errorf("edited file mode = %o, want 640", efi.Mode().Perm())
	}
}

func TestApplyNothingSelected(t *testing.T) {
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": calcTestGo})
	writeReport(t, root, []string{"calc_test.go"})
	out, err := runApply(t, Options{Root: root, VerdictCut: true})
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit %d: %v", code, err)
	}
	if len(out.Applied) != 0 || out.Snapshot != "" || out.Baseline != nil {
		t.Errorf("out = %+v", out)
	}
}

func TestApplyRequiresExactlyOneSelector(t *testing.T) {
	root := goProject(t, map[string]string{"calc.go": calcGo, "calc_test.go": calcTestGo})
	writeReport(t, root, []string{"calc_test.go"}, "TestUpper")
	for _, opt := range []Options{
		{Root: root},
		{Root: root, VerdictCut: true, IDs: []string{"go:calc_test.go:TestUpper"}},
	} {
		if _, err := runApply(t, opt); exitCode(err) != 2 {
			t.Errorf("%+v: err = %v, want exit 2", opt, err)
		}
	}
	if got := readFile(t, root, "calc_test.go"); got != calcTestGo {
		t.Errorf("file touched")
	}
}

func TestApplyPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	src := `import os
import unittest


def test_keep():
    assert 1 + 1 == 2


def test_env():
    assert os.sep
`
	root := t.TempDir()
	fixtureFile(t, root, "pyproject.toml", "[project]\nname = \"fixture\"\n", 0o644)
	fixtureFile(t, root, "test_calc.py", src, 0o644)
	writeReport(t, root, []string{"test_calc.py"}, "test_env")

	cmd := "python3 -m py_compile test_calc.py"
	out, err := runApply(t, Options{Root: root, VerdictCut: true, TestCommand: cmd})
	if code := exitCode(err); code != 0 {
		t.Fatalf("exit %d: %v\n%+v", code, err, out)
	}
	got := readFile(t, root, "test_calc.py")
	if strings.Contains(got, "test_env") || strings.Contains(got, "import os") || !strings.Contains(got, "def test_keep") {
		t.Errorf("test_calc.py =\n%s", got)
	}
	if len(out.After) != 1 || !out.After[0].OK || out.After[0].Command != cmd {
		t.Errorf("After = %+v", out.After)
	}
}
