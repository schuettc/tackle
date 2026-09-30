package rules

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/schuettc/tackle/internal/casebook/item"
)

// FieldType classifies what kind of value a field holds.
type FieldType int

// FieldType values.
const (
	FieldTypeEnum     FieldType = iota // fixed set of string values
	FieldTypeText                      // free-form string (subset may use matches)
	FieldTypeDuration                  // duration string: <n>h, <n>d, <n>w
	FieldTypeBool                      // "true" or "false"
	FieldTypeCount                     // non-negative integer
)

// Field describes one field in the vocabulary.
type Field struct {
	Name   string    `json:"name"`
	Type   FieldType `json:"type"`
	Ops    []string  `json:"ops"`
	Values []string  `json:"values"`
}

// Operator sets.
var (
	enumOps  = []string{"is", "is-not", "in", "not-in"}
	textOps  = []string{"is", "is-not", "in", "not-in"}
	titleOps = []string{"is", "is-not", "in", "not-in", "matches"}
	durOps   = []string{"older-than", "newer-than"}
	boolOps  = []string{"is", "is-not"}
	countOps = []string{"is", "is-not", "gt", "gte", "lt", "lte"}
)

// vocab is the full field vocabulary. It is built once and never mutated.
var vocab = []Field{
	// Identity fields
	{Name: "kind", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"repo", "pr", "issue", "branch", "worktree"}},
	{Name: "repo", Type: FieldTypeText, Ops: textOps},
	{Name: "owner", Type: FieldTypeText, Ops: textOps},
	{Name: "relation", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"outgoing", "incoming", "own"}},
	{Name: "status", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"new", "to-apply", "waiting", "due", "done", "drift", "conflict"}},
	{Name: "direction", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"outgoing", "incoming", "own"}},
	{Name: "author", Type: FieldTypeText, Ops: textOps},
	{Name: "bot", Type: FieldTypeBool, Ops: boolOps},
	{Name: "title", Type: FieldTypeText, Ops: titleOps}, // only field that allows "matches"
	{Name: "label", Type: FieldTypeText, Ops: textOps},

	// Age / activity fields
	{Name: "age", Type: FieldTypeDuration, Ops: durOps},
	{Name: "pushed", Type: FieldTypeDuration, Ops: durOps},
	{Name: "updated", Type: FieldTypeDuration, Ops: durOps},

	// Landing / branch fields
	{Name: "landed", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"all-machines", "some-machines", "none", "unknown"}},
	{Name: "landed-how", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"default-branch", "merged-pr"}},
	{Name: "gone-upstream", Type: FieldTypeBool, Ops: boolOps},
	{Name: "unpushed", Type: FieldTypeBool, Ops: boolOps},
	{Name: "dirty", Type: FieldTypeBool, Ops: boolOps},
	{Name: "worktree", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"dirty", "clean", "none"}},

	// Repository attributes
	{Name: "archived", Type: FieldTypeBool, Ops: boolOps},
	{Name: "fork", Type: FieldTypeBool, Ops: boolOps},
	{Name: "open-prs", Type: FieldTypeCount, Ops: countOps},
	{Name: "open-issues", Type: FieldTypeCount, Ops: countOps},

	// Decision / policy fields
	{Name: "has-decision", Type: FieldTypeBool, Ops: boolOps},
	{Name: "policy-hit", Type: FieldTypeText, Ops: textOps},
}

// vocabIndex maps field names to their Field for O(1) lookup.
var vocabIndex = func() map[string]Field {
	m := make(map[string]Field, len(vocab))
	for _, f := range vocab {
		m[f.Name] = f
	}
	return m
}()

// Vocabulary returns a copy of the field vocabulary. Callers must not modify
// the returned slice or its elements.
func Vocabulary() []Field {
	return vocab
}

// parseDur validates casebook's duration syntax (item.ParseDuration).
func parseDur(s string) error {
	_, err := item.ParseDuration(strings.TrimSpace(s))
	return err
}

// splitIn splits a comma-separated "in" value list, trimming spaces.
func splitIn(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ValidateCondition checks that c uses a known field and operator, and that
// the value is valid for the field's type.
//
// Specific rules:
//   - "matches" is only allowed on field "title".
//   - duration operators require a parseable duration value (<n>h/<n>d/<n>w).
//   - enum fields with "is" or "is-not" must use a known enum value.
//   - enum fields with "in" or "not-in" must use comma-separated known values.
//   - "matches" values are compiled as regular expressions.
//   - count fields need a non-negative whole number; bool fields "true" or
//     "false".
func ValidateCondition(c Condition) error {
	f, ok := vocabIndex[c.Field]
	if !ok {
		return fmt.Errorf("unknown field %q", c.Field)
	}
	if !slices.Contains(f.Ops, c.Op) {
		return fmt.Errorf("field %q does not support operator %q (allowed: %s)",
			c.Field, c.Op, strings.Join(f.Ops, ", "))
	}

	switch f.Type {
	case FieldTypeCount:
		if n, err := strconv.Atoi(strings.TrimSpace(c.Value)); err != nil || n < 0 {
			return fmt.Errorf("field %q op %q: value %q is not a count (a whole number, 0 or more)",
				c.Field, c.Op, c.Value)
		}
	case FieldTypeBool:
		if c.Value != "true" && c.Value != "false" {
			return fmt.Errorf("field %q: value %q is not allowed (allowed: true, false)", c.Field, c.Value)
		}
	}

	switch c.Op {
	case "older-than", "newer-than":
		if err := parseDur(c.Value); err != nil {
			return fmt.Errorf("field %q op %q: %w", c.Field, c.Op, err)
		}
	case "matches":
		if _, err := regexp.Compile(c.Value); err != nil {
			return fmt.Errorf("field %q op %q: bad regex %q: %w", c.Field, c.Op, c.Value, err)
		}
	case "is", "is-not":
		if f.Type == FieldTypeEnum && len(f.Values) > 0 {
			if !slices.Contains(f.Values, c.Value) {
				return fmt.Errorf("field %q: value %q is not allowed (allowed: %s)",
					c.Field, c.Value, strings.Join(f.Values, ", "))
			}
		}
	case "in", "not-in":
		if f.Type == FieldTypeEnum && len(f.Values) > 0 {
			for _, v := range splitIn(c.Value) {
				if !slices.Contains(f.Values, v) {
					return fmt.Errorf("field %q: value %q is not allowed (allowed: %s)",
						c.Field, v, strings.Join(f.Values, ", "))
				}
			}
		}
	}
	return nil
}
