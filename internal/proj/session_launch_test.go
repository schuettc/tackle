package proj

import (
	"os"
	"path/filepath"
	"testing"
)

// stubBins creates executable stubs for each named binary in a fresh dir and
// points $PATH at only that dir, so hasBin/exec.LookPath is deterministic and
// independent of what the test machine happens to have installed.
func stubBins(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", n, err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestAgentLaunchCmd(t *testing.T) {
	stubBins(t, "claude", "pi", "cursor-agent")

	cases := []struct {
		agent, name, want string
	}{
		// The trailing `--` is load-bearing: without it a bare `claude` opens
		// agent view instead of starting a session. It must survive refactors.
		{"claude", "repo/nfl-4", "claude --name 'repo/nfl-4' --"},
		{"pi", "repo/nfl-4", "pi --name 'repo/nfl-4'"},
		{"cursor", "repo/nfl-4", "cursor-agent"},
		{"none", "repo/nfl-4", ""},
		{"", "repo/nfl-4", ""},
	}
	for _, c := range cases {
		if got := agentLaunchCmd(c.agent, c.name); got != c.want {
			t.Errorf("agentLaunchCmd(%q,%q) = %q, want %q", c.agent, c.name, got, c.want)
		}
	}
}

// A name with shell metacharacters must be single-quoted so it survives being
// typed into the pane as ONE argument — the same guarantee zsh's ${(qq)n} gives.
func TestAgentLaunchCmdQuotesName(t *testing.T) {
	stubBins(t, "claude")
	got := agentLaunchCmd("claude", "a'b c")
	want := `claude --name 'a'\''b c' --`
	if got != want {
		t.Errorf("agentLaunchCmd quoting = %q, want %q", got, want)
	}
}

// When the agent binary is not on PATH, the launcher degrades to a plain shell
// (empty command) rather than typing a command that would fail in the pane.
func TestAgentLaunchCmdMissingBinDegrades(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir: nothing resolvable
	for _, agent := range []string{"claude", "pi", "cursor"} {
		if got := agentLaunchCmd(agent, "x"); got != "" {
			t.Errorf("agentLaunchCmd(%q) with no bin = %q, want \"\"", agent, got)
		}
	}
}
