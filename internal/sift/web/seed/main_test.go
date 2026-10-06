package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/store"
)

// The seeded round reproduces the fixture: every row, its source pointing at
// a real file (a repo file read at HEAD), every file recommended through
// the store's own checks (so the round is ready), and -scale pads it.
func TestSeedReproducesTheFixture(t *testing.T) {
	ctx := context.Background()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "round.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scale := range []int{0, 653} {
		base, db := t.TempDir(), filepath.Join(t.TempDir(), "sift.db")
		id, err := Seed(ctx, db, data, base, Options{Scale: scale})
		if err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		_, rows, err := st.Round(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		p, err := st.State(ctx, id)
		files, _ := st.Files(ctx, id)
		_ = st.Close()
		if err != nil || p.State != store.Ready || p.Files < 4 || p.Recommended != p.Files {
			t.Fatalf("scale %d: %+v %v", scale, p, err)
		}
		linked := 0
		for _, f := range files {
			if len(f.Group) > 1 {
				linked++
			}
		}
		if linked != 2 {
			t.Errorf("linked files %d", linked)
		}
		if scale > 0 && len(rows) != scale {
			t.Fatalf("scale %d: %d rows", scale, len(rows))
		}
		seen := map[string]bool{}
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatalf("duplicate id %s", r.ID)
			}
			seen[r.ID] = true
			if _, err := os.Stat(r.Source.File); err != nil {
				t.Fatalf("%s: %v", r.ID, err)
			}
			if r.Source.Repo != "" {
				if err := discover.Git(ctx, r.Source.Repo, "cat-file", "-e", "HEAD:"+r.Source.Path).Run(); err != nil {
					t.Fatalf("%s not committed: %v", r.Source.Path, err)
				}
			}
		}
	}
}

// -recommending leaves one file without a recommendation: the page waits.
func TestSeedCanLeaveTheRoundRecommending(t *testing.T) {
	ctx := context.Background()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "round.json"))
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "sift.db")
	id, err := Seed(ctx, db, data, t.TempDir(), Options{Recommending: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if p, _ := st.State(ctx, id); p.State != store.Recommending || p.Recommended != p.Files-1 {
		t.Fatalf("%+v", p)
	}
}
