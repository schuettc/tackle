package check

import (
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
)

func TestNegativeRule(t *testing.T) {
	hit := fixture(t, "negative-rule", "hit.md", discover.ClassRepo)
	near := fixture(t, "negative-rule", "near.md", discover.ClassRepo)
	rows := only(t, "negative-rule", input(hit, near))
	if got := lines(rows); len(got) != 4 || got[0] != 3 || got[3] != 6 || rows[3].Source.File != hit.Path {
		t.Fatalf("lines %v %+v", got, rows)
	}
	if got := evidence(rows[1], "matched"); got != "Don't" {
		t.Errorf("matched %q", got)
	}
	for _, r := range rows {
		if r.Certain {
			t.Error("a rewrite is a judgment")
		}
	}
}
