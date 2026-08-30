package proj

import (
	"strings"
	"time"
)

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

const activeWithinSecs = 30 // pane_activity newer than this ⇒ "working"

// classifyState maps (kind, bell, activity-age) to a coarse state.
func classifyState(kind string, bell bool, ageSecs int64) string {
	if kind == "" {
		return ""
	}
	if kind == "shell" {
		return "idle"
	}
	switch {
	case bell:
		return "waiting"
	case ageSecs >= 0 && ageSecs <= activeWithinSecs:
		return "working"
	default:
		return "idle"
	}
}

// AgentIn returns the agent kind and coarse state of session's main pane.
//
// kind ∈ {pi, claude, cursor, shell, ""}; state ∈ {working, waiting, idle, ""}.
// A shell or unknown pane is never "working"; a bell flag surfaces as
// "waiting", recent activity as "working", otherwise "idle".
func AgentIn(socket, session string) (kind, state string) {
	paneID, cmd, activity := paneMain(socket, session)
	kind = classifyAgent(cmd)
	if kind == "" || kind == "shell" {
		return kind, classifyState(kind, false, -1)
	}
	bell := Query(socket, paneID, "#{window_bell_flag}") == "1"
	age := int64(-1)
	if activity > 0 {
		age = time.Now().Unix() - activity
	}
	return kind, classifyState(kind, bell, age)
}

// paneMain returns the main pane's id, current command, and pane_activity
// epoch — the leftmost pane, ties broken by tallest.
func paneMain(socket, session string) (id, cmd string, activity int64) {
	out, err := Run(socket, "list-panes", "-t", session, "-F",
		"#{pane_left} #{pane_height} #{pane_id} #{pane_activity} #{pane_current_command}")
	if err != nil {
		return "", "", 0
	}
	bestLeft, bestH := 1<<30, -1
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 5 {
			continue
		}
		l, h := atoi(f[0]), atoi(f[1])
		if l < bestLeft || (l == bestLeft && h > bestH) {
			bestLeft, bestH = l, h
			id, activity, cmd = f[2], int64(atoi(f[3])), f[4]
		}
	}
	return id, cmd, activity
}
