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
	device := MusterDevice()
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
			// A recorded @proj_agent wins over pane process detection: the pane's
			// foreground process may be a launcher (e.g. hail running the shim),
			// not the agent itself. State still comes from AgentIn.
			if rec := Query(sock, name, "#{"+AgentOption()+"}"); rec != "" {
				agent = rec
			}
			label := Query(sock, name, "#{"+LabelOption()+"}")
			a := AttentionFor(counts, device, AliasFor(sock, label))
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
