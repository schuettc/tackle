package proj

import "testing"

// TestClassifyAgent locks in the pane_current_command → agent-kind mapping.
//
// The command strings below are VERIFIED against the live tmux servers on this
// machine (see task-5 report), not the brief's guessed table:
//   - pi presents its pane_current_command as "node" (pi is a node app;
//     the child process is "pi").
//   - Claude Code renames its process to its version string (e.g. "2.1.248"),
//     so version-looking commands classify as claude.
//   - cursor-agent → cursor is ASSUMED (no cursor session was live to observe).
func TestClassifyState(t *testing.T) {
	cases := []struct {
		kind    string
		bell    bool
		ageSecs int64
		want    string
	}{
		{"pi", true, 999, "waiting"},    // bell wins
		{"pi", false, 3, "working"},     // recent activity
		{"claude", false, 3600, "idle"}, // stale activity
		{"shell", false, 1, "idle"},     // shells never work
		{"", false, 1, ""},              // unknown stays empty
	}
	for _, c := range cases {
		if got := classifyState(c.kind, c.bell, c.ageSecs); got != c.want {
			t.Errorf("classifyState(%q,%v,%d)=%q want %q", c.kind, c.bell, c.ageSecs, got, c.want)
		}
	}
}

func TestClassifyAgent(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"pi", "pi"},               // literal pi (robustness)
		{"node", "pi"},             // VERIFIED: pi presents as "node"
		{"claude", "claude"},       // literal claude (robustness)
		{"2.1.248", "claude"},      // VERIFIED: Claude Code presents as its version
		{"2.1.239", "claude"},      // VERIFIED
		{"cursor-agent", "cursor"}, // ASSUMED
		{"zsh", "shell"},
		{"bash", "shell"},
		{"yazi", "shell"},
		{"scratch", "shell"},
		{"", ""},
	}
	for _, c := range cases {
		if got := classifyAgent(c.cmd); got != c.want {
			t.Errorf("classifyAgent(%q)=%q want %q", c.cmd, got, c.want)
		}
	}
}

func TestPickMainPane(t *testing.T) {
	// Regression: a version-string command (claude, e.g. "2.1.248") with NO
	// activity column must still parse — the old space-delimited 5-field format
	// collapsed the empty pane_activity field and blanked every agent.
	out := "0\x1f50\x1f%0\x1f2.1.248\n149\x1f11\x1f%1\x1fscratch\n149\x1f28\x1f%2\x1fyazi"
	id, cmd := pickMainPane(out)
	if id != "%0" || cmd != "2.1.248" {
		t.Fatalf("pickMainPane = %q,%q want %%0,2.1.248", id, cmd)
	}
	if classifyAgent(cmd) != "claude" {
		t.Fatalf("version cmd should classify as claude")
	}
	if eid, ecmd := pickMainPane(""); eid != "" || ecmd != "" {
		t.Fatalf("empty output → empty, got %q,%q", eid, ecmd)
	}
}
