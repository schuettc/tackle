package record

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/ledger/spool"
	"github.com/schuettc/tackle/internal/ledger/testgit"
)

func TestParseClaudePayload(t *testing.T) {
	testgit.Env(t)
	p := `{"session_id":"cc-9","cwd":"/w/hail","hook_event_name":"PostToolUse","tool_name":"Bash",
	  "tool_input":{"command":"git push origin feat/x && gh pr create --title SECRET"},"tool_response":{"stdout":"SECRET"}}`
	ev, ok := Parse("claude", []byte(p), time.Unix(5, 0))
	if !ok || ev.Src != "claude" || ev.ClaudeID != "cc-9" || ev.CWD != "/w/hail" || len(ev.Actions) != 2 {
		t.Fatalf("got %+v %v", ev, ok)
	}
	if ev.Actions[0].Verb != "push" || ev.Actions[1].Verb != "pr create" {
		t.Errorf("actions %+v", ev.Actions)
	}
}

func TestParseSkipsNonGitAndOtherTools(t *testing.T) {
	testgit.Env(t)
	for _, p := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`,
		`{"tool_name":"Edit","tool_input":{"command":"git push"}}`,
		`not json`,
		``,
	} {
		if _, ok := Parse("claude", []byte(p), time.Now()); ok {
			t.Errorf("recorded %q", p)
		}
	}
}

func TestParsePiPayloadUsesEnvIdentity(t *testing.T) {
	testgit.Env(t)
	t.Setenv("AGENT_SESSION_ID", "pi-1")
	ev, ok := Parse("pi", []byte(`{"command":"git fetch","cwd":"/w","exit_code":1}`), time.Now())
	if !ok || ev.Src != "pi" || ev.AgentID != "pi-1" || ev.ExitCode == nil || *ev.ExitCode != 1 {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestMainSpoolsAndNeverFails(t *testing.T) {
	testgit.Env(t)
	dir := filepath.Join(t.TempDir(), "spool")
	Main("claude", strings.NewReader(`{"tool_name":"Bash","cwd":"/w","tool_input":{"command":"git push"}}`), dir, time.Now())
	Main("claude", strings.NewReader(`garbage`), dir, time.Now())
	Main("bogus-harness", strings.NewReader(`{}`), dir, time.Now())
	b, err := spool.Drain(dir)
	if err != nil || len(b.Events) != 1 {
		t.Fatalf("got %+v %v", b, err)
	}
	b.Close()
}
