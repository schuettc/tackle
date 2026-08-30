package proj

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SocketFor returns the per-project tmux socket name for project.
func SocketFor(project string) string { return "proj-" + project }

// socketDir returns the directory tmux stores its per-uid sockets in,
// honoring $TMUX_TMPDIR (defaulting to /tmp), matching tmux's own convention.
func socketDir() string {
	if d := os.Getenv("TMUX_TMPDIR"); d != "" {
		return filepath.Join(d, fmt.Sprintf("tmux-%d", os.Getuid()))
	}
	return filepath.Join("/tmp", fmt.Sprintf("tmux-%d", os.Getuid()))
}

// Servers returns the "proj-*" socket names that have at least one live
// session. Stale sockets (no running server) are skipped because list-sessions
// fails against them.
func Servers() []string {
	entries, err := os.ReadDir(socketDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "proj-") {
			continue
		}
		if _, err := Run(name, "list-sessions"); err == nil {
			out = append(out, name)
		}
	}
	return out
}

// CurrentServer returns the socket base name of the tmux server hosting the
// current pane, or "" outside tmux.
func CurrentServer() string {
	sock := SocketFromEnv()
	if sock == "" {
		return ""
	}
	return filepath.Base(sock)
}

// FindServer returns the socket hosting a session named exactly name.
func FindServer(name string) (string, bool) {
	for _, s := range Servers() {
		if _, err := Run(s, "has-session", "-t", "="+name); err == nil {
			return s, true
		}
	}
	return "", false
}
