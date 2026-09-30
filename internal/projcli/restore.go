package projcli

import (
	"fmt"
	"io"

	"github.com/schuettc/tackle/internal/proj"
)

// Seams for runRestore's side effects, so tests never create sessions, open
// Ghostty windows or switch clients.
var (
	restoreSession = proj.RestoreSession
	spawnSidebar   = SpawnSidebarDetached
	ghosttyOpen    = proj.GhosttyOpen
	gotoSession    = proj.Goto
	liveState      = proj.CurrentLiveState
)

// runRestore brings back the named saved sessions, in saved order, then either
// jumps to jump (a single restore from enter) or rebuilds their Ghostty
// windows and tabs (^r). Every session is attempted; the exit code is 1 when
// any failed. A Ghostty failure is reported but does not undo the sessions.
func runRestore(names []string, jump string, out io.Writer) int {
	rec, err := proj.LoadRecord()
	if err != nil {
		_, _ = fmt.Fprintf(out, "proj: %v\n", err)
		return 1
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	cfg := proj.LoadConfig()
	status := 0
	var restored []proj.SavedSession
	for _, s := range proj.SavedSessions(rec, liveState(), proj.DefaultTranscripts()) {
		if !want[s.Name] {
			continue
		}
		conv := ""
		if s.Transcript {
			conv = s.Conversation
		}
		dir := proj.ResolveDir(s.Socket, s.Name, s.Dir)
		created, err := restoreSession(s.Socket, s.Name, dir, s.Agent, conv)
		switch {
		case err != nil:
			_, _ = fmt.Fprintf(out, "failed %s: %v\n", s.Name, err)
			status = 1
			continue
		case !created:
			_, _ = fmt.Fprintf(out, "exists %s\n", s.Name)
		default:
			_, _ = fmt.Fprintf(out, "restored %s (%s, %s)\n", s.Name, agentLabel(s.Agent), resumeLabel(s.Agent, conv))
			if cfg.SidebarFor(s.Project) {
				spawnSidebar(s.Socket, s.Name, dir)
			}
		}
		restored = append(restored, s)
	}

	if jump != "" {
		for _, s := range restored {
			if s.Name == jump {
				if err := gotoSession(s.Socket, s.Name); err != nil {
					_, _ = fmt.Fprintf(out, "proj: %v\n", err)
					return 1
				}
			}
		}
		return status
	}

	// Re-read client state: a session that already had a tab keeps it.
	live := liveState()
	for i := range restored {
		restored[i].Running = live.Running[restored[i].Name]
		restored[i].Attached = live.Attached[restored[i].Name]
	}
	if err := ghosttyOpen(proj.PlanGhostty(restored), out); err != nil {
		_, _ = fmt.Fprintf(out, "ghostty: %v; attach with: proj\n", err)
	}
	return status
}

func agentLabel(agent string) string {
	if agent == "" || agent == "none" {
		return "shell"
	}
	return agent
}

func resumeLabel(agent, conv string) string {
	if conv != "" && (agent == "pi" || agent == "claude") {
		return "resumed"
	}
	return "fresh"
}
