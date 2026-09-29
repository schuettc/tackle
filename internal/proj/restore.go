package proj

import (
	"sort"
	"strings"
)

// SavedSession is one record entry as the restore view sees it: where it sat
// in the saved layout and what state it is in now.
type SavedSession struct {
	Name string
	Entry
	Window     int  // 1-based position in the saved layout; 0 = unplaced
	Running    bool // its tmux session exists
	Attached   bool // a client (a Ghostty tab) is attached to it
	Transcript bool // its conversation's transcript file exists
}

// NeedsRestore reports whether restoring would do anything: bring the session
// back, or at least give it a tab.
func (s SavedSession) NeedsRestore() bool { return !s.Running || !s.Attached }

// LiveState is which sessions exist and which have a client, across servers.
type LiveState struct {
	Running, Attached map[string]bool
}

// CurrentLiveState reads LiveState from every live proj server.
func CurrentLiveState() LiveState {
	st := LiveState{Running: map[string]bool{}, Attached: map[string]bool{}}
	for _, sock := range Servers() {
		out, err := Run(sock, "list-sessions", "-F", "#{session_name}\x1f#{session_attached}")
		if err != nil {
			continue
		}
		for _, ln := range splitLines(out) {
			name, attached, _ := strings.Cut(ln, "\x1f")
			if name == "" {
				continue
			}
			st.Running[name] = true
			if atoi(attached) > 0 {
				st.Attached[name] = true
			}
		}
	}
	return st
}

// SavedSessions lists the record's sessions in restore order: the saved
// layout's windows in order, tabs in order within each, then every session
// the layout does not place, by project and then name. A layout name with no
// entry is skipped; a name the layout lists twice keeps its first position.
func SavedSessions(rec Record, live LiveState, t Transcripts) []SavedSession {
	mk := func(name string, e Entry, win int) SavedSession {
		_, ok := t.Locate(e.Conversation)
		return SavedSession{Name: name, Entry: e, Window: win,
			Running: live.Running[name], Attached: live.Attached[name], Transcript: ok}
	}
	var out []SavedSession
	placed := map[string]bool{}
	for i, w := range rec.Layout.Windows {
		for _, name := range w {
			e, ok := rec.Sessions[name]
			if !ok || placed[name] {
				continue
			}
			placed[name] = true
			out = append(out, mk(name, e, i+1))
		}
	}
	var rest []string
	for name := range rec.Sessions {
		if !placed[name] {
			rest = append(rest, name)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		pi, pj := rec.Sessions[rest[i]].Project, rec.Sessions[rest[j]].Project
		if pi != pj {
			return pi < pj
		}
		return rest[i] < rest[j]
	})
	for _, name := range rest {
		out = append(out, mk(name, rec.Sessions[name], 0))
	}
	return out
}

// PlanGhostty groups the selected sessions into the Ghostty windows to open:
// saved windows in order, then one window for everything unplaced. Sessions
// that already have a client are left out, so a second restore opens nothing
// twice.
func PlanGhostty(selected []SavedSession) [][]SavedSession {
	byWin := map[int][]SavedSession{}
	var wins []int
	for _, s := range selected {
		if s.Attached {
			continue
		}
		if _, seen := byWin[s.Window]; !seen {
			wins = append(wins, s.Window)
		}
		byWin[s.Window] = append(byWin[s.Window], s)
	}
	sort.Slice(wins, func(i, j int) bool {
		// Unplaced (0) sorts after every real window.
		if wins[i] == 0 || wins[j] == 0 {
			return wins[j] == 0 && wins[i] != 0
		}
		return wins[i] < wins[j]
	})
	var out [][]SavedSession
	for _, w := range wins {
		out = append(out, byWin[w])
	}
	return out
}
