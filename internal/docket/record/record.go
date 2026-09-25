package record

import (
	"encoding/json"
	"io"
	"time"

	"github.com/schuettc/tackle/internal/docket/journal"
	"github.com/schuettc/tackle/internal/docket/spool"
	"github.com/schuettc/tools-common/harness"
)

type payload struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Command  string `json:"command"`
	CWD      string `json:"cwd"`
	ExitCode *int   `json:"exit_code"`
}

// Parse turns a harness payload into a journal event. ok is false when the
// payload isn't a shell command or holds no git/gh action worth recording.
// harness is "claude" (Claude Code PostToolUse) or "pi" (pi-docket).
func Parse(harnessName string, b []byte, now time.Time) (journal.Event, bool) {
	var p payload
	if json.Unmarshal(b, &p) != nil {
		return journal.Event{}, false
	}
	cmd := p.Command
	switch harnessName {
	case "claude":
		if p.ToolName != "Bash" {
			return journal.Event{}, false
		}
		cmd = p.ToolInput.Command
	case "pi":
	default:
		return journal.Event{}, false
	}
	acts := Classify(cmd)
	if len(acts) == 0 {
		return journal.Event{}, false
	}
	id := harness.FromHookPayload(b)
	ev := journal.Event{V: 1, TS: now.UTC(), Src: harnessName, CWD: id.CWD, ClaudeID: id.ClaudeID,
		AgentID: id.AgentID, Child: id.Child, Actions: acts, ExitCode: p.ExitCode}
	if p.CWD != "" {
		ev.CWD = p.CWD
	}
	return ev, true
}

// Main is `docket record --harness <name>`: read one payload from stdin and
// spool it. It never fails and never prints.
func Main(harnessName string, stdin io.Reader, spoolDir string, now time.Time) {
	b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return
	}
	if ev, ok := Parse(harnessName, b, now); ok {
		_ = spool.Append(spoolDir, ev)
	}
}
