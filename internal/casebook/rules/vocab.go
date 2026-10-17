package rules

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/item"
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

// Field describes one field in the vocabulary. Label, Help and ValueLabels
// are the page's words for it; files always use Name and the raw values.
type Field struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Help        string            `json:"help"`
	Type        FieldType         `json:"type"`
	Ops         []string          `json:"ops"`
	Values      []string          `json:"values"`
	ValueLabels map[string]string `json:"value_labels"`
	// Kinds are the item types the field has a value on; empty: every type.
	Kinds []string `json:"kinds"`
}

// Operator is one operator and the page's words for it.
type Operator struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// operators are every operator a field may take, with its label.
var operators = []Operator{
	{"is", "is"},
	{"is-not", "is not"},
	{"in", "is one of"},
	{"not-in", "is none of"},
	{"matches", "matches pattern"},
	{"older-than", "more than"},
	{"before", "before"},
	{"on-or-after", "on or after"},
	{"newer-than", "within the last"},
	{"gt", "more than"},
	{"gte", "at least"},
	{"lt", "fewer than"},
	{"lte", "at most"},
}

// Operators returns every operator with its label.
func Operators() []Operator { return slices.Clone(operators) }

// Operator sets.
var (
	enumOps  = []string{"is", "is-not", "in", "not-in"}
	textOps  = []string{"is", "is-not", "in", "not-in"}
	titleOps = []string{"is", "is-not", "in", "not-in", "matches"}
	durOps   = []string{"older-than", "newer-than", "before", "on-or-after"}
	boolOps  = []string{"is", "is-not"}
	countOps = []string{"is", "is-not", "gt", "gte", "lt", "lte"}
)

// The item types fields apply to (Field.Kinds).
var (
	prIssue        = []string{"pr", "issue"}
	branchOnly     = []string{"branch"}
	branchWorktree = []string{"branch", "worktree"}
	repoOnly       = []string{"repo"}
)

// boolLabels are every bool field's value labels.
var boolLabels = map[string]string{"true": "yes", "false": "no"}

// vocab is the full field vocabulary. It is built once and never mutated.
var vocab = []Field{
	// Identity fields
	{Name: "kind", Label: "type", Help: "repository, pull request, issue, branch or worktree",
		Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"repo", "pr", "issue", "branch", "worktree"},
		ValueLabels: map[string]string{"repo": "repository", "pr": "pull request", "issue": "issue",
			"branch": "branch", "worktree": "worktree"}},
	{Name: "repo", Label: "repository", Help: "owner/name, e.g. subaud-tools/hail",
		Type: FieldTypeText, Ops: textOps},
	{Name: "owner", Label: "owner", Help: "the account or org, e.g. subaud-tools",
		Type: FieldTypeText, Ops: textOps},
	{Name: "relation", Kinds: prIssue, Label: "opened by",
		Help: "pull requests and issues: you elsewhere, someone else in yours, or you in yours",
		Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"outgoing", "incoming", "own"},
		ValueLabels: map[string]string{"outgoing": "you, elsewhere", "incoming": "someone else",
			"own": "you, in your repo"}},
	{Name: "status", Label: "status", Help: "where it stands in casebook",
		Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"new", "to-apply", "waiting", "due", "done", "drift", "conflict"}},
	{Name: "author", Kinds: prIssue, Label: "author", Help: "GitHub login of who opened it",
		Type: FieldTypeText, Ops: textOps},
	{Name: "bot", Kinds: prIssue, Label: "bot", Help: "opened by a bot account",
		Type: FieldTypeBool, Ops: boolOps, ValueLabels: boolLabels},
	{Name: "title", Kinds: []string{"pr", "issue", "worktree"}, Label: "title", Help: "the title; matches takes a regular expression",
		Type: FieldTypeText, Ops: titleOps}, // only field that allows "matches"
	{Name: "label", Kinds: prIssue, Label: "label", Help: "a GitHub label on it",
		Type: FieldTypeText, Ops: textOps},

	// Age / activity fields
	{Name: "age", Kinds: prIssue, Label: "opened", Help: "when it was opened or created",
		Type: FieldTypeDuration, Ops: durOps},
	{Name: "updated", Label: "last activity", Help: "last comment or update, push, or commit",
		Type: FieldTypeDuration, Ops: durOps},

	// Landing / branch fields
	{Name: "landed", Kinds: branchOnly, Label: "landed", Help: "a branch's commits are on the default branch or a merged PR",
		Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"all-machines", "some-machines", "none", "unknown"}},
	{Name: "landed-how", Kinds: branchOnly, Label: "landed how", Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"default-branch", "merged-pr"}},
	{Name: "gone-upstream", Kinds: branchOnly, Label: "upstream deleted", Help: "the branch's remote branch was deleted",
		Type: FieldTypeBool, Ops: boolOps, ValueLabels: boolLabels},
	{Name: "unpushed", Kinds: branchWorktree, Label: "unpushed commits", Help: "commits on no remote",
		Type: FieldTypeBool, Ops: boolOps, ValueLabels: boolLabels},
	{Name: "worktree", Kinds: branchWorktree, Label: "worktree", Help: "a branch's or worktree's local changes",
		Type: FieldTypeEnum, Ops: enumOps,
		Values: []string{"dirty", "clean", "none"},
		ValueLabels: map[string]string{"dirty": "uncommitted changes", "clean": "clean",
			"none": "no worktree"}},

	// Repository attributes
	{Name: "archived", Kinds: repoOnly, Label: "archived", Type: FieldTypeBool, Ops: boolOps, ValueLabels: boolLabels},
	{Name: "fork", Kinds: repoOnly, Label: "fork", Help: "the repository is a fork",
		Type: FieldTypeBool, Ops: boolOps, ValueLabels: boolLabels},
	{Name: "open-prs", Kinds: repoOnly, Label: "open PRs", Type: FieldTypeCount, Ops: countOps},
	{Name: "open-issues", Kinds: repoOnly, Label: "open issues", Type: FieldTypeCount, Ops: countOps},
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

// ParseDay parses a calendar day, YYYY-MM-DD, as its start in local time.
func ParseDay(s string) (time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a date (YYYY-MM-DD)", s)
	}
	return d, nil
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
	case "before", "on-or-after":
		if _, err := ParseDay(c.Value); err != nil {
			return fmt.Errorf("field %q op %q: %w", c.Field, c.Op, err)
		}
	case "matches":
		if _, err := regexp.Compile(c.Value); err != nil {
			return fmt.Errorf("field %q op %q: bad regex %q: %w", c.Field, c.Op, c.Value, err)
		}
	case "is", "is-not", "in", "not-in":
		if PatternFields[c.Field] {
			for _, v := range splitIn(c.Value) {
				if _, err := path.Match(v, ""); err != nil {
					return fmt.Errorf("field %q: %q is not a pattern (* matches within a /)", c.Field, v)
				}
			}
		}
	}
	switch c.Op {
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
