package creel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExecArgs(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want execArgs
		err  bool
	}{
		{"single name", []string{"KEY", "--", "run"},
			execArgs{names: []string{"KEY"}, command: []string{"run"}}, false},
		{"multi name", []string{"A,B,C", "--", "run", "-x"},
			execArgs{names: []string{"A", "B", "C"}, command: []string{"run", "-x"}}, false},
		{"dest space", []string{"KEY", "--dest", "config/.env", "--", "run"},
			execArgs{names: []string{"KEY"}, dest: "config/.env", command: []string{"run"}}, false},
		{"dest eq", []string{"KEY", "--dest=config/.env", "--", "run"},
			execArgs{names: []string{"KEY"}, dest: "config/.env", command: []string{"run"}}, false},
		{"command with own flags after boundary", []string{"KEY", "--", "tool", "--help", "--dest", "x"},
			execArgs{names: []string{"KEY"}, command: []string{"tool", "--help", "--dest", "x"}}, false},
		{"missing boundary", []string{"KEY", "run"}, execArgs{}, true},
		{"no boundary at all", []string{"KEY"}, execArgs{}, true},
		{"nothing after boundary", []string{"KEY", "--"}, execArgs{}, true},
		{"no names before boundary", []string{"--", "run"}, execArgs{}, true},
		{"dangling dest", []string{"KEY", "--dest"}, execArgs{}, true},
		{"unknown flag", []string{"KEY", "--nope", "--", "run"}, execArgs{}, true},
		{"invalid name", []string{"BAD-NAME", "--", "run"}, execArgs{}, true},
		{"invalid name in list", []string{"A,BAD-NAME", "--", "run"}, execArgs{}, true},
		{"two positional names", []string{"A", "B", "--", "run"}, execArgs{}, true},
		{"empty name in list", []string{"A,", "--", "run"}, execArgs{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseExecArgs(tt.argv)
			if tt.err {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !execArgsEqual(got, tt.want) {
				t.Fatalf("parseExecArgs(%v) = %+v, want %+v", tt.argv, got, tt.want)
			}
		})
	}
}

func execArgsEqual(a, b execArgs) bool {
	if a.dest != b.dest {
		return false
	}
	if !strSliceEqual(a.names, b.names) || !strSliceEqual(a.command, b.command) {
		return false
	}
	return true
}

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// withCapturedExec swaps execFn for one that records its call and does NOT
// replace the process, so runExec returns normally under test.
type execCapture struct {
	called bool
	argv0  string
	argv   []string
	env    []string
}

func withCapturedExec(t *testing.T) *execCapture {
	t.Helper()
	cap := &execCapture{}
	orig := execFn
	execFn = func(argv0 string, argv []string, env []string) error {
		cap.called = true
		cap.argv0 = argv0
		cap.argv = argv
		cap.env = env
		return nil
	}
	t.Cleanup(func() { execFn = orig })
	return cap
}

func envValue(env []string, name string) (string, bool) {
	prefix := name + "="
	// last occurrence wins, matching libc getenv-after-overlay intent
	val, ok := "", false
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			val = strings.TrimPrefix(kv, prefix)
			ok = true
		}
	}
	return val, ok
}

func TestRunExecInjectsValueIntoChildEnvOnly(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-ant-SUPERSECRETVALUE-9999"
	writeEnv(t, filepath.Join(dir, ".env"), "TYPESAFE_API_KEY="+secret+"\nOTHER=keep\n")

	cap := withCapturedExec(t)
	var out, errb bytes.Buffer
	code := runExec(dir, []string{"TYPESAFE_API_KEY", "--", "node", "run.mjs"}, &out, &errb)

	if code != 0 {
		t.Fatalf("exit = %d (%s), want 0", code, errb.String())
	}
	if !cap.called {
		t.Fatal("exec was never called")
	}
	if got, ok := envValue(cap.env, "TYPESAFE_API_KEY"); !ok || got != secret {
		t.Fatalf("child env TYPESAFE_API_KEY = %q (ok=%v), want %q", got, ok, secret)
	}
	if !strSliceEqual(cap.argv, []string{"node", "run.mjs"}) {
		t.Fatalf("child argv = %v, want [node run.mjs]", cap.argv)
	}
	assertNoSecret(t, secret, out.Bytes(), errb.Bytes(), []byte(strings.Join(cap.argv, " ")))
}

