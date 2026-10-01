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
// `tab`: inside the tell block `tab` names Ghostty's tab class. It prints
// nothing when Ghostty is not running: a bare `tell` would launch it, and the
// attach hook fires for clients in other terminals too.
const readTitlesScript = `set out to ""
if application "Ghostty" is running then
tell application "Ghostty"
repeat with w in windows
set out to out & "W" & linefeed
repeat with t in tabs of w
set out to out & "T" & (character id 9) & (name of t) & linefeed
end repeat
end repeat
end tell
end if
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

// mergeLayout folds the current Ghostty arrangement (cur) into the saved one
// (old). Every session with a tab now sits where its tab is. A saved session
// with no tab now (not restored yet, detached, or in another terminal) keeps
// its saved window: it is appended to the current window that holds the most
// of that saved window's sessions, or stays a window of its own when none of
// them have a tab. Like the record, the layout never shrinks on what is
// merely absent, so attaching one session before restoring the rest cannot
// overwrite the saved layout with a one-tab window.
func mergeLayout(old, cur [][]string) [][]string {
	tabbed := map[string]int{} // session -> index of its current window
	for i, w := range cur {
		for _, s := range w {
			tabbed[s] = i
		}
	}
	// Walk the saved windows in order. Each one either maps onto the current
	// window holding most of its sessions (lowest index on a tie), handing it
	// its untabbed sessions, or survives on its own. Current windows no saved
	// window maps onto follow, in Ghostty's order.
	extra := make([][]string, len(cur))
	placed := make([]bool, len(cur))
	var order [][]string // nil entry = a current window, resolved below
	var curAt []int      // for each nil entry in order, its cur index
	for _, w := range old {
		votes := map[int]int{}
		var rest []string
		for _, s := range w {
			if i, ok := tabbed[s]; ok {
				votes[i]++
			} else {
				rest = append(rest, s)
			}
		}
		best := -1
		for i, v := range votes {
			if best < 0 || v > votes[best] || (v == votes[best] && i < best) {
				best = i
			}
		}
		if best < 0 {
			if len(rest) > 0 { // an empty saved window would read as a nil marker
				order = append(order, rest)
			}
			continue
		}
		extra[best] = append(extra[best], rest...)
		if !placed[best] {
			placed[best] = true
			order = append(order, nil)
			curAt = append(curAt, best)
		}
	}
	for i := range cur {
		if !placed[i] {
			order = append(order, nil)
			curAt = append(curAt, i)
		}
	}
	var out [][]string
	seen := map[string]bool{}
	k := 0
	for _, w := range order {
		if w == nil {
			i := curAt[k]
			k++
			w = append(append([]string{}, cur[i]...), extra[i]...)
		}
		var keep []string
		for _, s := range w {
			if !seen[s] {
				seen[s] = true
				keep = append(keep, s)
			}
		}
		if len(keep) > 0 {
			out = append(out, keep)
		}
	}
	return out
}

// SaveLayout captures the current Ghostty arrangement of live sessions and
// merges it into the record's layout (see mergeLayout). It reports the windows
// and tabs saved. A Ghostty failure leaves the record untouched.
func SaveLayout(live map[string]bool) (windows, tabs int, err error) {
	titles, err := ghosttyTitles()
	if err != nil {
		return 0, 0, err
	}
	cur := MatchLayout(titles, live)
	err = UpdateRecord(func(r *Record) bool {
		wins := mergeLayout(r.Layout.Windows, cur)
		windows = len(wins)
		for _, w := range wins {
			tabs += len(w)
		}
		r.Layout = GhosttyLayout{SavedAt: time.Now(), Windows: wins}
		return true
	})
	return windows, tabs, err
}
