package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// casebook-data's lock. Every write to the working tree and the commit that
// follows it (Batch: a decision, a rule, a restore record, a sync's write
// phase), and Sync's rebase, hold it exclusive; a reader that must see a
// whole tree (serve's rebuild) holds it shared (Read). It has two halves,
// always taken in this order:
//
//  1. r.mu, a sync.RWMutex: this process's goroutines.
//  2. a flock on LockPath() (inside .git, so never committed): other
//     processes, i.e. serve, the launchd or CLI sync and the CLI decide.
//
// So a commit never stages another writer's half-written files, a rebase
// never meets a commit, and a reader never sees a tree mid-rebase or
// mid-write. The lock is held per step and never while taking another lock;
// the machine's sync lock (app.LockSync) and serve's remote lock come before
// it (serve/push.go documents the whole order). Nothing that holds it calls
// Batch, Read, Commit, Decide or Sync again: it is not reentrant.

// lockPoll is how often a wait for the file lock tries again.
const lockPoll = 10 * time.Millisecond

// RebaseTimeout bounds Sync's rebase, which runs on a context its caller's
// cancel doesn't end (a rebase stopped halfway would leave casebook-data
// mid-rebase).
var RebaseTimeout = 2 * time.Minute

// LockPath is the file casebook-data's lock flocks: <git dir>/casebook.lock.
func (r *Repo) LockPath() string {
	gd := filepath.Join(r.Dir, ".git")
	if fi, err := os.Stat(gd); err == nil && !fi.IsDir() {
		// A linked worktree or submodule: .git names the git dir.
		if b, err := os.ReadFile(gd); err == nil {
			if d, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: "); ok {
				if !filepath.IsAbs(d) {
					d = filepath.Join(r.Dir, d)
				}
				gd = d
			}
		}
	}
	return filepath.Join(gd, "casebook.lock")
}

// lock takes casebook-data's lock, exclusive or shared, waiting for other
// holders (in this process or another) until ctx ends.
func (r *Repo) lock(ctx context.Context, exclusive bool) (unlock func(), err error) {
	how := syscall.LOCK_SH
	release := r.mu.RUnlock
	if exclusive {
		how = syscall.LOCK_EX
		release = r.mu.Unlock
		r.mu.Lock()
	} else {
		r.mu.RLock()
	}
	f, err := os.OpenFile(r.LockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		release()
		return nil, fmt.Errorf("casebook-data lock: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = f.Close()
			release()
			return nil, fmt.Errorf("casebook-data lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			release()
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		release()
	}, nil
}

// Batch runs write and then commits everything with msg, all under
// casebook-data's exclusive lock: nobody else's commit can stage what write
// has written so far, and nobody reads the tree halfway. write may write
// files (WriteFile, AppendFile, os calls) but must not call another locking
// Repo method. A nil write just commits. When write fails nothing is
// committed. It reports whether a commit was made. A BatchWithin that died
// before its commit is committed first, under its own message (pending).
func (r *Repo) Batch(ctx context.Context, msg string, write func() error) (bool, error) {
	unlock, err := r.lock(ctx, true)
	if err != nil {
		return false, err
	}
	defer unlock()
	if err := r.commitPending(ctx); err != nil {
		return false, err
	}
	if write != nil {
		if err := write(); err != nil {
			return false, err
		}
	}
	return r.commit(ctx, msg)
}

// ErrLocked means casebook-data's lock stayed held by another writer for
// the whole wait BatchWithin allows.
var ErrLocked = errors.New("casebook-data is locked by another casebook process")

// BatchWithin is Batch that waits at most wait for casebook-data's lock and
// then gives up with ErrLocked, having written nothing. Once the lock is
// held, the write and the commit run on ctx alone.
//
// Its message is recorded as pending (PendingPath) until its commit is
// made, so a write that dies halfway (a crash, a kill, a failed write) is
// not left to be swept into someone else's commit: the next writer under
// the lock (Batch, BatchWithin, or Sync's rebase) commits what it left with
// this message first.
func (r *Repo) BatchWithin(ctx context.Context, wait time.Duration, msg string, write func() error) (bool, error) {
	lctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	unlock, err := r.lock(lctx, true)
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return false, ErrLocked
		}
		return false, err
	}
	defer unlock()
	if err := r.commitPending(ctx); err != nil {
		return false, err
	}
	if err := os.WriteFile(r.PendingPath(), []byte(msg), 0o600); err != nil {
		return false, err
	}
	if write != nil {
		if err := write(); err != nil {
			return false, err
		}
	}
	ok, err := r.commit(ctx, msg)
	if err != nil {
		return false, err
	}
	return ok, os.Remove(r.PendingPath())
}

// PendingPath holds the message of a BatchWithin whose commit isn't made
// yet: <git dir>/casebook-pending, beside the lock, so never committed.
func (r *Repo) PendingPath() string {
	return filepath.Join(filepath.Dir(r.LockPath()), "casebook-pending")
}

// commitPending commits what a BatchWithin that died before its commit
// left in the working tree, under that batch's message, and clears it.
// casebook-data's exclusive lock is held, so nothing else is half-written.
func (r *Repo) commitPending(ctx context.Context) error {
	b, err := os.ReadFile(r.PendingPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if msg := strings.TrimSpace(string(b)); msg != "" {
		if _, err := r.commit(ctx, msg); err != nil {
			return err
		}
	}
	return os.Remove(r.PendingPath())
}

// Read runs fn under casebook-data's shared lock: no write, commit or rebase
// (in this process or another) runs meanwhile, so fn sees one whole tree.
// fn must not call a locking Repo method.
func (r *Repo) Read(ctx context.Context, fn func() error) error {
	unlock, err := r.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// ignoreRule is casebook-data's .gitignore rule for the temp files a write
// leaves beside a file before renaming it over (tools.WriteFileAtomic's
// .<name>-*.tmp): a stray one is never committed.
const ignoreRule = ".*.tmp"

const gitignore = "# casebook's own temp files (written beside a file, then renamed over it).\n" + ignoreRule + "\n"

// hasIgnoreRule reports whether casebook-data's .gitignore has ignoreRule.
func (r *Repo) hasIgnoreRule() bool {
	b, err := r.ReadFile(".gitignore")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == ignoreRule {
			return true
		}
	}
	return false
}

// EnsureIgnore gives a repo from before the rule its .gitignore line for
// casebook's temp files, with one commit ("ignore casebook's temp files"),
// keeping any other lines. It does not push. false when the rule is there.
func (r *Repo) EnsureIgnore(ctx context.Context) (bool, error) {
	if r.hasIgnoreRule() {
		return false, nil
	}
	return r.Batch(ctx, "ignore casebook's temp files", func() error {
		if r.hasIgnoreRule() {
			return nil
		}
		b, err := r.ReadFile(".gitignore")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
			b = append(b, '\n')
		}
		_, err = r.WriteFile(".gitignore", append(b, gitignore...))
		return err
	})
}
