package rules

import (
	"strings"
	"testing"
)

func TestVocabularyHasEveryFieldWithOps(t *testing.T) {
	fields := Vocabulary()
	// 20 fields; each must have at least one operator.
	if len(fields) != 20 {
		t.Errorf("got %d fields, want 20", len(fields))
	}
	names := make(map[string]bool, len(fields))
	for _, f := range fields {
		if names[f.Name] {
			t.Errorf("duplicate field %q", f.Name)
		}
		names[f.Name] = true
		if len(f.Ops) == 0 {
			t.Errorf("field %q has no ops", f.Name)
		}
	}
	// spot-check that core fields are present
	for _, want := range []string{"kind", "landed", "title", "age"} {
		if !names[want] {
			t.Errorf("field %q missing from vocabulary", want)
		}
	}
}

func TestValidateConditionRejectsUnknownFieldAndOp(t *testing.T) {
	// unknown field
	err := ValidateCondition(Condition{Field: "colour", Op: "is", Value: "red"})
	if err == nil {
		t.Error("expected error for unknown field 'colour'")
	}
	// unknown operator on known field
	err = ValidateCondition(Condition{Field: "kind", Op: "glob", Value: "repo"})
	if err == nil {
		t.Error("expected error for unknown op 'glob' on 'kind'")
	}
}

func TestMatchesOnlyOnTitle(t *testing.T) {
	// matches is rejected on any field except title
	err := ValidateCondition(Condition{Field: "kind", Op: "matches", Value: ".*"})
	if err == nil {
		t.Error("expected error: 'matches' not allowed on 'kind'")
	}
	// matches is accepted on title
	err = ValidateCondition(Condition{Field: "title", Op: "matches", Value: "feat/.*"})
	if err != nil {
		t.Errorf("unexpected error for title+matches: %v", err)
	}
}

func TestDurationOpsRequireDuration(t *testing.T) {
	// non-duration value is rejected
	err := ValidateCondition(Condition{Field: "age", Op: "older-than", Value: "soon"})
	if err == nil {
		t.Error("expected error for non-duration value 'soon'")
	}
	// valid duration is accepted
	err = ValidateCondition(Condition{Field: "age", Op: "older-than", Value: "14d"})
	if err != nil {
		t.Errorf("unexpected error for valid duration '14d': %v", err)
	}
}

func TestEnumFieldValidatesValue(t *testing.T) {
	// invalid enum value for kind
	err := ValidateCondition(Condition{Field: "kind", Op: "is", Value: "commit"})
	if err == nil {
		t.Error("expected error for unknown kind value 'commit'")
	}
	// valid enum value for kind
	err = ValidateCondition(Condition{Field: "kind", Op: "is", Value: "branch"})
	if err != nil {
		t.Errorf("unexpected error for valid kind value 'branch': %v", err)
	}
	// valid landed enum value
	err = ValidateCondition(Condition{Field: "landed", Op: "is", Value: "all-machines"})
	if err != nil {
		t.Errorf("unexpected error for valid landed value: %v", err)
	}
	// invalid landed enum value
	err = ValidateCondition(Condition{Field: "landed", Op: "is", Value: "yes"})
	if err == nil {
		t.Error("expected error for unknown landed value 'yes'")
	}
}

// TestCountAndBoolFieldsValidateValue: spec §4.1 — unknown values are
// validation errors. A count comparison needs a non-negative whole number
// and a bool field "true" or "false"; before, both were accepted and the
// rule then failed (or silently matched nothing) at evaluation.
func TestCountAndBoolFieldsValidateValue(t *testing.T) {
	bad := []Condition{
		{Field: "open-prs", Op: "gt", Value: "many"},
		{Field: "open-prs", Op: "gte", Value: "-1"},
		{Field: "open-issues", Op: "lt", Value: "2.5"},
		{Field: "open-issues", Op: "is", Value: ""},
		{Field: "bot", Op: "is", Value: "yes"},
		{Field: "archived", Op: "is-not", Value: ""},
	}
	for _, c := range bad {
		if err := ValidateCondition(c); err == nil {
			t.Errorf("%s %s %q: want a validation error", c.Field, c.Op, c.Value)
		}
	}
	good := []Condition{
		{Field: "open-prs", Op: "gt", Value: "0"},
		{Field: "open-issues", Op: "lte", Value: " 12 "},
		{Field: "bot", Op: "is", Value: "true"},
		{Field: "fork", Op: "is-not", Value: "false"},
	}
	for _, c := range good {
		if err := ValidateCondition(c); err != nil {
			t.Errorf("%s %s %q: unexpected error %v", c.Field, c.Op, c.Value, err)
		}
	}
}

