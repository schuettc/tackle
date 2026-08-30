package proj

import "strings"

// classifyAgent maps a tmux pane_current_command to an agent kind.
//
// The concrete strings were VERIFIED against the live tmux servers on this
// machine (see the task-5 report):
//   - pi presents its pane_current_command as "node" (pi is a node app; its
//     child process is "pi").
//   - Claude Code renames its process to its version string (e.g. "2.1.248"),
//     so a leading "<major>.<minor>" version classifies as claude.
//   - cursor-agent → cursor is ASSUMED; no cursor session was live to observe.
//
// Interactive shells and file managers collapse to "shell"; anything unknown
// returns "".
func classifyAgent(cmd string) string {
	switch cmd {
	case "pi", "node":
		return "pi"
	case "claude":
		return "claude"
	case "cursor-agent", "cursor", "agent":
		return "cursor"
	case "zsh", "bash", "fish", "sh", "yazi", "scratch":
		return "shell"
	case "":
		return ""
	}
	// Claude Code advertises itself via a version-string process name.
	if looksLikeVersion(cmd) {
		return "claude"
	}
	return ""
}

// looksLikeVersion reports whether s is a dotted numeric version such as
// "2.1.248": all digits and dots, at least one dot, not starting with a dot.
func looksLikeVersion(s string) bool {
	dot := false
	for i, r := range s {
		switch {
		case r == '.':
			if i == 0 {
				return false
			}
			dot = true
		case r < '0' || r > '9':
			return false
		}
	}
	return dot
}

// AgentIn returns the agent kind and coarse state of session's main pane.
//
// kind ∈ {pi, claude, cursor, shell, ""}; state ∈ {working, waiting, idle, ""}.
// A shell or unknown pane is never "working"; a bell flag surfaces as
// "waiting", recent activity as "working", otherwise "idle".
func AgentIn(socket, session string) (kind, state string) {
	cmd := paneMainCommand(socket, session)
	kind = classifyAgent(cmd)
	if kind == "" {
		return "", ""
	}
	if kind == "shell" {
		return kind, "idle"
	}
	act := Query(socket, session, "#{pane_activity}")
	bell := Query(socket, session, "#{window_bell_flag}")
	switch {
	case bell == "1":
		state = "waiting"
	case act != "" && act != "0":
		state = "working"
	default:
		state = "idle"
	}
	return kind, state
}

// paneMainCommand returns the pane_current_command of a session's main pane,
// defined as the leftmost pane, breaking ties by tallest. This tracks the
// agent's primary pane even when helper panes are split off to the right.
func paneMainCommand(socket, session string) string {
	out, err := Run(socket, "list-panes", "-t", session, "-F",
		"#{pane_left} #{pane_height} #{pane_current_command}")
	if err != nil {
		return ""
	}
	best, bestLeft, bestH := "", 1<<30, -1
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		l, h := atoi(f[0]), atoi(f[1])
		if l < bestLeft || (l == bestLeft && h > bestH) {
			bestLeft, bestH, best = l, h, f[2]
		}
	}
	return best
}
