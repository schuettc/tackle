package rules

import (
	"slices"
	"testing"
)

// TestExamplesAreValidAndOn: the starter rules validate, are on, and are
// the ones Court approved (2026-10-17).
func TestExamplesAreValidAndOn(t *testing.T) {
	var ids []string
	for _, r := range Examples() {
		ids = append(ids, r.ID)
		if r.Status != StatusActive {
			t.Errorf("%s is %q, want active", r.ID, r.Status)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
	}
	if want := []string{"landed-branches", "quiet-bot-prs", "dormant-repos"}; !slices.Equal(ids, want) {
		t.Fatalf("examples %v, want %v", ids, want)
	}
}
