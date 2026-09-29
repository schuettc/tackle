package proj

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// newWindowScript opens a Ghostty window running argv[1] and prints its id, so
// later tabs can be placed in it from separate osascript calls. Record-literal
// configuration: `set command of cfg` does not parse (command is a keyword).
const newWindowScript = `on run argv
tell application "Ghostty"
set w to new window with configuration {command:(item 1 of argv)}
return id of w
end tell
end run`

// newTabScript adds a tab running argv[1] to the window whose id is argv[2].
const newTabScript = `on run argv
tell application "Ghostty"
set w to first window whose id is (item 2 of argv)
new tab in w with configuration {command:(item 1 of argv)}
end tell
end run`

// hasClient reports whether a tmux client is attached to the session; a seam
// for tests.
var hasClient = func(socket, name string) bool {
	out, err := Run(socket, "list-clients", "-t", "="+name)
	return err == nil && strings.TrimSpace(out) != ""
}

// attachPoll is the wait between attach checks after opening a tab (ten
// checks per tab, as tmux-reopen did).
var attachPoll = 500 * time.Millisecond

// attachCommand is what each Ghostty surface runs: attach to exactly this
// session on its own server. quoteTarget keeps the `=` exact-match prefix
// inside quotes, because Ghostty runs the command through the login shell.
func attachCommand(s SavedSession) string {
	return "tmux -L " + s.Socket + " attach -t " + quoteTarget(s.Name)
}

// GhosttyOpen opens each group as one Ghostty window, its first session in
// the new window and the rest as tabs in it. A session that does not attach
// is reported and the rest still open; only a failure to create a window
// aborts, since every later tab would fail the same way.
func GhosttyOpen(groups [][]SavedSession, out io.Writer) error {
	for _, g := range groups {
		var win string
		for i, s := range g {
			if i == 0 {
				id, err := osascript(newWindowScript, attachCommand(s))
				if err != nil {
					return fmt.Errorf("open window for %s: %w", s.Name, err)
				}
				win = strings.TrimSpace(id)
			} else if _, err := osascript(newTabScript, attachCommand(s), win); err != nil {
				_, _ = fmt.Fprintf(out, "WARN %s: open tab: %v\n", s.Name, err)
				continue
			}
			if !waitAttached(s) {
				_, _ = fmt.Fprintf(out, "WARN %s did not attach\n", s.Name)
			}
		}
	}
	return nil
}

func waitAttached(s SavedSession) bool {
	for i := 0; i < 10; i++ {
		if hasClient(s.Socket, s.Name) {
			return true
		}
		time.Sleep(attachPoll)
	}
	return false
}
