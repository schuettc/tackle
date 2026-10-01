package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// stdout carries MCP frames only.
func TestChannelSpeaksOnlyMCPOnStdout(t *testing.T) {
	t.Setenv("CULL_HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGENT_SESSION_ID", "")
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	var out, errw bytes.Buffer
	if code := Main([]string{"channel"}, strings.NewReader(in), &out, &errw); code != 0 {
		t.Fatalf("code %d, errw %q", code, errw.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout = %q", out.String())
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["jsonrpc"] != "2.0" {
			t.Errorf("not an MCP frame: %q", l)
		}
	}
	if !strings.Contains(lines[1], "cull_review") {
		t.Errorf("tools/list = %s", lines[1])
	}
}
