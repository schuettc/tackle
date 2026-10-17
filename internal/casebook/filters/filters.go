// Package filters defines casebook's saved filters: a name and the rules'
// conditions, stored in casebook-data under filters/<id>.toml. A filter is a
// question asked on the Attention list: it never proposes or decides. Its
// format is documented in internal/casebook/FORMAT.md §Saved filters.
package filters

import (
	"bytes"
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/subaud-tools/tackle/internal/casebook/engine"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
)

// Filter is a saved filter stored at casebook-data/filters/<id>.toml.
type Filter struct {
	ID    string            `toml:"id" json:"id"`
	Name  string            `toml:"name" json:"name"`
	Match []rules.Condition `toml:"match" json:"match"`
}

// Decode parses a filter from TOML bytes. It rejects unknown keys.
func Decode(b []byte) (Filter, error) {
	var f Filter
	md, err := toml.Decode(string(b), &f)
	if err != nil {
		return Filter{}, fmt.Errorf("filter: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return Filter{}, fmt.Errorf("filter: unknown key %q", und[0].String())
	}
	return f, nil
}

// Encode serialises f to TOML bytes.
func Encode(f Filter) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# casebook saved filter. Format: internal/casebook/FORMAT.md in subaud-tools/tackle.\n")
	if err := toml.NewEncoder(&buf).Encode(f); err != nil {
		return nil, fmt.Errorf("filter encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Validate reports why f can't be saved or evaluated, nil when it can: a
// safe id, a name, and at least one condition, each valid (numbered from 1).
func (f Filter) Validate() error {
	if !rules.ValidID(f.ID) {
		return fmt.Errorf("filter id %q must match ^[a-z0-9-]+$", f.ID)
	}
	if f.Name == "" {
		return fmt.Errorf("a filter needs a name")
	}
	if len(f.Match) == 0 {
		return fmt.Errorf("a filter needs at least one condition")
	}
	return rules.ValidateConditions(f.Match)
}

// Keys is the set of item keys in res where every condition of f holds.
func (f Filter) Keys(res engine.Result, now time.Time) (map[string]bool, error) {
	ms, err := rules.Rule{Match: f.Match}.MatchAll(res, now)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ms))
	for _, m := range ms {
		out[m.Key] = true
	}
	return out, nil
}

// Examples are the filters a casebook-data repo is given once (FORMAT.md
// §Saved filters), in this order.
func Examples() []Filter {
	c := func(field, op, value string) rules.Condition {
		return rules.Condition{Field: field, Op: op, Value: value}
	}
	return []Filter{
		{ID: "outgoing-prs", Name: "Outgoing PRs", Match: []rules.Condition{c("kind", "is", "pr"), c("relation", "is", "outgoing")}},
		{ID: "incoming", Name: "Incoming", Match: []rules.Condition{c("relation", "is", "incoming")}},
		{ID: "quiet-14d", Name: "Not updated in 14 days", Match: []rules.Condition{c("updated", "older-than", "14d")}},
		{ID: "unpushed", Name: "Unpushed commits", Match: []rules.Condition{c("unpushed", "is", "true")}},
		{ID: "dormant-repos", Name: "Repos not pushed in a year", Match: []rules.Condition{c("kind", "is", "repo"), c("updated", "older-than", "365d")}},
	}
}
