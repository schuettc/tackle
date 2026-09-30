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
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/tackle/internal/casebook/engine"
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

// ValidateStructure checks only what a rule file needs to be a rule: a safe
// id and a known status. A draft may be saved in any state of completion or
// correctness (Court fixes it on the page); Validate is what activation and
// evaluation require.
func (r Rule) ValidateStructure() error {
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("rule id %q must match ^[a-z0-9-]+$", r.ID)
	}
	if !slices.Contains(validStatuses, r.Status) {
		return fmt.Errorf("rule status %q is not valid (want draft or active)", r.Status)
	}
	return nil
}

// ValidateConditions checks every condition, naming the first bad one.
// Conditions are numbered from 1, as Court and the agent count them.
func ValidateConditions(match []Condition) error {
	for i, c := range match {
		if err := ValidateCondition(c); err != nil {
			return fmt.Errorf("condition %d: %w", i+1, err)
		}
	}
	return nil
}

// Validate reports why r can't be activated or evaluated, nil when it can.
// It checks:
//   - id non-empty and matches ^[a-z0-9-]+$
//   - status is "draft" or "active"
//   - every Condition passes ValidateCondition (numbered from 1)
//   - the proposal: a disposition valid for every kind of item the rule
//     can match (Dispositions), and an until that parses (wait and watch
//     need one). Every proposal is validated as a decision for its item,
//     so a rule that passes here never has a proposal refused for these
//     fields: it can't be active and silently propose nothing.
func (r Rule) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("rule id is required")
	}
	if err := r.ValidateStructure(); err != nil {
		return err
	}
	if err := ValidateConditions(r.Match); err != nil {
		return err
	}
	return ValidateProposal(r.Match, r.Propose)
}

// ValidateProposal checks p for a rule with conditions match.
func ValidateProposal(match []Condition, p RuleAction) error {
	d := p.Disposition
	if d == "" {
		return fmt.Errorf("propose: no disposition yet (what the rule proposes)")
	}
	if !slices.Contains(dispositionNames(), d) {
		return fmt.Errorf("propose: %q is not a disposition (%s)", d, strings.Join(dispositionNames(), ", "))
	}
	kinds := Kinds(match)
	if len(kinds) == 0 {
		return fmt.Errorf("propose: the kind conditions leave no kind of item to match")
	}
	if allowed := Dispositions(match); !slices.Contains(allowed, d) {
		return fmt.Errorf("propose: %s can't be proposed for %s (allowed: %s)", d, kindsText(kinds), strings.Join(allowed, ", "))
	}
	if (d == string(item.Wait) || d == string(item.Watch)) && strings.TrimSpace(p.Until) == "" {
		return fmt.Errorf("propose: %s needs an until", d)
	}
	if p.Until != "" {
		if _, err := item.ParseUntil(p.Until); err != nil {
			return fmt.Errorf("propose: %w", err)
		}
	}
	return nil
}

// kindsText names the kinds a rule can match, for a message.
func kindsText(kinds []item.Kind) string {
	if len(kinds) == len(allKinds) {
		return "every kind of item (the rule has no kind condition)"
	}
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	if len(names) == 1 {
		return "a " + names[0]
	}
	return "kind " + strings.Join(names, ", ")
}

func dispositionNames() []string {
	out := make([]string, len(dispositionOrder))
	for i, d := range dispositionOrder {
		out[i] = string(d)
	}
	return out
}

// allKinds is every kind of item, in the vocabulary's order.
var allKinds = []item.Kind{item.KindRepo, item.KindPR, item.KindIssue, item.KindBranch, item.KindWorktree}

// dispositionOrder is the order dispositions are listed in.
var dispositionOrder = []item.Disposition{item.Keep, item.Archive, item.Close, item.Delete, item.Merge, item.Wait, item.Watch, item.Ignore}

// Kinds lists the kinds of item match can match, as its kind conditions
// say: every kind when it has none. Other fields aren't read (a condition
// that only some kinds have a value for still leaves the others possible:
// "is-not" and "not-in" match an empty value).
func Kinds(match []Condition) []item.Kind {
	out := []item.Kind{}
	for _, k := range allKinds {
		f := engine.Fields{Kind: string(k)}
		ok := true
		for _, c := range match {
			if c.Field != "kind" {
				continue
			}
			if m, err := c.Eval(f); err != nil || !m {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, k)
		}
	}
	return out
}

// Dispositions lists what a rule with these conditions may propose: the
// dispositions valid for every kind of item it can match (Kinds), so each
// of its proposals is a valid decision for its item.
func Dispositions(match []Condition) []string {
	kinds := Kinds(match)
	out := []string{}
	if len(kinds) == 0 {
		return out
	}
	for _, d := range dispositionOrder {
		all := true
		for _, k := range kinds {
			if !slices.Contains(item.Allowed(k), d) {
				all = false
				break
			}
		}
		if all {
			out = append(out, string(d))
		}
	}
	return out
}
