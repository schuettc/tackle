package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/key"
)

const initFakeKey = "ts-fake-0000"

// initSeams makes init think stdin is a terminal and answers the key prompt.
func initSeams(t *testing.T, term bool, secret string) {
	t.Helper()
	oldT, oldP := isTerminal, promptSecret
	isTerminal = func(any) bool { return term }
	promptSecret = func() (string, error) { return secret, nil }
	t.Cleanup(func() { isTerminal, promptSecret = oldT, oldP })
	t.Setenv("CULL_HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "")
}

func TestInitRefusesWithoutTerminal(t *testing.T) {
	initSeams(t, false, initFakeKey)
	root := t.TempDir()
	code, _, errw := run(t, "y\n", "init", root)
	if code != 2 || !strings.Contains(errw, "run cull init yourself, in a terminal") {
		t.Fatalf("code %d errw %q", code, errw)
	}
	if _, err := os.Stat(key.Path()); err == nil {
		t.Error("key written without a terminal")
	}
}

func TestInitWritesKeyEgressGitignoreIdempotently(t *testing.T) {
	initSeams(t, true, initFakeKey)
	root := t.TempDir()
	code, out, errw := run(t, "y\n", "init", root)
	if code != 0 {
		t.Fatalf("code %d errw %q", code, errw)
	}
	if strings.Contains(out+errw, initFakeKey) {
		t.Error("init printed the key")
	}
	if k, _, err := key.Load(); err != nil || k != initFakeKey {
		t.Fatalf("key not saved: %v", err)
	}
	toml, _ := os.ReadFile(filepath.Join(root, ".cull.toml"))
	if !strings.Contains(string(toml), "egress = true") {
		t.Errorf(".cull.toml: %q", toml)
	}
	gi, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if strings.Count(string(gi), ".cull/") != 1 {
		t.Errorf(".gitignore: %q", gi)
	}

	// Second run: nothing to ask, nothing duplicated.
	promptSecret = func() (string, error) { t.Fatal("asked for a key already stored"); return "", nil }
	if code, _, errw := run(t, "", "init", root); code != 0 {
		t.Fatalf("second run %d %q", code, errw)
	}
	toml2, _ := os.ReadFile(filepath.Join(root, ".cull.toml"))
	gi2, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if string(toml2) != string(toml) || string(gi2) != string(gi) {
		t.Errorf("not idempotent:\n%q\n%q", toml2, gi2)
	}
}

func TestInitEgressNoLeavesFileUnchanged(t *testing.T) {
	initSeams(t, true, initFakeKey)
	root := t.TempDir()
	orig := "model = \"jev-latest\"\n"
	if err := os.WriteFile(filepath.Join(root, ".cull.toml"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules\n.cull/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errw := run(t, "n\n", "init", root)
	if code != 0 {
		t.Fatalf("code %d %q", code, errw)
	}
	got, _ := os.ReadFile(filepath.Join(root, ".cull.toml"))
	if string(got) != orig {
		t.Errorf(".cull.toml changed: %q", got)
	}
	gi, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if string(gi) != "node_modules\n.cull/\n" {
		t.Errorf(".gitignore changed: %q", gi)
	}
	if _, err := os.Stat(filepath.Join(root, ".cull.toml")); err != nil {
		t.Error(err)
	}
}

func TestInitEgressYesAppendsToExistingToml(t *testing.T) {
	initSeams(t, true, initFakeKey)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".cull.toml"), []byte("model = \"jev-latest\""), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := run(t, "yes\n", "init", root); code != 0 {
		t.Fatalf("code %d %q", code, errw)
	}
	got, _ := os.ReadFile(filepath.Join(root, ".cull.toml"))
	if !strings.Contains(string(got), "model = \"jev-latest\"\negress = true") {
		t.Errorf("got %q", got)
	}
}

// The environment is per-shell; the file is what agents use. Init saves a
// file even when TYPESAFE_API_KEY is set.
func TestInitSavesFileEvenWithEnvKey(t *testing.T) {
	initSeams(t, true, initFakeKey)
	t.Setenv("TYPESAFE_API_KEY", "ts-fake-env-1111")
	root := t.TempDir()
	code, out, errw := run(t, "n\n", "init", root)
	if code != 0 {
		t.Fatalf("code %d %q", code, errw)
	}
	if _, err := os.Stat(key.Path()); err != nil {
		t.Fatalf("no key file: %v", err)
	}
	if !strings.Contains(out, "saved to "+key.Path()) {
		t.Errorf("out %q", out)
	}
}

// At a workspace root (no tests of its own, repositories inside) init saves
// the key but leaves the per-repository question to each repository.
func TestInitAtAWorkspaceRootPointsAtTheRepositories(t *testing.T) {
	initSeams(t, true, initFakeKey)
	ws := t.TempDir()
	for _, name := range []string{"shop", "site"} {
		if err := os.MkdirAll(filepath.Join(ws, name, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errw := run(t, "y\n", "init", ws)
	if code != 0 {
		t.Fatalf("code %d errw %q", code, errw)
	}
	if k, _, err := key.Load(); err != nil || k != initFakeKey {
		t.Fatalf("key not saved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".cull.toml")); err == nil {
		t.Error("wrote .cull.toml at the workspace root")
	}
	if _, err := os.Stat(filepath.Join(ws, ".gitignore")); err == nil {
		t.Error("wrote .gitignore at the workspace root")
	}
	for _, want := range []string{"cull init shop", "cull init site"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
