package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// TestCrashedBatchHelper is the process TestACrashedBatchIsCommittedUnderItsOwnMessage
// kills: it writes one file of a BatchWithin and exits before the commit.
func TestCrashedBatchHelper(t *testing.T) {
	dir := os.Getenv("CASEBOOK_CRASH_REPO")
	if dir == "" {
		t.Skip("run by TestACrashedBatchIsCommittedUnderItsOwnMessage")
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = r.BatchWithin(ctx, time.Second, os.Getenv("CASEBOOK_CRASH_MSG"), func() error {
		if _, err := r.WriteFile(os.Getenv("CASEBOOK_CRASH_FILE"), []byte("half of a prune\n")); err != nil {
			return err
		}
		os.Exit(3) // the crash: the file is written, nothing is committed
		return nil
	})
	t.Fatal("the batch outlived its crash")
}

// crashBatch runs a BatchWithin in another process that dies after writing
// rel and before committing.
func crashBatch(t *testing.T, r *Repo, msg, rel string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashedBatchHelper$")
	cmd.Env = append(os.Environ(), "CASEBOOK_CRASH_REPO="+r.Dir, "CASEBOOK_CRASH_MSG="+msg, "CASEBOOK_CRASH_FILE="+rel)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("the crashing batch: %v\n%s", err, out)
	}
	if st := testgit.Git(t, r.Dir, "status", "--porcelain", "-uall"); !strings.Contains(st, filepath.Base(rel)) {
		t.Fatalf("the crash left no uncommitted write: %q", st)
	}
}

// TestACrashedBatchIsCommittedUnderItsOwnMessage: a BatchWithin (prune's)
// that dies between its writes and its commit leaves them uncommitted; the
// next writer under casebook-data's lock (a sync's Batch, or its rebase)
// commits them first, under the crashed batch's own message, and then does
// its own work in its own commit.
func TestACrashedBatchIsCommittedUnderItsOwnMessage(t *testing.T) {
	r, remote := newStore(t)
	const prune = "prune mbp: 2 temp-folder journal events"

	// The next Batch.
	crashBatch(t, r, prune, "journal/mbp/2026/09-24.jsonl")
	if _, err := r.Batch(ctx, "sync mbp: 1 events", func() error {
		_, err := r.WriteFile("journal/mbp/2026/09-25.jsonl", []byte("a sync's line\n"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	log := testgit.Git(t, r.Dir, "log", "-2", "--format=%s", "--name-only")
	want := "sync mbp: 1 events\n\njournal/mbp/2026/09-25.jsonl\n" + prune + "\n\njournal/mbp/2026/09-24.jsonl"
	if log != want {
		t.Errorf("log after a crashed prune and a sync:\n%s\nwant\n%s", log, want)
	}

	// The next sync's rebase, when another machine pushed meanwhile.
	must(t, func() error { _, err := r.Sync(ctx); return err }())
	other := filepath.Join(t.TempDir(), "other")
	testgit.Git(t, filepath.Dir(other), "clone", "-q", remote, other)
	testgit.Commit(t, other, "journal/other/2026/09-25.jsonl", "another machine\n")
	testgit.Git(t, other, "push", "-q", "origin", "HEAD:main")
	crashBatch(t, r, prune, "journal/mbp/2026/09-26.jsonl")
	if _, err := r.Sync(ctx); err != nil {
		t.Fatalf("sync after a crashed prune: %v", err)
	}
	if log := testgit.Git(t, r.Dir, "log", "-1", "--format=%s", "--name-only"); log != prune+"\n\njournal/mbp/2026/09-26.jsonl" {
		t.Errorf("head after a crashed prune and a sync's rebase:\n%s", log)
	}
	if st := testgit.Git(t, r.Dir, "status", "--porcelain"); st != "" {
		t.Errorf("uncommitted after recovery: %q", st)
	}
	if got := testgit.Git(t, remote, "log", "-1", "--format=%s", "main"); got != prune {
		t.Errorf("remote head %q, want the recovered prune pushed", got)
	}
}
