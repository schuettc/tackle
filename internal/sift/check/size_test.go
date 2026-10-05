package check

import (
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
)

func TestSize(t *testing.T) {
	hit := fixture(t, "size", "hit.md", discover.ClassRepo)
	near := fixture(t, "size", "near.md", discover.ClassRepo)
	in := input(hit, near)
	in.Config.Budgets.Repo = 100
	rows := only(t, "size", in)
	if len(rows) != 1 || rows[0].Source.File != hit.Path || rows[0].Certain {
		t.Fatalf("%+v", rows)
	}
	if evidence(rows[0], "size") != "173 bytes" || evidence(rows[0], "budget") != "100 bytes (repo file)" {
		t.Errorf("evidence %+v", rows[0].Evidence)
	}
	// The class picks the budget: the same file is fine as a skill.
	hit.Class = discover.ClassSkill
	if rows := only(t, "size", in); len(rows) != 0 {
		t.Fatalf("skill budget: %+v", rows)
	}
}
