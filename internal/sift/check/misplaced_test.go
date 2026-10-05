package check

import (
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/discover"
)

func TestMisplaced(t *testing.T) {
	hit := fixture(t, "misplaced", "hit.md", discover.ClassGlobal)
	near := fixture(t, "misplaced", "near.md", discover.ClassGlobal)
	// The same words in a repo file are where they belong.
	repoFile := fixture(t, "misplaced", "hit.md", discover.ClassRepo)
	in := input(hit, near, repoFile)
	in.Config.Roots = []config.Root{{Path: "/src/work"}}
	in.Repos = []*discover.Repo{{Root: "/src/work/widgetd"}, {Root: "/src/work/ui"}}
	rows := only(t, "misplaced", in)
	if got := lines(rows); len(got) != 2 || got[0] != 3 || got[1] != 4 || rows[0].Source.File != hit.Path {
		t.Fatalf("%+v", rows)
	}
	if got := evidence(rows[0], "term"); got != "widgetd (from /src/work)" {
		t.Errorf("term %q", got)
	}
	if got := evidence(rows[1], "term"); got != "/src/work (from /src/work); widgetd (from /src/work)" {
		t.Errorf("path %q", got)
	}
}
