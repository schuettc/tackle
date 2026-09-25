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

// Decision is the only thing humans and agents write: one per item, in the
// item's decision file.
type Decision struct {
	Disposition Disposition `toml:"disposition"`
	Note        string      `toml:"note,omitempty"`
	Until       string      `toml:"until,omitempty"`
	DecidedBy   string      `toml:"decided_by"`
	DecidedAt   time.Time   `toml:"decided_at"`
	Conflict    *Conflict   `toml:"conflict,omitempty"`
}

// Conflict records the losing side of a decision race (the later decided_at
// wins). A decision with a conflict has status conflict until decided again.
type Conflict struct {
	Disposition Disposition `toml:"disposition"`
	Note        string      `toml:"note,omitempty"`
	Until       string      `toml:"until,omitempty"`
	DecidedBy   string      `toml:"decided_by"`
	DecidedAt   time.Time   `toml:"decided_at"`
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
