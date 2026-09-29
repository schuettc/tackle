package proj

import (
	"os"
	"path/filepath"
	"regexp"
)

// Transcripts locates agent conversation files by session id. Which store an
// id lives in is how proj knows the agent a session is actually running:
// @proj_agent only records what proj launched, and a pane can be switched to
// another agent afterwards.
type Transcripts struct {
	PiRoot     string // pi's session dir: <PiRoot>/<cwd-key>/<timestamp>_<id>.jsonl
	ClaudeRoot string // Claude Code's config dir: <ClaudeRoot>/projects/<cwd-key>/<id>.jsonl
}

// safeID is the id shape both announcers write. Anything else is refused
// before it reaches a glob pattern.
var safeID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// DefaultTranscripts honours each agent's own override
// ($PI_CODING_AGENT_SESSION_DIR, $CLAUDE_CONFIG_DIR) and falls back to its
// default location under $HOME.
func DefaultTranscripts() Transcripts {
	home := os.Getenv("HOME")
	t := Transcripts{
		PiRoot:     os.Getenv("PI_CODING_AGENT_SESSION_DIR"),
		ClaudeRoot: os.Getenv("CLAUDE_CONFIG_DIR"),
	}
	if t.PiRoot == "" {
		t.PiRoot = filepath.Join(home, ".pi", "agent", "sessions")
	}
	if t.ClaudeRoot == "" {
		t.ClaudeRoot = filepath.Join(home, ".claude")
	}
	return t
}

// Locate reports which agent's store holds the conversation id.
func (t Transcripts) Locate(id string) (agent string, ok bool) {
	if !safeID.MatchString(id) {
		return "", false
	}
	if m, _ := filepath.Glob(filepath.Join(t.PiRoot, "*", "*_"+id+".jsonl")); len(m) > 0 {
		return "pi", true
	}
	if m, _ := filepath.Glob(filepath.Join(t.ClaudeRoot, "projects", "*", id+".jsonl")); len(m) > 0 {
		return "claude", true
	}
	return "", false
}
