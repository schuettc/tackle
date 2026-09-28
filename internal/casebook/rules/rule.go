// Package rules defines the casebook standing-rule model, field vocabulary,
// validation and TOML encoding/decoding. Rules live in casebook-data under
// rules/<id>.toml and are the only things that ever generate proposals
// automatically; they never decide.
package rules

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/tackle/internal/casebook/item"
)

// Status values for a Rule.
const (
	StatusDraft  = "draft"
	StatusActive = "active"
)

var validStatuses = []string{StatusDraft, StatusActive}

// idRe is the allowed pattern for rule IDs.
var idRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// ValidID reports whether id is a safe rule id: lower-case letters, digits and
// hyphens only, so it can never name a path outside rules/.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Rule is a standing rule stored at casebook-data/rules/<id>.toml.
// Its format is documented in internal/casebook/FORMAT.md §Rules.
type Rule struct {
	ID        string      `toml:"id"`
	Name      string      `toml:"name"`
	Status    string      `toml:"status"`
	CreatedBy string      `toml:"created_by"`
	CreatedAt time.Time   `toml:"created_at"`
	EditedAt  time.Time   `toml:"edited_at"`
	Match     []Condition `toml:"match"`
	Propose   Action      `toml:"propose"`
	Exclude   []Exclusion `toml:"exclude,omitempty"`
}

// Condition is one clause in a rule's [[match]] list. All conditions must hold.
type Condition struct {
	Field string `toml:"field"`
	Op    string `toml:"op"`
	Value string `toml:"value"`
}

// Action is the [propose] block: what disposition to propose when the rule matches.
type Action struct {
	Disposition string `toml:"disposition"`
	Until       string `toml:"until,omitempty"`
	Note        string `toml:"note,omitempty"`
}

// Exclusion is one [[exclude]] entry: an item explicitly skipped by this rule.
type Exclusion struct {
	Key    string    `toml:"key"`
	Reason string    `toml:"reason,omitempty"`
	By     string    `toml:"by"`
	At     time.Time `toml:"at"`
}

// Decode parses a rule from TOML bytes. It rejects unknown keys.
func Decode(b []byte) (Rule, error) {
	var r Rule
	md, err := toml.Decode(string(b), &r)
	if err != nil {
		return Rule{}, fmt.Errorf("rule: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return Rule{}, fmt.Errorf("rule: unknown key %q", und[0].String())
	}
	return r, nil
}

// Encode serialises r to TOML bytes.
func Encode(r Rule) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# casebook standing rule. Format: internal/casebook/FORMAT.md in schuettc/tackle.\n")
	if err := toml.NewEncoder(&buf).Encode(r); err != nil {
		return nil, fmt.Errorf("rule encode: %w", err)
	}
	return buf.Bytes(), nil
}

// allDispositions is the set of all dispositions that exist across all item kinds.
var allDispositions = func() []string {
	seen := map[item.Disposition]bool{}
	for _, k := range []item.Kind{item.KindRepo, item.KindPR, item.KindIssue, item.KindBranch, item.KindWorktree} {
		for _, d := range item.Allowed(k) {
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, string(d))
	}
	return out
}()

// Validate reports any structural errors in r.
// It checks:
//   - id non-empty and matches ^[a-z0-9-]+$
//   - status is "draft" or "active"
//   - every Condition passes ValidateCondition
//   - Action.Disposition is a valid item.Disposition
//   - wait/watch dispositions require a non-empty Until
func (r Rule) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("rule id is required")
	}
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("rule id %q must match ^[a-z0-9-]+$", r.ID)
	}
	if !slices.Contains(validStatuses, r.Status) {
		return fmt.Errorf("rule status %q is not valid (want draft or active)", r.Status)
	}
	for i, c := range r.Match {
		if err := ValidateCondition(c); err != nil {
			return fmt.Errorf("condition %d: %w", i, err)
		}
	}
	if !slices.Contains(allDispositions, r.Propose.Disposition) {
		return fmt.Errorf("propose.disposition %q is not a valid disposition", r.Propose.Disposition)
	}
	if (r.Propose.Disposition == string(item.Wait) || r.Propose.Disposition == string(item.Watch)) &&
		r.Propose.Until == "" {
		return fmt.Errorf("propose.disposition %q requires a non-empty until", r.Propose.Disposition)
	}
	return nil
}
