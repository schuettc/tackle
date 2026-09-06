package proj

import "testing"

// LiveSessions must report the agent from the @proj_agent option when set, even
// though the pane's foreground process is not that agent (here a plain shell).
func TestLiveSessionsPrefersProjAgentOption(t *testing.T) {
	requireTmux(t)
	sock := "proj-agentopt-test"
	defer Run(sock, "kill-server")

	created, err := EnsureSession(sock, "proj-agentopt-test/w", t.TempDir(), "none", nil)
	if err != nil || !created {
		t.Fatalf("EnsureSession created=%v err=%v", created, err)
	}
	// Pane runs a shell (agent "none" launches nothing); record @proj_agent=pi.
	if _, err := Run(sock, "set-option", "-t", "proj-agentopt-test/w", AgentOption(), "pi"); err != nil {
		t.Fatalf("set @proj_agent: %v", err)
	}

	for _, s := range LiveSessions() {
		if s.Socket == sock && s.Name == "proj-agentopt-test/w" {
			if s.Agent != "pi" {
				t.Fatalf("Agent = %q, want pi (from @proj_agent option)", s.Agent)
			}
			return
		}
	}
	t.Fatal("session not found in LiveSessions")
}
