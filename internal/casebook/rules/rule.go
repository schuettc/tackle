// Package rules defines the casebook standing-rule model, field vocabulary,
// validation and TOML encoding/decoding. Rules live in casebook-data under
// rules/<id>.toml and are the only things that ever generate proposals
// automatically; they never decide.
package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	ID        string      `toml:"id" json:"id"`
	Name      string      `toml:"name" json:"name"`
	Status    string      `toml:"status" json:"status"`
	CreatedBy string      `toml:"created_by" json:"created_by"`
	CreatedAt time.Time   `toml:"created_at" json:"created_at"`
	EditedAt  time.Time   `toml:"edited_at" json:"edited_at"`
	Match     []Condition `toml:"match" json:"match"`
	Propose   RuleAction  `toml:"propose" json:"propose"`
	Exclude   []Exclusion `toml:"exclude,omitempty" json:"exclude,omitempty"`
}

// Condition is one clause in a rule's [[match]] list. All conditions must hold.
type Condition struct {
	Field string `toml:"field" json:"field"`
	Op    string `toml:"op" json:"op"`
	Value string `toml:"value" json:"value"`
}

// RuleAction is the [propose] block: what disposition to propose when the rule matches.
// The TOML table key stays [propose]; only the Go type name changed (to avoid
// clashing with journal.Action in the wire.d.ts TypeScript declarations).
type RuleAction struct {
	Disposition string `toml:"disposition" json:"disposition"`
	Until       string `toml:"until,omitempty" json:"until,omitempty"`
	Note        string `toml:"note,omitempty" json:"note,omitempty"`
}

// Exclusion is one [[exclude]] entry: an item explicitly skipped by this rule.
type Exclusion struct {
	Key    string    `toml:"key" json:"key"`
	Reason string    `toml:"reason,omitempty" json:"reason,omitempty"`
	By     string    `toml:"by" json:"by"`
	At     time.Time `toml:"at" json:"at"`
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

// SameMeaning reports whether a and b match the same items and propose the
// same thing: the same conditions, in order, and the same [propose]. That is
// what edited_at dates (spec §4.1); a rename or an exclusion is not an edit.
func SameMeaning(a, b Rule) bool {
	return slices.Equal(a.Match, b.Match) && a.Propose == b.Propose
}

// Version names r's content: everything but its exclusions (which change
// what matches, live on the page, not what the rule says). Two copies with
// the same version say the same thing; a rename, a status change or an edit
// changes it.
func Version(r Rule) string {
	r.Exclude = nil
	b, err := Encode(r)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Validate reports any structural errors in r.
// It checks:
//   - id non-empty and matches ^[a-z0-9-]+$
//   - status is "draft" or "active"
//   - every Condition passes ValidateCondition
//   - RuleAction.Disposition is a valid item.Disposition
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
