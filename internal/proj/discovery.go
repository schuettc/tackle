package proj

// Session is a live tmux session discovered across the per-project servers.
type Session struct{ Name, Socket, Dir, Agent, State string }

// LiveSessions returns every live session across all Servers(), enriched with
// its working directory and agent kind/state.
func LiveSessions() []Session {
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
			out = append(out, Session{
				Name:   name,
				Socket: sock,
				Dir:    dir,
				Agent:  agent,
				State:  state,
			})
		}
	}
	return out
}
