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

// AgentOption is the tmux user-option proj writes to record the agent a session
// was launched with. `proj list` prefers it over pane process-name detection,
// so a session whose foreground process is not the agent (e.g. hail running the
// shim under --command) still reports the right agent.
func AgentOption() string { return "@proj_agent" }

// runner is the tmux exec seam for EnsureSession's option/query/pane commands;
// tests override it to capture the exact argv. It defaults to Run.
var runner = Run

// newSessionHome creates the detached session from $HOME (so both the server
// cwd and #{session_path} are $HOME — the cwd-poison defense). It is a seam so
// tests can drive EnsureSession's create path without a real tmux server.
var newSessionHome = func(socket, name string) error {
	c := exec.Command("tmux", "-L", socket, "new-session", "-d", "-s", name)
	c.Dir = os.Getenv("HOME")
	return c.Run()
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
// EnsureSession makes the tmux session `name` exist on `socket`. It reports
// whether it created the session (created=false when has-session already
// succeeded, i.e. the session was reused).
//
// When command is non-empty and the session is newly created, the command is
// run directly in the working pane via `respawn-pane -k -c dir -- command…`
// (no shell, no send-keys). When command is empty, `agent` is launched by
// typing its command into the pane (the historical behaviour). Either way, the
// chosen agent (if any) is recorded in the AgentOption user option so
// `proj list` reports it regardless of the pane's foreground process.
//
// A reused session is left untouched: neither command nor agent is re-applied,
// so proj never clobbers a pane the user is working in.
func EnsureSession(socket, name, dir, agent string, command []string) (created bool, err error) {
	if _, err := runner(socket, "has-session", "-t", "="+name); err == nil {
		return false, nil // exists → reuse
	}
	// Create from $HOME (no -c) so the server cwd and #{session_path} are both
	// $HOME and never pin to a dir that may later be deleted (worktree).
	if err := newSessionHome(socket, name); err != nil {
		return false, err
	}
	// Move the pane into dir without touching #{session_path}. -k restarts the
	// pane in dir; with a command it runs that command directly instead of a
	// shell. #{session_path} stays $HOME (set at new-session), so the
	// session_path==$HOME invariant holds.
	respawn := []string{"respawn-pane", "-k", "-t", "=" + name + ":", "-c", dir}
	if len(command) > 0 {
		respawn = append(respawn, "--")
		respawn = append(respawn, command...)
	}
	if _, err := runner(socket, respawn...); err != nil {
		return false, err
	}
	// label = the work segment (after the last '/'), for muster.
	label := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		label = name[i+1:]
	}
	_, _ = runner(socket, "set-option", "-t", name, LabelOption(), label)

	if agent != "" && agent != "none" {
		_, _ = runner(socket, "set-option", "-t", name, AgentOption(), agent)
		// Only type an agent launch command when no explicit command was given;
		// with a command the pane already runs it directly.
		if len(command) == 0 {
			if cmd := agentLaunchCmd(agent, name); cmd != "" {
				// target "=name:" — trailing colon resolves the active pane on tmux 3.7+.
				_, _ = runner(socket, "send-keys", "-t", "="+name+":", cmd, "Enter")
			}
		}
	}
	return true, nil
}

// KillSession terminates the tmux session `name` on `socket`.
func KillSession(socket, name string) error {
	_, err := Run(socket, "kill-session", "-t", "="+name)
	return err
}

// CurrentSessionName returns the name of the tmux session this process is
// attached to (via $TMUX), or "" when not inside tmux. Used to refuse reaping
// the session hosting the picker.
func CurrentSessionName() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	name, err := Run("", "display-message", "-p", "#S")
	if err != nil {
		return ""
	}
	return name
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
