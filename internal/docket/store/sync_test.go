package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/docket/item"
	"github.com/schuettc/tackle/internal/docket/testgit"
)

// twoMachines returns two docket clones of one bare remote.
func twoMachines(t *testing.T) (a, b *Repo, remote string) {
	t.Helper()
	a, remote = newStore(t)
	b, err := Init(ctx, filepath.Join(t.TempDir(), "docket-b"), remote)
	if err != nil {
		t.Fatal(err)
	}
	return a, b, remote
}

func TestDecisionRaceLaterWins(t *testing.T) {
	a, b, _ := twoMachines(t)
	k := item.RepoKey("schuettc/old")
	t0 := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	if err := a.Decide(ctx, k, dec(item.Archive, "court@a", t0)); err != nil {
		t.Fatal(err)
	}
	if err := b.Decide(ctx, k, dec(item.Keep, "court@b", t0.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := b.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resolved) != 1 || res.Resolved[0] != k.File() {
		t.Errorf("resolved %v", res.Resolved)
	}
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*Repo{"a": a, "b": b} {
		d, err := r.ReadDecision(k)
		if err != nil || d == nil {
			t.Fatalf("%s: %v %v", name, d, err)
		}
		if d.Disposition != item.Keep || d.Conflict == nil || d.Conflict.Disposition != item.Archive || d.Conflict.DecidedBy != "court@a" {
			t.Errorf("%s: %+v conflict=%+v", name, d, d.Conflict)
		}
		if item.Compute(k, d, item.Observed{}, nil, time.Now(), false) != item.StatusConflict {
			t.Errorf("%s: status not conflict", name)
		}
	}
	// Both decisions stay in history.
	if n := strings.Count(testgit.Git(t, a.Dir, "log", "--format=%s", "--", k.File()), "decide "); n != 2 {
		t.Errorf("history has %d decide commits", n)
	}
	if testgit.Git(t, a.Dir, "rev-list", "--merges", "--count", "HEAD") != "0" {
		t.Error("history is not linear")
	}
}

func TestSameDecisionRaceIsNotAConflict(t *testing.T) {
	a, b, _ := twoMachines(t)
	k := item.RepoKey("schuettc/old")
	t0 := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	must(t, a.Decide(ctx, k, dec(item.Archive, "court@a", t0)))
	must(t, b.Decide(ctx, k, dec(item.Archive, "court@b", t0.Add(time.Minute))))
	_, err := a.Sync(ctx)
	must(t, err)
	_, err = b.Sync(ctx)
	must(t, err)
	d, _ := b.ReadDecision(k)
	if d.Conflict != nil || d.DecidedBy != "court@b" {
		t.Fatalf("got %+v", d)
	}
}

func TestViewConflictTakesUpstream(t *testing.T) {
	a, b, _ := twoMachines(t)
	_, err := a.WriteFile("README.md", []byte("from a\n"))
	must(t, err)
	_, err = a.Commit(ctx, "sync a")
	must(t, err)
	_, err = b.WriteFile("README.md", []byte("from b\n"))
	must(t, err)
	_, err = b.Commit(ctx, "sync b")
	must(t, err)
	_, err = a.Sync(ctx)
	must(t, err)
	res, err := b.Sync(ctx)
	must(t, err)
	if !res.ViewsTaken {
		t.Error("ViewsTaken not reported")
	}
	got, _ := b.ReadFile("README.md")
	if string(got) != "from a\n" {
		t.Errorf("README = %q", got)
	}
}

func TestUnknownConflictAbortsCleanly(t *testing.T) {
	a, b, _ := twoMachines(t)
	_, err := a.WriteFile("notes.txt", []byte("a\n"))
	must(t, err)
	_, err = a.Commit(ctx, "a")
	must(t, err)
	_, err = b.WriteFile("notes.txt", []byte("b\n"))
	must(t, err)
	_, err = b.Commit(ctx, "b")
	must(t, err)
	_, err = a.Sync(ctx)
	must(t, err)
	if _, err := b.Sync(ctx); err == nil || !strings.Contains(err.Error(), "notes.txt") {
		t.Fatalf("got %v", err)
	}
	if st := testgit.Git(t, b.Dir, "status", "--porcelain"); st != "" {
		t.Errorf("worktree not clean after abort: %q", st)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, ".git", "rebase-merge")); err == nil {
		t.Error("rebase left in progress")
	}
}

func TestOfflineQueuesThenPushes(t *testing.T) {
	a, _ := newStore(t)
	remote := testgit.Git(t, a.Dir, "remote", "get-url", "origin")
	moved := remote + ".away"
	must(t, os.Rename(remote, moved))
	must(t, a.Decide(ctx, item.RepoKey("a/b"), dec(item.Keep, "c", time.Now())))
	if _, err := a.Sync(ctx); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v", err)
	}
	must(t, os.Rename(moved, remote))
	res, err := a.Sync(ctx)
	if err != nil || !res.Pushed {
		t.Fatalf("got %+v %v", res, err)
	}
	if got := testgit.Git(t, remote, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "decide repo:a/b") {
		t.Errorf("remote head %q", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
