package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sleepyTest = "package pkg\n\nimport \"time\"\n\nfunc TestA(t *testing.T) {\n\ttime.Sleep(2 * time.Second)\n}\n"

func TestCheckDryRunPrintsSpeedDigestToStderr(t *testing.T) {
	checkEnv(t, "")
	f := &checkFake{}
	f.start(t)
	root := t.TempDir()
	ckGoModule(t, root)
	ckWriteFile(t, root, "pkg/calc_test.go", sleepyTest)
	code, out, errw := run(t, "", "check", root, "--dry-run")
	if code != 0 {
		t.Fatalf("code %d, errw %q", code, errw)
	}
	if !strings.Contains(errw, "pkg/calc_test.go:6  fixed_wait") || !strings.Contains(errw, "2s") {
		t.Errorf("errw = %q, want the digest", errw)
	}
	if strings.Contains(out, "fixed_wait") {
		t.Errorf("digest on stdout in dry-run: %q", out)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests during --dry-run", f.n.Load())
	}
}

func TestCheckTablePrintsSpeedDigestAfterIt(t *testing.T) {
	checkEnv(t, "k")
	(&checkFake{}).start(t)
	root := t.TempDir()
	ckGoModule(t, root)
	ckEgress(t, root)
	ckWriteFile(t, root, "pkg/calc_test.go", sleepyTest)
	code, out, errw := run(t, "", "check", root)
	if code != 0 {
		t.Fatalf("code %d, errw %q", code, errw)
	}
	i, j := strings.Index(out, "keep"), strings.Index(out, "pkg/calc_test.go:6  fixed_wait")
	if i < 0 || j < i {
		t.Errorf("out:\n%s", out)
	}
}

func TestDoctorListsChecks(t *testing.T) {
	root := doctorEnv(t)
	okHome(t, root)
	ckWriteFile(t, root, "go.mod", "module x\n")
	ckWriteFile(t, root, "a_test.go", "package x\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n")
	ckWriteFile(t, root, ".github/workflows/ci.yml",
		"name: ci\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n      - run: go test ./...\n      - uses: some/go-ci@v1\n")
	code, out, _ := run(t, "", "doctor", root)
	if !strings.Contains(out, "checks") || !strings.Contains(out, "go  .  go test ./...") {
		t.Errorf("no go check listed:\n%s", out)
	}
	if !strings.Contains(out, "unknown") || !strings.Contains(out, "some/go-ci@v1") {
		t.Errorf("the unknown is not listed with its note:\n%s", out)
	}
	_ = code
}

func TestDoctorTypeScriptNoteGoesWhenEachTestHasOne(t *testing.T) {
	root := doctorEnv(t)
	okHome(t, root)
	bin := t.TempDir()
	for _, n := range []string{"node"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	ckWriteFile(t, root, "infra/a.test.ts", "test('a', () => {});\n")
	ckWriteFile(t, root, "infra/node_modules/typescript/package.json", "{}")
	_, out, _ := run(t, "", "doctor", root)
	if strings.Contains(out, "will be skipped") {
		t.Errorf("note about skipped TypeScript tests although infra has typescript:\n%s", out)
	}
}
