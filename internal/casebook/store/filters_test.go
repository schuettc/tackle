package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/filters"
	"github.com/subaud-tools/tackle/internal/casebook/item"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
	"github.com/subaud-tools/tackle/internal/casebook/testgit"
)

var exampleFilterPaths = []string{
	"filters/.gitkeep",
	"filters/dormant-repos.toml",
	"filters/incoming.toml",
	"filters/outgoing-prs.toml",
	"filters/quiet-14d.toml",
	"filters/unpushed.toml",
}

func TestInitSeedsExampleFilters(t *testing.T) {
	r, _ := newStore(t)
	got := strings.Split(testgit.Git(t, r.Dir, "ls-files", "filters"), "\n")
	if !slices.Equal(got, exampleFilterPaths) {
		t.Fatalf("ls-files filters = %q, want %q", got, exampleFilterPaths)
	}
	fs, errs := r.Filters()
	if len(errs) != 0 || len(fs) != 5 {
		t.Fatalf("Filters() = %d filters, errs %v", len(fs), errs)
	}
}

// TestTidySeedsExampleFiltersOnce: a repo with no filters/ gets the examples
// in their own commit with the next write, and a deleted example never
// comes back.
func TestTidySeedsExampleFiltersOnce(t *testing.T) {
	r, _ := newStore(t)
	testgit.Git(t, r.Dir, "rm", "-r", "-q", "filters")
	testgit.Git(t, r.Dir, "commit", "-q", "-m", "an older repo")
	if err := r.Decide(ctx, item.IssueKey("a/b", 1), dec(item.Keep, "court", time.Now())); err != nil {
		t.Fatal(err)
	}
	subj := strings.Split(testgit.Git(t, r.Dir, "log", "-2", "--format=%s"), "\n")
	if len(subj) != 2 || !strings.HasPrefix(subj[0], "decide issue:a/b#1") || subj[1] != "add example filters" {
		t.Fatalf("log %q", subj)
	}
	if got := strings.Split(testgit.Git(t, r.Dir, "show", "--name-only", "--format=", "HEAD~1"), "\n"); !slices.Equal(got, exampleFilterPaths) {
		t.Fatalf("seed commit holds %q, want %q", got, exampleFilterPaths)
	}

	if err := r.DeleteFilter(ctx, "incoming", "filter incoming deleted by court"); err != nil {
		t.Fatal(err)
	}
	if err := r.Decide(ctx, item.IssueKey("a/b", 2), dec(item.Keep, "court", time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "filters", "incoming.toml")); !os.IsNotExist(err) {
		t.Fatalf("incoming.toml came back: %v", err)
	}
	for _, id := range []string{"outgoing-prs", "quiet-14d", "unpushed", "dormant-repos"} {
		if err := r.DeleteFilter(ctx, id, "filter "+id+" deleted by court"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Decide(ctx, item.IssueKey("a/b", 3), dec(item.Keep, "court", time.Now())); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range strings.Split(testgit.Git(t, r.Dir, "log", "--format=%s"), "\n") {
		if s == "add example filters" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf(`"add example filters" committed %d times, want 1`, n)
	}
}

func TestWriteFilterRefusesInvalid(t *testing.T) {
	r, _ := newStore(t)
	before := testgit.Git(t, r.Dir, "rev-parse", "HEAD")
	err := r.WriteFilter(ctx, filters.Filter{ID: "empty", Name: "Empty"}, "filter empty saved by court")
	if err == nil {
		t.Fatal("WriteFilter accepted a filter with no conditions")
	}
	if after := testgit.Git(t, r.Dir, "rev-parse", "HEAD"); after != before {
		t.Fatalf("HEAD moved %s → %s", before, after)
	}
}

func TestFiltersListsUnreadable(t *testing.T) {
	r, _ := newStore(t)
	testgit.Commit(t, r.Dir, "filters/bad.toml", "not toml")
	_, errs := r.Filters()
	found := false
	for _, err := range errs {
		var fe *FilterFileError
		if errors.As(err, &fe) && fe.ID == "bad" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Filters() errs %v, want a FilterFileError for bad", errs)
	}
	_, err := r.ReadFilter("bad")
	var fe *FilterFileError
	if !errors.As(err, &fe) {
		t.Fatalf("ReadFilter(bad) err %v, want *FilterFileError", err)
	}
}

// TestFilterIDMustMatchFileName: a file whose id names another filter is
// listed as not valid under its own file name, so it can't pose as that
// filter, and edit and delete reach this file.
func TestFilterIDMustMatchFileName(t *testing.T) {
	r, _ := newStore(t)
	testgit.Commit(t, r.Dir, "filters/custom.toml", "id = \"incoming\"\nname = \"Incoming\"\n\n[[match]]\nfield = \"relation\"\nop = \"is\"\nvalue = \"incoming\"\n")
	fs, errs := r.Filters()
	n := 0
	for _, f := range fs {
		if f.ID == "incoming" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("Filters() lists %d filters with id incoming, want 1 (the real file)", n)
	}
	var fe *FilterFileError
	found := false
	for _, err := range errs {
		if errors.As(err, &fe) && fe.ID == "custom" && strings.Contains(err.Error(), `id "incoming" does not match the file name`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Filters() errs %v, want custom listed as not valid for its id", errs)
	}
	if _, err := r.ReadFilter("custom"); !errors.As(err, &fe) || fe.ID != "custom" {
		t.Fatalf("ReadFilter(custom) err %v, want *FilterFileError for custom", err)
	}
	if err := r.DeleteFilter(ctx, "custom", "filter custom deleted by court"); err != nil {
		t.Fatalf("DeleteFilter(custom): %v", err)
	}
	if got, err := r.ReadFilter("incoming"); err != nil || got == nil {
		t.Fatalf("incoming after deleting custom = %+v, %v; want it untouched", got, err)
	}
}

func TestDeleteFilterCommits(t *testing.T) {
	r, _ := newStore(t)
	f := filters.Filter{ID: "open-issues", Name: "Open issues", Match: []rules.Condition{{Field: "kind", Op: "is", Value: "issue"}}}
	if err := r.WriteFilter(ctx, f, "filter open-issues saved by court"); err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadFilter("open-issues")
	if err != nil || got == nil || got.Name != "Open issues" {
		t.Fatalf("ReadFilter = %+v, %v", got, err)
	}
	msg := "filter open-issues deleted by court"
	if err := r.DeleteFilter(ctx, "open-issues", msg); err != nil {
		t.Fatal(err)
	}
	if subj := testgit.Git(t, r.Dir, "log", "-1", "--format=%s"); subj != msg {
		t.Errorf("subject %q, want %q", subj, msg)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "filters", "open-issues.toml")); !os.IsNotExist(err) {
		t.Errorf("file still there: %v", err)
	}
	if got, err := r.ReadFilter("open-issues"); got != nil || err != nil {
		t.Errorf("ReadFilter after delete = %+v, %v", got, err)
	}
}
