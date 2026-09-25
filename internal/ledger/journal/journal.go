// Package journal defines the records the ledger journals: git hook events
// and harness (agent) actions. Records never contain command lines, commit
// messages, titles, bodies, field values or credentials: only verbs,
// targets and allow-listed flags.
package journal

import "time"

// Action is one git or gh invocation observed in an agent's command.
type Action struct {
	Tool   string   `json:"tool"`             // git | gh
	Verb   string   `json:"verb"`             // e.g. "push", "pr merge", "worktree add"
	Dir    string   `json:"dir,omitempty"`    // git -C
	Repo   string   `json:"repo,omitempty"`   // gh -R, or the repo of Dir (filled at sync)
	Number int      `json:"number,omitempty"` // gh pr/issue number
	Refs   []string `json:"refs,omitempty"`   // branch/tag/remote names, refspecs, paths
	Flags  []string `json:"flags,omitempty"`  // allow-listed flags only
}

// Event is one journal line.
type Event struct {
	V         int        `json:"v"`
	TS        time.Time  `json:"ts"`
	Src       string     `json:"src"`             // git-hook | claude | pi
	Hook      string     `json:"hook,omitempty"`  // git hook name
	Args      []string   `json:"args,omitempty"`  // git hook arguments
	Stdin     [][]string `json:"stdin,omitempty"` // git hook stdin, whitespace-split lines
	Truncated bool       `json:"truncated,omitempty"`
	CWD       string     `json:"cwd,omitempty"`
	GitDir    string     `json:"git_dir,omitempty"`
	ClaudeID  string     `json:"claude_id,omitempty"`
	AgentID   string     `json:"agent_id,omitempty"`
	Child     bool       `json:"child,omitempty"`
	Actions   []Action   `json:"actions,omitempty"`
	ExitCode  *int       `json:"exit_code,omitempty"`
	Repo      string     `json:"repo,omitempty"`    // owner/name, filled at sync
	Machine   string     `json:"machine,omitempty"` // filled at sync
}
