package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// holdLock takes casebook-data's write lock file the way another process
// does (its own open file, so flock treats it as a separate holder), shared
// or exclusive. release lets go; the test's end does too.
func holdLock(t *testing.T, r *Repo, how int) (release func()) {
	t.Helper()
	f, err := os.OpenFile(r.LockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	done := false
	release = func() {
		if done {
			return
		}
		done = true
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	t.Cleanup(release)
	return release
}

// returnsWithin reports whether done closes within d.
func returnsWithin(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// TestBatchKeepsOtherCommitsOut: a write phase and its commit are one step
// under the store's lock. A decide that lands while a sync is writing waits
// for the sync's commit, so it never stages the sync's half-written files
// (here a view written so far and a temp file mid-rename), and no commit
// ever holds a temp file.
func TestBatchKeepsOtherCommitsOut(t *testing.T) {
	r, _ := newStore(t)
	writing, release := make(chan struct{}), make(chan struct{})
	batched := make(chan error, 1)
	go func() {
		_, err := r.Batch(ctx, "sync mbp: 1 events", func() error {
			if _, err := r.WriteFile("machines/mbp.json", []byte("{}\n")); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(r.Dir, ".README.md-123.tmp"), []byte("half"), 0o644); err != nil {
				return err
			}
			close(writing)
			<-release
			if err := os.Rename(filepath.Join(r.Dir, ".README.md-123.tmp"), filepath.Join(r.Dir, "README.md")); err != nil {
				return err
			}
			return nil
		})
		batched <- err
	}()
	<-writing
	decided := make(chan struct{})
	var derr error
	go func() {
		defer close(decided)
		derr = r.Decide(ctx, item.IssueKey("a/b", 1), dec(item.Keep, "court", time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)))
	}()
	if returnsWithin(decided, 300*time.Millisecond) {
		t.Error("the decide committed while the sync was still writing")
	}
	close(release)
	if err := <-batched; err != nil {
		t.Fatal(err)
	}
	<-decided
	if derr != nil {
		t.Fatal(derr)
	}
	files := testgit.Git(t, r.Dir, "show", "--name-only", "--format=", "HEAD")
	if files != item.IssueKey("a/b", 1).File() {
		t.Errorf("the decide's commit holds %q, want only its decision", files)
	}
	if all := testgit.Git(t, r.Dir, "log", "--name-only", "--format="); strings.Contains(all, ".tmp") {
		t.Errorf("a temp file was committed:\n%s", all)
	}
}

// TestTheWriteLockSpansProcesses: the store's lock is also a file lock on
// casebook-data, so another process (the launchd sync, the CLI decide)
// holding it keeps this one's writes out, and a writer keeps readers out.
func TestTheWriteLockSpansProcesses(t *testing.T) {
	r, _ := newStore(t)
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	// Another process reading (a serve rebuild) holds it shared: a write waits.
	release := holdLock(t, r, syscall.LOCK_SH)
	decided := make(chan struct{})
	go func() {
		defer close(decided)
		must(t, r.Decide(ctx, item.IssueKey("a/b", 1), dec(item.Keep, "court", at)))
	}()
	if returnsWithin(decided, 300*time.Millisecond) {
		t.Fatal("a decide committed while another process held casebook-data's lock")
	}
	release()
	if !returnsWithin(decided, 5*time.Second) {
		t.Fatal("the decide never committed once the lock was free")
	}

	// Another process writing holds it exclusive: a read waits.
	release = holdLock(t, r, syscall.LOCK_EX)
	read := make(chan struct{})
	go func() {
		defer close(read)
		must(t, r.Read(ctx, func() error { return nil }))
	}()
	if returnsWithin(read, 300*time.Millisecond) {
		t.Fatal("a read ran while another process held casebook-data's write lock")
	}
	release()
	if !returnsWithin(read, 5*time.Second) {
		t.Fatal("the read never ran once the lock was free")
	}

	// A wait for the lock ends with its context.
	release = holdLock(t, r, syscall.LOCK_EX)
	defer release()
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := r.Read(cctx, func() error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a read waiting on a held lock past its context: %v, want DeadlineExceeded", err)
	}
}

// TestTempFilesAreNeverCommitted: casebook-data ignores the temp files
// writes leave beside a file before renaming it (.<name>-*.tmp), so a stray
// one is never committed; a new repo has the rule, and an existing repo gets
// it (one commit) when it is opened.
func TestTempFilesAreNeverCommitted(t *testing.T) {
	r, remote := newStore(t)
	stray := filepath.Join(r.Dir, "items", "issue", "a", "b", ".1.toml-99.tmp")
	must(t, os.MkdirAll(filepath.Dir(stray), 0o755))
	must(t, os.WriteFile(stray, []byte("half"), 0o644))
	must(t, r.Decide(ctx, item.IssueKey("a/b", 1), dec(item.Keep, "court", time.Now())))
	if files := testgit.Git(t, r.Dir, "ls-files"); strings.Contains(files, ".tmp") {
		t.Fatalf("a new repo committed a temp file:\n%s", files)
	}

	// An existing repo from before the rule: opening it adds the rule.
	old, err := Init(ctx, filepath.Join(t.TempDir(), "old"), remote)
	must(t, err)
	testgit.Git(t, old.Dir, "rm", "-q", "--ignore-unmatch", ".gitignore")
	testgit.Git(t, old.Dir, "commit", "-q", "--allow-empty", "-m", "a repo from before")
	added, err := old.EnsureIgnore(ctx)
	must(t, err)
	if !added {
		t.Fatal("EnsureIgnore added nothing to a repo without the rule")
	}
	if again, err := old.EnsureIgnore(ctx); err != nil || again {
		t.Fatalf("EnsureIgnore again: %v %v, want nothing to do", again, err)
	}
	must(t, os.WriteFile(filepath.Join(old.Dir, ".README.md-7.tmp"), []byte("half"), 0o644))
	must(t, old.Decide(ctx, item.IssueKey("a/b", 2), dec(item.Keep, "court", time.Now())))
	if files := testgit.Git(t, old.Dir, "ls-files"); strings.Contains(files, ".tmp") {
		t.Fatalf("an upgraded repo committed a temp file:\n%s", files)
	}
	if subj := testgit.Git(t, old.Dir, "log", "-1", "--format=%s", "--", ".gitignore"); subj != "ignore casebook's temp files" {
		t.Errorf(".gitignore commit %q", subj)
	}
}

// TestCancelledSyncNeverLeavesARebase: the rebase runs on a context that the
// caller's cancel (serve stopping) doesn't end, so casebook-data is never
// left mid-rebase. A post-checkout hook holds the rebase while the caller
// cancels.
func TestCancelledSyncNeverLeavesARebase(t *testing.T) {
	a, b, _ := twoMachines(t)
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	must(t, a.Decide(ctx, item.IssueKey("a/b", 1), dec(item.Keep, "court@a", at)))
	_, err := a.Sync(ctx)
	must(t, err)
	must(t, b.Decide(ctx, item.IssueKey("a/b", 2), dec(item.Close, "court@b", at)))

	hold := testgit.HoldRebase(t, b.Dir)
	sctx, cancel := context.WithCancel(ctx)
	synced := make(chan error, 1)
	go func() { _, err := b.Sync(sctx); synced <- err }()
	hold.Started()
	cancel()
	time.Sleep(200 * time.Millisecond) // a killed git would be gone by now
	hold.Release()
	select {
	case <-synced:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled sync never returned")
	}
	if _, err := os.Stat(filepath.Join(b.Dir, ".git", "rebase-merge")); err == nil {
		t.Fatal("casebook-data left mid-rebase")
	}
	if ref := testgit.Git(t, b.Dir, "symbolic-ref", "-q", "HEAD"); ref != "refs/heads/main" {
		t.Fatalf("HEAD %q, want refs/heads/main", ref)
	}
	for _, n := range []int{1, 2} {
		if d, err := b.ReadDecision(item.IssueKey("a/b", n)); err != nil || d == nil {
			t.Errorf("issue #%d after the cancelled sync: %v %v", n, d, err)
		}
	}
}
