package rules

import (
	"strings"
	"testing"
	"time"
)

// specExample is the landed-branches rule from spec §4.1.
const specExample = `id = "landed-branches"
name = "Landed branches → delete"
status = "draft"
created_by = "court"
created_at = 2026-09-27T10:00:00Z
edited_at = 2026-09-27T10:00:00Z

[[match]]
field = "kind"
op = "is"
value = "branch"

[[match]]
field = "landed"
op = "is"
value = "all-machines"

[[match]]
field = "worktree"
op = "is-not"
value = "dirty"

[propose]
disposition = "delete"
until = ""
note = "landed ({how}); restore tip {tip}"

[[exclude]]
key = "branch:schuettc/galley@feat/headcount-licensing"
reason = "keep for reference"
by = "court"
at = 2026-09-27T10:05:00Z
`

func TestDecodeEncodeRoundTripsTheSpecExample(t *testing.T) {
	r, err := Decode([]byte(specExample))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if r.ID != "landed-branches" {
		t.Errorf("ID = %q, want %q", r.ID, "landed-branches")
	}
	if r.Name != "Landed branches → delete" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Status != StatusDraft {
		t.Errorf("Status = %q, want %q", r.Status, StatusDraft)
	}
	if r.CreatedBy != "court" {
		t.Errorf("CreatedBy = %q", r.CreatedBy)
	}
	wantTime := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	if !r.CreatedAt.Equal(wantTime) {
		t.Errorf("CreatedAt = %v, want %v", r.CreatedAt, wantTime)
	}
	if !r.EditedAt.Equal(wantTime) {
		t.Errorf("EditedAt = %v, want %v", r.EditedAt, wantTime)
	}
	if len(r.Match) != 3 {
		t.Fatalf("len(Match) = %d, want 3", len(r.Match))
	}
	if r.Match[0].Field != "kind" || r.Match[0].Op != "is" || r.Match[0].Value != "branch" {
		t.Errorf("Match[0] = %+v", r.Match[0])
	}
	if r.Match[1].Field != "landed" || r.Match[1].Op != "is" || r.Match[1].Value != "all-machines" {
		t.Errorf("Match[1] = %+v", r.Match[1])
	}
	if r.Match[2].Field != "worktree" || r.Match[2].Op != "is-not" || r.Match[2].Value != "dirty" {
		t.Errorf("Match[2] = %+v", r.Match[2])
	}
	if r.Propose.Disposition != "delete" {
		t.Errorf("Propose.Disposition = %q", r.Propose.Disposition)
	}
	if r.Propose.Note != "landed ({how}); restore tip {tip}" {
		t.Errorf("Propose.Note = %q", r.Propose.Note)
	}
	if len(r.Exclude) != 1 {
		t.Fatalf("len(Exclude) = %d, want 1", len(r.Exclude))
	}
	if r.Exclude[0].Key != "branch:schuettc/galley@feat/headcount-licensing" {
		t.Errorf("Exclude[0].Key = %q", r.Exclude[0].Key)
	}
	if r.Exclude[0].Reason != "keep for reference" {
		t.Errorf("Exclude[0].Reason = %q", r.Exclude[0].Reason)
	}
	if r.Exclude[0].By != "court" {
		t.Errorf("Exclude[0].By = %q", r.Exclude[0].By)
	}

	// round-trip: encode then decode
	b, err := Encode(r)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	r2, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode round-trip: %v", err)
	}
	if r2.ID != r.ID || r2.Name != r.Name || r2.Status != r.Status {
		t.Errorf("round-trip mismatch: %+v vs %+v", r2, r)
	}
	if len(r2.Match) != len(r.Match) {
		t.Errorf("round-trip Match len %d vs %d", len(r2.Match), len(r.Match))
	}
}

func TestDecodeRejectsUnknownKey(t *testing.T) {
	bad := specExample + "unknown_field = \"oops\"\n"
	_, err := Decode([]byte(bad))
	if err == nil {
		t.Error("expected error for unknown TOML key")
	}
}

func TestValidateRejectsBadIdAndDisposition(t *testing.T) {
	r, _ := Decode([]byte(specExample))

	// bad id: uppercase
	r.ID = "LandedBranches"
	if err := r.Validate(); err == nil {
		t.Error("expected error for uppercase ID")
	}
	r.ID = "landed-branches"

	// bad id: empty
	r2 := r
	r2.ID = ""
	if err := r2.Validate(); err == nil {
		t.Error("expected error for empty ID")
	}

	// bad disposition
	r3 := r
	r3.Propose.Disposition = "zap"
	if err := r3.Validate(); err == nil {
		t.Error("expected error for unknown disposition 'zap'")
	}

	// wait without until
	r4 := r
	r4.Propose.Disposition = "wait"
	r4.Propose.Until = ""
	if err := r4.Validate(); err == nil {
		t.Error("expected error: wait needs until")
	}

	// valid rule passes
	if err := r.Validate(); err != nil {
		t.Errorf("valid rule rejected: %v", err)
	}
}

func TestValidateRejectsBadStatus(t *testing.T) {
	r, _ := Decode([]byte(specExample))
	r.Status = "pending"
	if err := r.Validate(); err == nil {
		t.Error("expected error for bad status 'pending'")
	}
}

func TestValidateRejectsBadCondition(t *testing.T) {
	r, _ := Decode([]byte(specExample))
	r.Match = append(r.Match, Condition{Field: "colour", Op: "is", Value: "red"})
	if err := r.Validate(); err == nil {
		t.Error("expected error for unknown field in condition")
	}
	if !strings.Contains(r.Validate().Error(), "colour") {
		t.Errorf("error should mention field name 'colour'")
	}
}

func TestStatusConstants(t *testing.T) {
	if StatusDraft != "draft" {
		t.Errorf("StatusDraft = %q, want %q", StatusDraft, "draft")
	}
	if StatusActive != "active" {
		t.Errorf("StatusActive = %q, want %q", StatusActive, "active")
	}
}