// TestPolicyHitIsGone: the age flags are gone, and the field that read them.
func TestPolicyHitIsGone(t *testing.T) {
	err := ValidateCondition(Condition{Field: "policy-hit", Op: "is", Value: "dormant"})
	if err == nil || !strings.Contains(err.Error(), `unknown field "policy-hit"`) {
		t.Fatalf("got %v", err)
	}
}

// TestOverlappingFieldsAreGone: one meaning per field. pushed (use updated),
// direction (use relation), has-decision (use status is-not new) and dirty
// (use worktree) are unknown fields now.
func TestOverlappingFieldsAreGone(t *testing.T) {
	for _, c := range []Condition{
		{Field: "pushed", Op: "older-than", Value: "365d"},
		{Field: "direction", Op: "is", Value: "outgoing"},
		{Field: "has-decision", Op: "is", Value: "true"},
		{Field: "dirty", Op: "is", Value: "true"},
	} {
		err := ValidateCondition(c)
		if err == nil || !strings.Contains(err.Error(), `unknown field "`+c.Field+`"`) {
			t.Errorf("%s: got %v, want unknown field", c.Field, err)
		}
	}
}

// TestVocabularyLabels: every field, value and operator has a plain label
// for the page; the files keep the raw names.
func TestVocabularyLabels(t *testing.T) {
	by := map[string]Field{}
	for _, f := range Vocabulary() {
		by[f.Name] = f
		if f.Label == "" {
			t.Errorf("field %q has no label", f.Name)
		}
		if f.Type == FieldTypeBool && (f.ValueLabels["true"] != "yes" || f.ValueLabels["false"] != "no") {
			t.Errorf("bool field %q: value labels %v, want yes/no", f.Name, f.ValueLabels)
		}
	}
	for _, c := range []struct{ field, label, help string }{
		{"kind", "type", "repository, pull request, issue, branch or worktree"},
		{"relation", "opened by", "pull requests and issues: you elsewhere, someone else in yours, or you in yours"},
		{"updated", "last activity", "last comment or update, push, or commit"},
		{"gone-upstream", "upstream deleted", "the branch's remote branch was deleted"},
		{"open-prs", "open PRs", ""},
	} {
		if f := by[c.field]; f.Label != c.label || f.Help != c.help {
			t.Errorf("%s: label %q help %q, want %q %q", c.field, f.Label, f.Help, c.label, c.help)
		}
	}
	for field, want := range map[string]map[string]string{
		"kind":     {"repo": "repository", "pr": "pull request", "issue": "issue"},
		"relation": {"outgoing": "you, elsewhere", "incoming": "someone else", "own": "you, in your repo"},
		"worktree": {"dirty": "uncommitted changes", "clean": "clean", "none": "no worktree"},
	} {
		for v, l := range want {
			if got := by[field].ValueLabels[v]; got != l {
				t.Errorf("%s %s: label %q, want %q", field, v, got, l)
			}
		}
	}
	ops := map[string]string{}
	for _, o := range Operators() {
		ops[o.Name] = o.Label
	}
	if len(ops) != 11 || ops["older-than"] != "more than \u2026 ago" || ops["not-in"] != "is none of" || ops["lte"] != "at most" {
		t.Errorf("operators %v", ops)
	}
	for _, f := range Vocabulary() {
		for _, op := range f.Ops {
			if ops[op] == "" {
				t.Errorf("field %q op %q has no label", f.Name, op)
			}
		}
	}
}
