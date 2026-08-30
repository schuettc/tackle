package proj

import (
	"os"
	"os/exec"
	"strings"
)

// LabelOption is the tmux user-option muster reads for a session's work label.
// Overridable via $MUSTER_LABEL_OPTION; defaults to "@claude_task".
func LabelOption() string {
	if v := os.Getenv("MUSTER_LABEL_OPTION"); v != "" {
		return v
	}
	return "@claude_task"
}

// EnsureSession makes the tmux session `name` exist on `socket`, launching
// `agent` into its pane. It is a no-op if the session already exists.
//
// The session is created from $HOME (cmd.Dir) with NO -c flag, so both the tmux
// server's permanent cwd AND the session's own start path (#{session_path}) are
// $HOME. This is the cwd-poison defense: neither the server nor the session
// ever pins to a directory that may later be deleted (e.g. a worktree).
//
// The working pane is then moved into `dir` with `respawn-pane -c dir`, which
// sets #{pane_current_path} to dir while leaving #{session_path} at $HOME.
//
// NOTE (machine-verified, tmux 3.7b): the naive `new-session -c dir` the source
// brief prescribed sets #{session_path} to dir, not $HOME, which would violate
// the session_path==$HOME contract. respawn-pane -c is what keeps both the
// session_path==$HOME and pane_current_path==dir contracts true. See
// task-6-report.md.
func EnsureSession(socket, name, dir, agent string) error {
	if _, err := Run(socket, "has-session", "-t", "="+name); err == nil {
		return nil // exists
	}
	// Create from $HOME (no -c) so the server cwd and #{session_path} are both
	// $HOME and never pin to a dir that may later be deleted (worktree).
	c := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", name)
	c.Dir = os.Getenv("HOME")
	if err := c.Run(); err != nil {
		return err
	}
	// Move the pane into dir without touching #{session_path}. -k restarts the
	// (freshly-created) shell in dir; harmless this early.
	_, _ = Run(socket, "respawn-pane", "-k", "-t", "="+name+":", "-c", dir)
	// label = the work segment (after the last '/'), for muster.
	label := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		label = name[i+1:]
	}
	_, _ = Run(socket, "set-option", "-t", name, LabelOption(), label)

	if agent != "" && agent != "none" {
		if cmd := agentLaunchCmd(agent, name); cmd != "" {
			// target "=name:" — trailing colon resolves the active pane on tmux 3.7+.
			_, _ = Run(socket, "send-keys", "-t", "="+name+":", cmd, "Enter")
		}
	}
	return nil
}

// agentLaunchCmd returns the shell command to type into the pane, or "" if the
// agent binary is absent (degradation → plain shell).
//
// The argument shape mirrors the zsh __pi_launch_cmd / __claude_launch_cmd in
// ~/dotfiles/config/zsh/04-aliases.zsh, which bake load-bearing flags:
//   - pi:     `pi --name <session>` (--name sets pi's session display name so
//     its identity matches the pane; no trailing `--` needed).
//   - claude: `claude --name <session> --` (the trailing `--` is load-bearing:
//     a bare `claude` opens agent view rather than starting a session; --name
//     carries the conversation display name).
//   - cursor: bare `cursor-agent` (no zsh analog carries flags).
//
// The session name is quoted the same way zsh's ${(qq)n} does, guarding names
// with shell metacharacters when typed into the pane.
func agentLaunchCmd(agent, name string) string {
	switch agent {
	case "pi":
		if !hasBin("pi") {
			return ""
		}
		return "pi --name " + shellQuote(name)
	case "claude":
		if !hasBin("claude") {
			return ""
		}
		return "claude --name " + shellQuote(name) + " --"
	case "cursor":
		if !hasBin("cursor-agent") {
			return ""
		}
		return "cursor-agent"
	default:
		return ""
	}
}

// hasBin reports whether bin is resolvable on $PATH.
func hasBin(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// shellQuote single-quotes s for safe use in the typed pane command, matching
// zsh's ${(qq)s}: wrap in single quotes and escape embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
