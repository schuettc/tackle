package rules

import (
	"testing"
)

func TestVocabularyHasEveryFieldWithOps(t *testing.T) {
	fields := Vocabulary()
	// §4.1 lists 25 fields; each must have at least one operator.
	if len(fields) != 25 {
		t.Errorf("got %d fields, want at least 24 (spec §4.1)", len(fields))
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
	for _, want := range []string{"kind", "landed", "title", "age", "policy-hit"} {
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
