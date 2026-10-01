// Package testgit gives casebook tests hermetic git: no user or system config,
// a fixed identity, and no inherited harness or casebook variables.
package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Env isolates git and the casebook's environment for the rest of the test.
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
		"CASEBOOK_INTERNAL", "CASEBOOK_DISABLE", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
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

// RebaseHold holds a clone's next rebase: Started waits until a rebase has
// reached its first checkout (the working tree is the upstream's commits,
// without the local ones), and Release lets it go on.
type RebaseHold struct {
	t    testing.TB
	gate string
}

// HoldRebase installs a post-checkout hook in the clone at dir that, during
// a rebase only, touches <gate>/started and waits (up to 30 s) for
// <gate>/release. The test's end releases it.
func HoldRebase(t testing.TB, dir string) *RebaseHold {
	t.Helper()
	h := &RebaseHold{t: t, gate: t.TempDir()}
	hook := filepath.Join(dir, ".git", "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
[ -d .git/rebase-merge ] || [ -d .git/rebase-apply ] || exit 0
touch '` + h.gate + `/started'
i=0
while [ ! -f '` + h.gate + `/release' ] && [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done
exit 0
`
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Release)
	return h
}

// Started waits (up to 10 s) for the held rebase to begin.
func (h *RebaseHold) Started() {
	h.t.Helper()
	p := filepath.Join(h.gate, "started")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	h.t.Fatal("the held rebase never started")
}

// IsStarted reports whether the held rebase has begun.
func (h *RebaseHold) IsStarted() bool {
	_, err := os.Stat(filepath.Join(h.gate, "started"))
	return err == nil
}

// Release lets the held rebase (and any later one) go on.
func (h *RebaseHold) Release() {
	_ = os.WriteFile(filepath.Join(h.gate, "release"), nil, 0o644)
}
