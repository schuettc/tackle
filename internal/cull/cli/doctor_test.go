package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func doctorEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("PATH", t.TempDir()) // no python3, node
	return t.TempDir()
}

func TestDoctorReportsEachMissingItem(t *testing.T) {
	root := doctorEnv(t)
	ckWriteFile(t, root, "test_a.py", "def test_a():\n    pass\n")
	code, out, _ := run(t, "", "doctor", root)
	if code != 1 {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, want := range []string{"key", "cull init", "egress", "python3", "test command", "serve", "~/.claude.json", "channels.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDoctorNeverPrintsKey(t *testing.T) {
	root := doctorEnv(t)
	t.Setenv("TYPESAFE_API_KEY", "ts-fake-env-9999")
	_, out, errw := run(t, "", "doctor", root)
	if strings.Contains(out+errw, "ts-fake-env-9999") {
		t.Errorf("doctor printed the key:\n%s", out)
	}
	if !strings.Contains(out, "environment") {
		t.Errorf("source not shown:\n%s", out)
	}

	t.Setenv("TYPESAFE_API_KEY", "")
	if err := os.MkdirAll(filepath.Join(os.Getenv("CULL_HOME"), "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("CULL_HOME"), "config", "key"), []byte("ts-fake-file-8888\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, errw = run(t, "", "doctor", root)
	if strings.Contains(out+errw, "ts-fake-file-8888") {
		t.Errorf("doctor printed the file key:\n%s", out)
	}
}

func TestDoctorAllOk(t *testing.T) {
	root := doctorEnv(t)
	bin := t.TempDir()
	for _, n := range []string{"python3"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("TYPESAFE_API_KEY", "ts-fake-0000")
	ckWriteFile(t, root, ".cull.toml", "egress = true\n")
	ckWriteFile(t, root, "go.mod", "module x\n")
	ckWriteFile(t, root, "a_test.go", "package x\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n")
	home := os.Getenv("HOME")
	ckWriteFile(t, home, ".claude.json", `{"numStartups":412,"theme":"dark","tipsHistory":[1,2],"mcpServers":{"galley":{"command":"galley"},"cull":{"command":"cull"}}}`)
	ckWriteFile(t, home, ".pi/agent/channels.json", `{"channelServers":{"galley":{"command":"galley"},"cull":{"command":"cull","args":["channel"]}}}`)
	code, out, _ := run(t, "", "doctor", root)
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	if strings.Contains(out, "is not in") {
		t.Errorf("a registered channel reported missing:\n%s", out)
	}
	if strings.Contains(out, "ts-fake-0000") {
		t.Error("key printed")
	}
}

func okHome(t *testing.T, root string) {
	t.Helper()
	t.Setenv("TYPESAFE_API_KEY", "ts-fake-0000")
	ckWriteFile(t, root, ".cull.toml", "egress = true\n")
}

func TestDoctorGoOnlyNeedsNoPython(t *testing.T) {
	root := doctorEnv(t) // PATH has no python3
	okHome(t, root)
	ckWriteFile(t, root, "go.mod", "module x\n")
	ckWriteFile(t, root, "a_test.go", "package x\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n")
	code, out, _ := run(t, "", "doctor", root)
	if code != 0 || strings.Contains(out, "python3") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestDoctorPythonProjectNeedsPython(t *testing.T) {
	root := doctorEnv(t)
	okHome(t, root)
	ckWriteFile(t, root, "test_a.py", "def test_a():\n    pass\n")
	code, out, _ := run(t, "", "doctor", root)
	if code != 1 || !strings.Contains(out, "python3") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestDoctorSuiteErrorIsItsOwnLine(t *testing.T) {
	root := doctorEnv(t)
	okHome(t, root)
	ckWriteFile(t, root, "go.mod", "module x\n")
	ckWriteFile(t, root, "a_test.go", "package x\n")
	if err := os.Mkdir(filepath.Join(root, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o755) })
	code, out, _ := run(t, "", "doctor", root)
	if code != 1 || !strings.Contains(out, "tests") || strings.Contains(out, "no tests found") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

// pi's channels.json keeps its servers under channelServers (channels.tools),
// and doctor reports cull present or absent there.
func TestDoctorReadsPiChannelServers(t *testing.T) {
	root := doctorEnv(t)
	okHome(t, root)
	ckWriteFile(t, root, "go.mod", "module x\n")
	ckWriteFile(t, root, "a_test.go", "package x\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n")
	home := os.Getenv("HOME")
	ckWriteFile(t, home, ".pi/agent/channels.json", `{"channelServers":{"cull":{"command":"cull","args":["channel"]}}}`)
	_, out, _ := run(t, "", "doctor", root)
	if strings.Contains(out, "cull is not in ~/.pi/agent/channels.json") {
		t.Fatalf("registered cull reported missing:\n%s", out)
	}
	ckWriteFile(t, home, ".pi/agent/channels.json", `{"channelServers":{"galley":{"command":"galley"}}}`)
	_, out, _ = run(t, "", "doctor", root)
	if !strings.Contains(out, "cull is not in ~/.pi/agent/channels.json") {
		t.Fatalf("missing cull not reported:\n%s", out)
	}
}

// TypeScript tests with no node_modules/typescript at the project root are
// skipped by cull check, so doctor notes it rather than failing.
func TestDoctorMissingTypeScriptIsANote(t *testing.T) {
	root := doctorEnv(t)
	okHome(t, root)
	bin := t.TempDir()
	for _, n := range []string{"node", "python3"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	ckWriteFile(t, root, "test_a.py", "def test_a():\n    pass\n")
	ckWriteFile(t, root, "infra/a.test.ts", "test('a', () => {});\n")
	ckWriteFile(t, root, "infra/package.json", `{"scripts":{"test":"jest"}}`)
	code, out, _ := run(t, "", "doctor", root)
	if code != 0 {
		t.Fatalf("code %d (missing TypeScript must not be required)\n%s", code, out)
	}
	if !strings.Contains(out, "TypeScript tests will be skipped") {
		t.Errorf("no note about skipped TypeScript tests:\n%s", out)
	}
}

// The test command line names the command and counts the files instead of
// listing every one (it was 1.4 MB on a real project).
func TestDoctorTestCommandCountsFiles(t *testing.T) {
	root := doctorEnv(t)
	okHome(t, root)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, n := range []string{"a", "b", "c"} {
		ckWriteFile(t, root, "tests/test_"+n+".py", "def test_x():\n    pass\n")
	}
	_, out, _ := run(t, "", "doctor", root)
	var tc string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "test command") {
			tc = l
		}
	}
	if !strings.Contains(tc, "(3 test files)") || strings.Contains(tc, "test_a.py") {
		t.Fatalf("test command line = %q", tc)
	}
}

func TestTruncatedArgvStaysValidUTF8(t *testing.T) {
	root := doctorEnv(t)
	if err := os.MkdirAll(root+"/.github/workflows", 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := "go test ./... -run '" + strings.Repeat("é", 120) + "'"
	wf := "on: push\njobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n      - run: " + cmd + "\n"
	if err := os.WriteFile(root+"/.github/workflows/ci.yml", []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := run(t, "", "doctor", root)
	if !utf8.ValidString(out) {
		t.Errorf("doctor output is not valid UTF-8:\n%q", out)
	}
}
