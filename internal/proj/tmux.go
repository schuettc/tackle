package proj

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes tmux against an explicit socket ("" = default server), trimming
// one trailing newline. Any error is returned to callers that care; most use
// Query, which swallows it.
func Run(socket string, args ...string) (string, error) {
	full := args
	if socket != "" {
		full = append([]string{"-L", socket}, args...)
	}
	out, err := exec.Command("tmux", full...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

// Query runs `display-message -p -t <target> <format>` on socket and returns
// the value, or "" on any failure (dead socket, missing session, no tmux).
func Query(socket, target, format string) string {
	if socket == "" || target == "" {
		return ""
	}
	out, err := Run(socket, "display-message", "-p", "-t", target, format)
	if err != nil {
		return ""
	}
	return out
}

// SocketFromEnv returns the tmux socket path from $TMUX ("<sock>,<pid>,<idx>"),
// symlinks resolved, or "" outside tmux.
func SocketFromEnv() string {
	t := os.Getenv("TMUX")
	if t == "" {
		return ""
	}
	p := strings.SplitN(t, ",", 2)[0]
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
