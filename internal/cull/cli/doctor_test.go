package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	ckWriteFile(t, home, ".claude.json", `{"mcpServers":{"cull":{"command":"cull"}}}`)
	ckWriteFile(t, home, ".pi/agent/channels.json", `{"cull":{"command":"cull"}}`)
	code, out, _ := run(t, "", "doctor", root)
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	if strings.Contains(out, "ts-fake-0000") {
		t.Error("key printed")
	}
}
