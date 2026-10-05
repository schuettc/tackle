// Package sifttest gives sift tests hermetic git (no user or system config, a
// fixed identity) and a fake home with harness directories, following
// casebook's testgit.
package sifttest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Env isolates git for the rest of the test.
func Env(t testing.TB) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n"), 0o600); err != nil {
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
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

// Home points HOME at a fresh temp dir with no harness variables set, and
// returns it.
func Home(t testing.TB) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
	return h
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

// Write writes file (relative to dir), creating its directories.
func Write(t testing.TB, dir, file, content string) string {
	t.Helper()
	p := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Repo creates a repo at dir (made if needed) on main, with files committed
// in one commit (none: an empty commit).
func Repo(t testing.TB, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "init", "-q", "-b", "main")
	Commit(t, dir, files)
	return dir
}

// Commit writes files and commits them (none: an empty commit).
func Commit(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for f, c := range files {
		Write(t, dir, f, c)
		Git(t, dir, "add", "--", f)
	}
	Git(t, dir, "commit", "-q", "--allow-empty", "-m", "update")
}

// Publish gives the repo at dir a bare origin, pushes main to it and sets
// origin/HEAD, as a clone would have.
func Publish(t testing.TB, dir string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	Git(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	Git(t, dir, "remote", "add", "origin", bare)
	Git(t, dir, "push", "-q", "-u", "origin", "main")
	Git(t, dir, "remote", "set-head", "origin", "main")
	return bare
}
