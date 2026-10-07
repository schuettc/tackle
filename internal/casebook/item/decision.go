package item

import (
	"bytes"
	"fmt"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

// Disposition is the recorded intent for an item.
type Disposition string

// Dispositions.
const (
	Keep    Disposition = "keep"
	Archive Disposition = "archive"
	Close   Disposition = "close"
	Delete  Disposition = "delete"
	Merge   Disposition = "merge"
	Wait    Disposition = "wait"
	Watch   Disposition = "watch"
	Ignore  Disposition = "ignore"
)

var allowed = map[Kind][]Disposition{
	KindRepo:     {Keep, Archive, Delete, Wait, Watch, Ignore},
	KindPR:       {Keep, Close, Merge, Wait, Watch, Ignore},
	KindIssue:    {Keep, Close, Wait, Watch, Ignore},
	KindBranch:   {Keep, Delete, Wait, Watch, Ignore},
	KindWorktree: {Keep, Delete, Wait, Ignore},
}

// Allowed lists the valid dispositions for kind k.
func Allowed(k Kind) []Disposition { return slices.Clone(allowed[k]) }

// Choice is one answer the decide step offers for a kind: its disposition,
// the words the page shows (Label) and what choosing it does (Says).
// Outward choices change something outside casebook, so they only go to
// To apply. NeedsUntil choices need an until condition (Not now).
type Choice struct {
	Disposition Disposition
	Label       string
	Says        string
	Outward     bool
	NeedsUntil  bool
}

// The decide wording. This table is the one source: serve copies it into
// GET /api/decisions/vocabulary, and the page and the agent's guide render
// only what that returns. It names no person, provider or model.
var questions = map[Kind]string{
	KindPR:       "What should happen to this pull request?",
	KindIssue:    "What should happen to this issue?",
	KindBranch:   "What should happen to this branch?",
	KindWorktree: "What should happen to this worktree?",
	KindRepo:     "What should happen to this repository?",
}

var (
	leaveOpen = Choice{Disposition: Keep, Label: "Leave it open", Says: "It stays open and stays in your list, at the bottom. It moves back up when someone replies or it changes."}
	keepIt    = Choice{Disposition: Keep, Label: "Keep it", Says: "It stays as it is and stays in your list, at the bottom."}
	notNow    = Choice{Disposition: Wait, Label: "Not now", Says: "Hidden until a date or an event you pick, or until someone replies or it changes. Then it asks again.", NeedsUntil: true}
	stopTrack = Choice{Disposition: Ignore, Label: "Stop tracking it", Says: "casebook never asks about it again. Nothing is done on GitHub."}
	closeSays = "Goes to To apply with your closing comment. Nothing changes until you approve the plan, and you see the comment again before it's posted."
)

// choices lists what the decide step offers per kind, in the order shown.
// watch is read and accepted (allowed) but never offered: Not now writes wait.
var choices = map[Kind][]Choice{
	KindPR: {
		leaveOpen,
		{Disposition: Merge, Label: "Merge it", Says: "Goes to To apply. Nothing changes until you approve the plan, which shows the exact command.", Outward: true},
		{Disposition: Close, Label: "Close it without merging", Says: closeSays, Outward: true},
		notNow, stopTrack,
	},
	KindIssue: {
		leaveOpen,
		{Disposition: Close, Label: "Close it", Says: closeSays, Outward: true},
		notNow, stopTrack,
	},
	KindBranch: {
		keepIt,
		{Disposition: Delete, Label: "Delete it", Says: "Goes to To apply. Nothing is deleted until you approve the plan, and a restore record is kept.", Outward: true},
		notNow, stopTrack,
	},
	KindWorktree: {
		keepIt,
		{Disposition: Delete, Label: "Remove it", Says: "Goes to To apply. Nothing is removed until you approve the plan, and a restore record is kept.", Outward: true},
		notNow, stopTrack,
	},
	KindRepo: {
		keepIt,
		{Disposition: Archive, Label: "Archive it on GitHub", Says: "Goes to To apply. Nothing changes until you approve the plan.", Outward: true},
		{Disposition: Delete, Label: "Delete it", Says: "Goes to To apply. Nothing is deleted until you approve the plan.", Outward: true},
		notNow, stopTrack,
	},
}

// Question is the decide step's question for kind k.
func Question(k Kind) string { return questions[k] }

// Choices lists the answers the decide step offers for kind k, in order.
func Choices(k Kind) []Choice { return slices.Clone(choices[k]) }

// NotNowForm is one condition Not now offers. Template is an until form
// with one %s hole for what Asks names ("days": the page fills today +
// Days; "date", "pr", "pr-or-issue", "repo": the page asks for it), or a
// whole until form with no hole when Asks is "" (fixed).
type NotNowForm struct {
	ID       string
	Label    string
	Template string
	Asks     string
	Days     int
}

// Fill returns f's until condition with v in its hole; a fixed form ignores v.
func (f NotNowForm) Fill(v string) string {
	if f.Asks == "" {
		return f.Template
	}
	return fmt.Sprintf(f.Template, v)
}

// NotNowForms lists Not now's conditions in the order the page offers them.
// Each maps to an until form (UntilForms), so nothing new is stored.
func NotNowForms() []NotNowForm {
	return []NotNowForm{
		{ID: "in-1w", Label: "in 1 week", Template: "date(%s)", Asks: "days", Days: 7},
		{ID: "in-1m", Label: "in 1 month", Template: "date(%s)", Asks: "days", Days: 30},
		{ID: "on-date", Label: "on a date\u2026", Template: "date(%s)", Asks: "date"},
		{ID: "pr-merges", Label: "when a PR merges\u2026", Template: "merged(%s)", Asks: "pr"},
		{ID: "closes", Label: "when a PR or issue closes\u2026", Template: "closed(%s)", Asks: "pr-or-issue"},
		{ID: "quiet-90", Label: "when it goes quiet for 90 days", Template: "inactive(90d)"},
		{ID: "release", Label: "when a repo releases\u2026", Template: "released(%s)", Asks: "repo"},
	}
}

// Decision is the only thing humans and agents write: one per item, in the
// item's decision file.
type Decision struct {
	Disposition Disposition `toml:"disposition" json:"disposition"`
	Note        string      `toml:"note,omitempty" json:"note,omitempty"`
	Until       string      `toml:"until,omitempty" json:"until,omitempty"`
	DecidedBy   string      `toml:"decided_by" json:"decided_by"`
	DecidedAt   time.Time   `toml:"decided_at" json:"decided_at"`
	// ProposedBy names who proposed this decision when it was accepted from a
	// proposal: "pi:<session>", "claude:<session>" or "rule:<id>". Format 2.
	ProposedBy string `toml:"proposed_by,omitempty" json:"proposed_by,omitempty"`
	// Rule is the standing rule that proposed it, when one did. Format 2.
	Rule     string    `toml:"rule,omitempty" json:"rule,omitempty"`
	Conflict *Conflict `toml:"conflict,omitempty" json:"conflict,omitempty"`
}

// Conflict records the losing side of a decision race (the later decided_at
// wins). A decision with a conflict has status conflict until decided again.
type Conflict struct {
	Disposition Disposition `toml:"disposition" json:"disposition"`
	Note        string      `toml:"note,omitempty" json:"note,omitempty"`
	Until       string      `toml:"until,omitempty" json:"until,omitempty"`
	DecidedBy   string      `toml:"decided_by" json:"decided_by"`
	DecidedAt   time.Time   `toml:"decided_at" json:"decided_at"`
}

// Validate checks d for an item of kind k.
func (d Decision) Validate(k Kind) error {
	if !slices.Contains(allowed[k], d.Disposition) {
		return fmt.Errorf("disposition %q is not valid for %s (allowed: %v)", d.Disposition, k, allowed[k])
	}
	if d.DecidedBy == "" {
		return fmt.Errorf("decided_by is required")
	}
	if d.DecidedAt.IsZero() {
		return fmt.Errorf("decided_at is required")
	}
	if (d.Disposition == Wait || d.Disposition == Watch) && d.Until == "" {
		return fmt.Errorf("%s needs an until condition", d.Disposition)
	}
	if d.Until != "" {
		if _, err := ParseUntil(d.Until); err != nil {
			return err
		}
	}
	return nil
}

// DecodeDecision parses a decision file, rejecting unknown fields.
func DecodeDecision(b []byte) (Decision, error) {
	var d Decision
	md, err := toml.Decode(string(b), &d)
	if err != nil {
		return d, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return d, fmt.Errorf("unknown field %q", und[0].String())
	}
	return d, nil
}

// EncodeDecision renders d as a decision file. Times are written in UTC at
// second precision.
func EncodeDecision(d Decision) ([]byte, error) {
	d.DecidedAt = d.DecidedAt.UTC().Truncate(time.Second)
	if d.Conflict != nil {
		c := *d.Conflict
		c.DecidedAt = c.DecidedAt.UTC().Truncate(time.Second)
		d.Conflict = &c
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
