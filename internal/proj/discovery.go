package proj

// Session is a live tmux session discovered across the per-project servers.
type Session struct {
	Name, Socket, Dir, Agent, State string
	Unread, ActionRequired          int
}

// LiveSessions returns every live session across all Servers(), enriched with
// its working directory, agent kind/state, and muster attention counts.
func LiveSessions() []Session {
	counts := MusterCounts()
	var out []Session
	for _, sock := range Servers() {
		names, err := Run(sock, "list-sessions", "-F", "#{session_name}")
		if err != nil {
			continue
		}
		for _, name := range splitLines(names) {
			if name == "" {
				continue
			}
			dir := Query(sock, name, "#{pane_current_path}")
			agent, state := AgentIn(sock, name)
			label := Query(sock, name, "#{"+LabelOption()+"}")
			a := counts[AliasFor(sock, label)]
			out = append(out, Session{
				Name:           name,
				Socket:         sock,
				Dir:            dir,
				Agent:          agent,
				State:          state,
				Unread:         a.Unread,
				ActionRequired: a.ActionRequired,
			})
		}
	}
	return out
}
