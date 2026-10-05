package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
)

var ctx = context.Background()

// now is the fixed clock the checks see.
var now = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// fixture reads testdata/<check>/<name> as a file of the given class.
func fixture(t *testing.T, check, name string, class discover.Class) *discover.File {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", check, name))
	if err != nil {
		t.Fatal(err)
	}
	return &discover.File{Path: "/fixture/" + check + "/" + name, Class: class, Content: string(b)}
}

// inRepo places a file in a repo whose tree holds the given paths.
func inRepo(f *discover.File, root, rel string, tree ...string) *discover.File {
	r := &discover.Repo{Root: root, Ref: "origin/main", Remote: "git@github.com:owner/app.git", Tree: map[string]bool{}}
	for _, p := range append(tree, rel) {
		r.Tree[p] = true
		for d := filepath.Dir(p); d != "."; d = filepath.Dir(d) {
			r.Tree[d] = true
		}
	}
	f.Repo, f.Rel, f.Path = r, rel, filepath.Join(root, rel)
	return f
}

func input(files ...*discover.File) *Input {
	return &Input{Files: files, Config: config.Default(), Now: now}
}

// only runs one check by name.
func only(t *testing.T, name string, in *Input) []row.Row {
	t.Helper()
	for _, c := range All {
		if c.Name == name {
			rows := c.Run(ctx, in)
			for _, r := range rows {
				if r.Check != name || r.ID == "" || r.Source.File == "" || r.Summary == "" {
					t.Errorf("malformed row %+v", r)
				}
			}
			return rows
		}
	}
	t.Fatalf("no check %s", name)
	return nil
}

func lines(rows []row.Row) []int {
	var out []int
	for _, r := range rows {
		out = append(out, r.Source.Start)
	}
	return out
}

func evidence(r row.Row, name string) string {
	var vals []string
	for _, f := range r.Evidence {
		if f.Name == name {
			vals = append(vals, f.Value)
		}
	}
	return strings.Join(vals, "; ")
}

func TestChecksHaveTheSpecNames(t *testing.T) {
	var names []string
	for _, c := range All {
		names = append(names, c.Name)
	}
	want := "size load-limit duplicate dead-path stale-status retired-store misplaced negative-rule secret"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("checks %q", got)
	}
}

// Run runs every check and orders the rows by file, line and check.
func TestRunOrdersRows(t *testing.T) {
	in := input(fixture(t, "negative-rule", "hit.md", discover.ClassRepo), fixture(t, "size", "hit.md", discover.ClassRepo))
	in.Config.Budgets.Repo = 100
	rows := Run(ctx, in)
	if len(rows) < 5 {
		t.Fatalf("rows %d", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1].Source, rows[i].Source
		if a.File > b.File || (a.File == b.File && a.Start > b.Start) {
			t.Fatalf("out of order at %d: %+v then %+v", i, a, b)
		}
	}
}
