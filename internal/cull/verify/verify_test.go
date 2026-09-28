package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlanTestCommandWins(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"pkg/foo_test.go": "go"}
	cmds, err := Plan(root, "make test", files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("len(cmds) = %d, want 1", len(cmds))
	}
	c := cmds[0]
	if c.Dir != root {
		t.Errorf("Dir = %q, want %q", c.Dir, root)
	}
	wantArgv := []string{"sh", "-c", "make test"}
	if len(c.Argv) != len(wantArgv) {
		t.Fatalf("Argv = %v, want %v", c.Argv, wantArgv)
	}
	for i := range wantArgv {
		if c.Argv[i] != wantArgv[i] {
			t.Errorf("Argv[%d] = %q, want %q", i, c.Argv[i], wantArgv[i])
		}
	}
	if c.Shell != "make test" {
		t.Errorf("Shell = %q, want %q", c.Shell, "make test")
	}
}

func TestPlanGoPerModule(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/root\n\ngo 1.22\n")
	writeFile(t, root, "services/api/go.mod", "module example.com/api\n\ngo 1.22\n")
	files := map[string]string{
		"pkg/foo_test.go":              "go",
		"main_test.go":                 "go",
		"services/api/handler_test.go": "go",
		"services/api/sub/x_test.go":   "go",
	}
	cmds, err := Plan(root, "", files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("len(cmds) = %d, want 2: %+v", len(cmds), cmds)
	}
	// Deterministic order: root module before nested module (sorted by dir).
	root0 := cmds[0]
	if root0.Dir != root {
		t.Errorf("cmds[0].Dir = %q, want %q", root0.Dir, root)
	}
	wantArgv0 := []string{"go", "test", "./", "./pkg"}
	if strings.Join(root0.Argv, " ") != strings.Join(wantArgv0, " ") {
		t.Errorf("cmds[0].Argv = %v, want %v", root0.Argv, wantArgv0)
	}

	api := cmds[1]
	wantDir := filepath.Join(root, "services", "api")
	if api.Dir != wantDir {
		t.Errorf("cmds[1].Dir = %q, want %q", api.Dir, wantDir)
	}
	wantArgv1 := []string{"go", "test", "./", "./sub"}
	if strings.Join(api.Argv, " ") != strings.Join(wantArgv1, " ") {
		t.Errorf("cmds[1].Argv = %v, want %v", api.Argv, wantArgv1)
	}
}

func TestPlanGoNoModIsPlanError(t *testing.T) {
	root := t.TempDir()
	// No go.mod anywhere under root.
	files := map[string]string{"pkg/foo_test.go": "go"}
	_, err := Plan(root, "", files)
	if err == nil {
		t.Fatal("Plan: want error when no go.mod is found at or above the file")
	}
	want := "cannot determine how to run pkg/foo_test.go's tests; set test_command in .cull.toml or pass --no-verify"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestPlanPythonVenvAndProjectDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "svc/pyproject.toml", "[tool.pytest]\n")
	files := map[string]string{
		"svc/test_a.py":     "python",
		"svc/sub/test_b.py": "python",
	}

	// No VIRTUAL_ENV, no .venv/venv present -> python3.
	t.Setenv("VIRTUAL_ENV", "")
	cmds, err := Plan(root, "", files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("len(cmds) = %d, want 1: %+v", len(cmds), cmds)
	}
	c := cmds[0]
	wantDir := filepath.Join(root, "svc")
	if c.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", c.Dir, wantDir)
	}
	wantArgv := []string{"python3", "-m", "pytest", "-q", "sub/test_b.py", "test_a.py"}
	if strings.Join(c.Argv, " ") != strings.Join(wantArgv, " ") {
		t.Errorf("Argv = %v, want %v", c.Argv, wantArgv)
	}

	// With .venv present, the venv interpreter wins over python3.
	writeFile(t, root, "svc/.venv/bin/python", "#!/bin/sh\n")
	cmds, err = Plan(root, "", files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantPy := filepath.Join(root, "svc", ".venv", "bin", "python")
	if cmds[0].Argv[0] != wantPy {
		t.Errorf("Argv[0] = %q, want %q", cmds[0].Argv[0], wantPy)
	}

	// VIRTUAL_ENV wins over .venv.
	venvRoot := t.TempDir()
	t.Setenv("VIRTUAL_ENV", venvRoot)
	cmds, err = Plan(root, "", files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	wantVenvPy := filepath.Join(venvRoot, "bin", "python")
	if cmds[0].Argv[0] != wantVenvPy {
		t.Errorf("Argv[0] = %q, want %q", cmds[0].Argv[0], wantVenvPy)
	}
}

func TestPlanTsNeedsTestScript(t *testing.T) {
	root := t.TempDir()

	// package.json with a test script -> npm test.
	writeFile(t, root, "web/package.json", `{"scripts": {"test": "jest"}}`)
	files := map[string]string{"web/src/foo.test.ts": "typescript"}
	cmds, err := Plan(root, "", files)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("len(cmds) = %d, want 1", len(cmds))
	}
	c := cmds[0]
	wantDir := filepath.Join(root, "web")
	if c.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", c.Dir, wantDir)
	}
	if strings.Join(c.Argv, " ") != "npm test" {
		t.Errorf("Argv = %v, want [npm test]", c.Argv)
	}

	// package.json without a test script -> error.
	writeFile(t, root, "notest/package.json", `{"scripts": {"build": "tsc"}}`)
	files2 := map[string]string{"notest/src/foo.test.ts": "typescript"}
	_, err = Plan(root, "", files2)
	if err == nil {
		t.Fatal("Plan: want error for package.json with no test script")
	}
	wantMsg := "cannot determine how to run notest/src/foo.test.ts's tests; set test_command in .cull.toml or pass --no-verify"
	if err.Error() != wantMsg {
		t.Errorf("err = %q, want %q", err.Error(), wantMsg)
	}
}

