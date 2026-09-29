package proj

import (
	"os/exec"
	"strings"
	"time"
)

// Ghostty is driven through its AppleScript dictionary (Ghostty ≥1.3). It
// exposes windows → tabs with titles but no tty, so the tab title is the only
// link from a tab to the tmux session attached in it. tmux's set-titles puts
// the session name first (see ~/.tmux.conf set-titles-string).

// osascript runs an AppleScript, one -e per script line, with args passed to
// its `on run argv` handler. A seam so tests never drive the real Ghostty.
var osascript = func(script string, args ...string) (string, error) {
	var argv []string
	for _, line := range strings.Split(script, "\n") {
		argv = append(argv, "-e", line)
	}
	out, err := exec.Command("osascript", append(argv, args...)...).Output()
	return string(out), err
}

// readTitlesScript prints one "W" line per window and one "T<tab><title>" line
// per tab, in Ghostty's window and tab order. `character id 9` rather than
// `tab`: inside the tell block `tab` names Ghostty's tab class.
const readTitlesScript = `set out to ""
tell application "Ghostty"
repeat with w in windows
set out to out & "W" & linefeed
repeat with t in tabs of w
set out to out & "T" & (character id 9) & (name of t) & linefeed
end repeat
end repeat
end tell
return out`

// ghosttyTitles returns every window's tab titles, in order.
func ghosttyTitles() ([][]string, error) {
	out, err := osascript(readTitlesScript)
	if err != nil {
		return nil, err
	}
	return parseGhosttyTitles(out), nil
}

func parseGhosttyTitles(out string) [][]string {
	var wins [][]string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "W":
			wins = append(wins, []string{})
		case strings.HasPrefix(line, "T\t") && len(wins) > 0:
			wins[len(wins)-1] = append(wins[len(wins)-1], strings.TrimPrefix(line, "T\t"))
		}
	}
	return wins
}

// titleSession extracts the session name a tab title leads with: the
// attention prefixes (🔐 permission, 🔔 bell) are dropped and everything from
// the first " · " (the topic and mail suffixes) is cut.
func titleSession(title string) string {
	title = strings.TrimPrefix(title, "🔐 ")
	title = strings.TrimPrefix(title, "🔔 ")
	if i := strings.Index(title, " · "); i >= 0 {
		title = title[:i]
	}
	return title
}

// MatchLayout maps window/tab titles to live session names. Tabs that name no
// live session (plain shells, other programs, a muster alias leading the
// title) are dropped rather than guessed, then windows left empty are dropped.
func MatchLayout(titles [][]string, live map[string]bool) [][]string {
	var wins [][]string
	for _, w := range titles {
		var tabs []string
		for _, title := range w {
			if s := titleSession(title); live[s] {
				tabs = append(tabs, s)
			}
		}
		if len(tabs) > 0 {
			wins = append(wins, tabs)
		}
	}
	return wins
}

// SaveLayout captures the current Ghostty arrangement of live sessions into
// the record, replacing any earlier one. It reports the windows and tabs kept.
// A Ghostty failure leaves the record untouched.
func SaveLayout(live map[string]bool) (windows, tabs int, err error) {
	titles, err := ghosttyTitles()
	if err != nil {
		return 0, 0, err
	}
	wins := MatchLayout(titles, live)
	for _, w := range wins {
		tabs += len(w)
	}
	err = UpdateRecord(func(r *Record) bool {
		r.Layout = GhosttyLayout{SavedAt: time.Now(), Windows: wins}
		return true
	})
	return len(wins), tabs, err
}
