package check

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
)

func TestDuplicate(t *testing.T) {
	a := fixture(t, "duplicate", "a.md", discover.ClassRepo)
	b := fixture(t, "duplicate", "b.md", discover.ClassSkill)
	near := fixture(t, "duplicate", "near.md", discover.ClassRepo)
	rows := only(t, "duplicate", input(a, b, near))
	// The bullet in a.md and the paragraph in b.md say the same thing in
	// nearly the same words (a code block is not prose); near.md shares
	// only a few words with either.
	if len(rows) != 2 {
		t.Fatalf("%+v", rows)
	}
	if rows[0].Source.File != a.Path || rows[0].Source.Start != 3 || rows[1].Source.File != b.Path || rows[1].Source.Start != 3 {
		t.Fatalf("sources %+v %+v", rows[0].Source, rows[1].Source)
	}
	if !strings.Contains(evidence(rows[0], "other copy"), b.Path+":3") || !strings.Contains(evidence(rows[1], "other copy"), a.Path+":3") {
		t.Errorf("evidence %+v / %+v", rows[0].Evidence, rows[1].Evidence)
	}
	if rows[0].Certain {
		t.Error("a duplicate is a judgment")
	}
}

// The same passage twice in one file is a duplicate too.
func TestDuplicateWithinAFile(t *testing.T) {
	p := "Run the full verification suite before you push, because the hook and CI run exactly the same command."
	f := &discover.File{Path: "/x/CLAUDE.md", Class: discover.ClassRepo, Content: "# A\n\n" + p + "\n\n# B\n\n- " + p + "\n"}
	rows := only(t, "duplicate", input(f))
	if got := lines(rows); len(got) != 2 || got[0] != 3 || got[1] != 7 {
		t.Fatalf("lines %v", got)
	}
}