func TestPlanUnknownErrors(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"pkg/thing.rb": "ruby"}
	_, err := Plan(root, "", files)
	if err == nil {
		t.Fatal("Plan: want error for unknown language")
	}
	want := "cannot determine how to run pkg/thing.rb's tests; set test_command in .cull.toml or pass --no-verify"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestRunStopsAtFirstFailureAndTails(t *testing.T) {
	cmds := []Command{
		{Dir: ".", Argv: []string{"sh", "-c", "echo one; exit 0"}},
		{Dir: ".", Argv: []string{"sh", "-c", "for i in $(seq 1 70); do echo line$i; done; exit 3"}},
		{Dir: ".", Argv: []string{"sh", "-c", "echo should-not-run; exit 0"}},
	}
	results := Run(context.Background(), cmds, 5*time.Second)
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (stop at first failure): %+v", len(results), results)
	}
	if !results[0].OK || results[0].ExitCode != 0 {
		t.Errorf("results[0] = %+v, want OK, exit 0", results[0])
	}
	r1 := results[1]
	if r1.OK || r1.ExitCode != 3 {
		t.Errorf("results[1] = %+v, want !OK, exit 3", r1)
	}
	if r1.TimedOut {
		t.Errorf("results[1].TimedOut = true, want false")
	}
	lines := strings.Split(strings.TrimRight(r1.OutputTail, "\n"), "\n")
	if len(lines) != 60 {
		t.Fatalf("OutputTail has %d lines, want 60:\n%s", len(lines), r1.OutputTail)
	}
	if lines[len(lines)-1] != "line70" {
		t.Errorf("last tail line = %q, want line70", lines[len(lines)-1])
	}
	if lines[0] != "line11" {
		t.Errorf("first tail line = %q, want line11", lines[0])
	}
}

func TestRunTimeout(t *testing.T) {
	cmds := []Command{
		{Dir: ".", Argv: []string{"sh", "-c", "sleep 5"}},
	}
	start := time.Now()
	results := Run(context.Background(), cmds, 200*time.Millisecond)
	elapsed := time.Since(start)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	r := results[0]
	if !r.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}
	if r.OK {
		t.Errorf("OK = true, want false")
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, want well under 5s (child should be killed)", elapsed)
	}
}

func TestRunTimeoutBoundedWaitAfterKill(t *testing.T) {
	old := killGrace
	killGrace = 500 * time.Millisecond
	defer func() { killGrace = old }()

	cmds := []Command{
		// Spawns a detached grandchild in its own session, holding the
		// stdout/stderr pipe open well past the parent's death, then the
		// parent itself sleeps past the timeout.
		{Dir: ".", Argv: []string{"sh", "-c", "setsid sleep 30 </dev/null >/dev/null 2>&1 & sleep 30"}},
	}
	start := time.Now()
	results := Run(context.Background(), cmds, 200*time.Millisecond)
	elapsed := time.Since(start)
	// Clean up any stray detached sleep from this test.
	_ = exec.Command("pkill", "-f", "setsid sleep 30").Run()

	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	r := results[0]
	if !r.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}
	if r.OK {
		t.Errorf("OK = true, want false")
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, want well under 2s (bounded wait after kill)", elapsed)
	}
}

func TestRunEnvHasNoKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "secret-value")
	cmds := []Command{
		{Dir: ".", Argv: []string{"sh", "-c", `if [ -n "$TYPESAFE_API_KEY" ]; then echo leaked; exit 1; fi; echo clean`}},
	}
	results := Run(context.Background(), cmds, 5*time.Second)
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	r := results[0]
	if !r.OK {
		t.Errorf("OK = false, want true (env should not contain TYPESAFE_API_KEY): tail=%s", r.OutputTail)
	}
	if strings.Contains(r.OutputTail, "leaked") {
		t.Errorf("OutputTail contains leaked key marker: %s", r.OutputTail)
	}
}
