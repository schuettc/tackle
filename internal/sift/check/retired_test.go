package check

import (
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/discover"
)

func TestRetiredStore(t *testing.T) {
	hit := fixture(t, "retired-store", "hit.md", discover.ClassGlobal)
	near := fixture(t, "retired-store", "near.md", discover.ClassGlobal)
	in := input(hit, near)
	in.Config.Retired = []config.Retired{{Name: "old-agent memory", Patterns: []string{"~/.old-agent/memory", "memory_search"}}}
	rows := only(t, "retired-store", in)
	if len(rows) != 1 || rows[0].Source.Start != 3 || !rows[0].Certain {
		t.Fatalf("%+v", rows)
	}
	if got := evidence(rows[0], "pointer"); got != "~/.old-agent/memory; memory_search" {
		t.Errorf("pointer %q", got)
	}
	// No retired stores configured: nothing to find.
	in.Config.Retired = nil
	if rows := only(t, "retired-store", in); len(rows) != 0 {
		t.Fatalf("%+v", rows)
	}
}