func TestRunExecMultiVar(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, filepath.Join(dir, ".env"), "A=avalue\nB=bvalue\nC=cvalue\n")

	cap := withCapturedExec(t)
	var out, errb bytes.Buffer
	code := runExec(dir, []string{"A,C", "--", "run"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d (%s), want 0", code, errb.String())
	}
	if v, ok := envValue(cap.env, "A"); !ok || v != "avalue" {
		t.Fatalf("A = %q ok=%v", v, ok)
	}
	if v, ok := envValue(cap.env, "C"); !ok || v != "cvalue" {
		t.Fatalf("C = %q ok=%v", v, ok)
	}
	// B was not requested; it must not be injected (only inherited env passes,
	// and the test process has no B).
	if _, ok := envValue(cap.env, "B"); ok {
		t.Fatalf("B was injected but not requested")
	}
	assertNoSecret(t, "avalue", out.Bytes(), errb.Bytes())
	assertNoSecret(t, "cvalue", out.Bytes(), errb.Bytes())
}

func TestRunExecMissingVarIsStrictError(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, filepath.Join(dir, ".env"), "PRESENT=1\n")

	cap := withCapturedExec(t)
	var out, errb bytes.Buffer
	code := runExec(dir, []string{"ABSENT_KEY", "--", "run"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a missing var", code)
	}
	if cap.called {
		t.Fatal("exec ran despite a missing var; must not launch")
	}
	if !strings.Contains(errb.String(), "ABSENT_KEY") {
		t.Fatalf("error %q should name the missing KEY", errb.String())
	}
}

func TestRunExecMissingFileIsStrictError(t *testing.T) {
	dir := t.TempDir() // no .env
	cap := withCapturedExec(t)
	var out, errb bytes.Buffer
	code := runExec(dir, []string{"ANY", "--", "run"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if cap.called {
		t.Fatal("exec ran with no .env")
	}
}

func TestRunExecToleratesSurroundingQuotes(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, filepath.Join(dir, ".env"), "DQ=\"double\"\nSQ='single'\nBARE=bare\n")
	cap := withCapturedExec(t)
	var out, errb bytes.Buffer
	code := runExec(dir, []string{"DQ,SQ,BARE", "--", "run"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, errb.String())
	}
	for name, want := range map[string]string{"DQ": "double", "SQ": "single", "BARE": "bare"} {
		if v, ok := envValue(cap.env, name); !ok || v != want {
			t.Fatalf("%s = %q ok=%v, want %q", name, v, ok, want)
		}
	}
}

func TestRunExecRejectsDestOutsideCwd(t *testing.T) {
	dir := t.TempDir()
	cap := withCapturedExec(t)
	var out, errb bytes.Buffer
	code := runExec(dir, []string{"KEY", "--dest", "../escape/.env", "--", "run"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for dest outside cwd", code)
	}
	if cap.called {
		t.Fatal("exec ran with an out-of-cwd dest")
	}
}

func TestRunExecUsageErrorsExit2(t *testing.T) {
	dir := t.TempDir()
	cap := withCapturedExec(t)
	for _, argv := range [][]string{
		{"KEY"},       // no boundary
		{"KEY", "--"}, // no command
		{"--", "run"}, // no names
		{"BAD-NAME", "--", "run"},
	} {
		var out, errb bytes.Buffer
		if code := runExec(dir, argv, &out, &errb); code != 2 {
			t.Fatalf("runExec(%v) = %d, want 2", argv, code)
		}
	}
	if cap.called {
		t.Fatal("exec ran on a usage error")
	}
}

func writeEnv(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
}

// assertNoSecret is the hard invariant: no secret value may appear in any
// operator-facing output (stdout, stderr, or the child argv).
func assertNoSecret(t *testing.T, secret string, streams ...[]byte) {
	t.Helper()
	for _, s := range streams {
		if bytes.Contains(s, []byte(secret)) {
			t.Fatalf("secret value %q leaked into output: %q", secret, s)
		}
	}
}
