// Package testgit gives ledger tests hermetic git: no user or system config,
// a fixed identity, and no inherited harness or ledger variables.
package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Env isolates git and the ledger's environment for the rest of the test.
func Env(t testing.TB) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.invalid",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.invalid",
	} {
		t.Setenv(k, v)
	}
	for _, k := range []string{"CLAUDE_CODE_SESSION_ID", "AGENT_SESSION_ID", "AGENT_SESSION_CHILD",
		"LEDGER_INTERNAL", "LEDGER_DISABLE", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// NewRepo creates a temp repo on main with one commit.
func NewRepo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	Commit(t, dir, "README", "hello\n")
	return dir
}

// NewBare creates a temp bare repo.
func NewBare(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	Git(t, filepath.Dir(dir), "init", "-q", "--bare", "-b", "main", dir)
	return dir
}

// Commit writes file (relative to dir), commits it and returns the SHA.
func Commit(t testing.TB, dir, file, content string) string {
	t.Helper()
	p := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "add", "--", file)
	Git(t, dir, "commit", "-q", "-m", "update "+file)
	return Git(t, dir, "rev-parse", "HEAD")
}
