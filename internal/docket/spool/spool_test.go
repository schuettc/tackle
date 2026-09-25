package spool

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/docket/journal"
)

func TestAppendDrainNoLoss(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Append(dir, journal.Event{V: 1, Src: "git-hook", Hook: "post-commit", TS: time.Unix(int64(i), 0)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	var drained []journal.Event
	for i := 0; i < 5; i++ { // drain while the appenders run
		b, err := Drain(dir)
		if err != nil {
			t.Fatal(err)
		}
		drained = append(drained, b.Events...)
		if err := b.Done(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	b, err := Drain(dir)
	if err != nil {
		t.Fatal(err)
	}
	drained = append(drained, b.Events...)
	b.Close()
	if len(drained) != 50 {
		t.Fatalf("drained %d events, want 50", len(drained))
	}
}

func TestDrainRedeliversUntilDone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := Append(dir, journal.Event{V: 1, Src: "claude"}); err != nil {
		t.Fatal(err)
	}
	b1, _ := Drain(dir)
	if _, err := Drain(dir); !errors.Is(err, ErrBusy) {
		t.Fatalf("second concurrent Drain: %v, want ErrBusy", err)
	}
	b1.Close()
	b2, _ := Drain(dir)
	if len(b1.Events) != 1 || len(b2.Events) != 1 {
		t.Fatalf("got %d then %d", len(b1.Events), len(b2.Events))
	}
	if err := b2.Done(); err != nil {
		t.Fatal(err)
	}
	b3, _ := Drain(dir)
	defer b3.Close()
	if len(b3.Events) != 0 {
		t.Fatalf("after Done: %d", len(b3.Events))
	}
}

func TestDrainCountsBadLinesAndFileModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := Append(dir, journal.Event{V: 1}); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{not json\n")
	f.Close()
	fi, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("spool dir mode %v", fi.Mode().Perm())
	}
	b, err := Drain(dir)
	if err != nil || len(b.Events) != 1 || b.Bad != 1 {
		t.Fatalf("got %+v %v", b, err)
	}
	b.Close()
}
