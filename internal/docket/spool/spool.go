// Package spool is the machine-local queue between the git hooks / harness
// recorders and `docket sync`. Appenders hold a shared flock while writing one
// JSON line; Drain takes the exclusive lock only to rename the live file, so
// no event is written into a file that is already being read.
package spool

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/schuettc/tackle/internal/docket/journal"
)

const live = "events.jsonl"

// Append writes ev as one line. It waits at most 300ms for the lock and gives
// up silently (returns an error the caller ignores) rather than block a hook.
func Append(dir string, ev journal.Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	unlock, err := lock(dir, syscall.LOCK_SH, 300*time.Millisecond)
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(filepath.Join(dir, live), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ErrBusy means another sync is draining the spool right now.
var ErrBusy = errors.New("another docket sync is draining the spool")

// Batch is a set of drained events. They stay on disk until Done, so a sync
// that fails before committing them sees them again next time. A Batch holds
// the drain lock until Done or Close.
type Batch struct {
	Events []journal.Event
	Bad    int // unparseable lines, skipped
	files  []string
	unlock func()
}

// Drain moves the live spool aside and returns every pending event, oldest
// file first. Only one Drain can be open at a time (ErrBusy otherwise).
func Drain(dir string) (*Batch, error) {
	drainUnlock, err := lockFile(dir, ".drain.lock", syscall.LOCK_EX, 0)
	if err != nil {
		return nil, ErrBusy
	}
	b := &Batch{unlock: drainUnlock}
	unlock, err := lock(dir, syscall.LOCK_EX, 5*time.Second)
	if err != nil {
		b.Close()
		return nil, err
	}
	src := filepath.Join(dir, live)
	if _, err := os.Stat(src); err == nil {
		dst := filepath.Join(dir, fmt.Sprintf("draining-%d.jsonl", time.Now().UnixNano()))
		if err := os.Rename(src, dst); err != nil {
			unlock()
			b.Close()
			return nil, err
		}
	}
	unlock()
	files, err := filepath.Glob(filepath.Join(dir, "draining-*.jsonl"))
	if err != nil {
		b.Close()
		return nil, err
	}
	sort.Strings(files)
	b.files = files
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			b.Close()
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var ev journal.Event
			if json.Unmarshal(sc.Bytes(), &ev) != nil {
				b.Bad++
				continue
			}
			b.Events = append(b.Events, ev)
		}
		f.Close()
	}
	return b, nil
}

// Done deletes the drained files and releases the drain lock; call it after
// the events are committed.
func (b *Batch) Done() error {
	defer b.Close()
	for _, p := range b.files {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Close releases the drain lock without deleting anything. Safe to call twice.
func (b *Batch) Close() {
	if b.unlock != nil {
		b.unlock()
		b.unlock = nil
	}
}

func lock(dir string, how int, wait time.Duration) (func(), error) {
	return lockFile(dir, ".lock", how, wait)
}

func lockFile(dir, name string, how int, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil //nolint:errcheck
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("spool lock busy: %w", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
