package proj

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// recordHome points proj's state dir at a fresh temp dir so record tests
// never touch the operator's real ~/.local/state/proj.
func recordHome(t *testing.T) {
	t.Helper()
	t.Setenv("PROJ_HOME", t.TempDir())
}

func TestLoadRecordMissingIsEmpty(t *testing.T) {
	recordHome(t)
	rec, err := LoadRecord()
	if err != nil {
		t.Fatalf("LoadRecord: %v", err)
	}
	if rec.Version != 1 || len(rec.Sessions) != 0 {
		t.Fatalf("got version=%d sessions=%d, want 1 and 0", rec.Version, len(rec.Sessions))
	}
	if rec.Sessions == nil {
		t.Fatal("Sessions is nil; want an empty map callers can write to")
	}
}

func liveAW() LiveEntry {
	return LiveEntry{Name: "a/w", Entry: Entry{Socket: "proj-a", Project: "a", Dir: "/x", Agent: "pi", Conversation: "id1"}}
}

func TestMergeUpsertsAndStamps(t *testing.T) {
	rec := Record{Version: 1, Sessions: map[string]Entry{}}
	now := time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC)
	if !mergeLive(&rec, []LiveEntry{liveAW()}, now) {
		t.Fatal("first merge reported no change")
	}
	e, ok := rec.Sessions["a/w"]
	if !ok || e.Conversation != "id1" || e.Dir != "/x" || e.Agent != "pi" || e.Socket != "proj-a" {
		t.Fatalf("entry = %+v (ok=%v)", e, ok)
	}
	if !e.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v want %v", e.UpdatedAt, now)
	}
	if mergeLive(&rec, []LiveEntry{liveAW()}, now.Add(time.Hour)) {
		t.Fatal("identical merge reported a change")
	}
	if !rec.Sessions["a/w"].UpdatedAt.Equal(now) {
		t.Fatal("identical merge moved UpdatedAt")
	}
}

func TestMergeNeverDeletes(t *testing.T) {
	rec := Record{Version: 1, Sessions: map[string]Entry{
		"a/w": {Socket: "proj-a"},
		"b/w": {Socket: "proj-b"},
	}}
	if mergeLive(&rec, nil, time.Now()) {
		t.Fatal("empty merge reported a change")
	}
	if len(rec.Sessions) != 2 {
		t.Fatalf("sessions = %v; an empty snapshot must delete nothing", rec.Sessions)
	}
}

func TestMergeRenameReplaces(t *testing.T) {
	now := time.Now()
	rec := Record{Version: 1, Sessions: map[string]Entry{
		"a/old": {Socket: "proj-a", Conversation: "id1"},
	}}
	renamed := LiveEntry{Name: "a/new", Entry: Entry{Socket: "proj-a", Conversation: "id1"}}
	mergeLive(&rec, []LiveEntry{renamed}, now)
	if _, ok := rec.Sessions["a/old"]; ok {
		t.Fatal("renamed session left its old name behind")
	}
	if _, ok := rec.Sessions["a/new"]; !ok {
		t.Fatal("renamed session missing under its new name")
	}

	// Both names live with the same conversation: nothing is replaced.
	rec = Record{Version: 1, Sessions: map[string]Entry{"a/old": {Socket: "proj-a", Conversation: "id1"}}}
	old := LiveEntry{Name: "a/old", Entry: Entry{Socket: "proj-a", Conversation: "id1"}}
	mergeLive(&rec, []LiveEntry{old, renamed}, now)
	if len(rec.Sessions) != 2 {
		t.Fatalf("sessions = %v; a live old name must be kept", rec.Sessions)
	}

	// An empty conversation never matches another entry.
	rec = Record{Version: 1, Sessions: map[string]Entry{"a/old": {Socket: "proj-a"}}}
	mergeLive(&rec, []LiveEntry{{Name: "a/new", Entry: Entry{Socket: "proj-a"}}}, now)
	if _, ok := rec.Sessions["a/old"]; !ok {
		t.Fatal("empty conversation triggered a rename replacement")
	}
}

func TestMergeKeepsAgentWhenLiveUnknown(t *testing.T) {
	rec := Record{Version: 1, Sessions: map[string]Entry{"a/w": {Socket: "proj-a", Agent: "claude"}}}
	mergeLive(&rec, []LiveEntry{{Name: "a/w", Entry: Entry{Socket: "proj-a"}}}, time.Now())
	if got := rec.Sessions["a/w"].Agent; got != "claude" {
		t.Fatalf("Agent = %q want claude", got)
	}
}

func TestUpdateRecordVersionGuard(t *testing.T) {
	recordHome(t)
	p := RecordPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	future := []byte(`{"version":2,"sessions":{}}`)
	if err := os.WriteFile(p, future, 0o600); err != nil {
		t.Fatal(err)
	}
	err := UpdateRecord(func(*Record) bool { return true })
	if !errors.Is(err, ErrRecordVersion) {
		t.Fatalf("err = %v want ErrRecordVersion", err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(future) {
		t.Fatalf("record rewritten to %q", b)
	}
	if _, err := LoadRecord(); !errors.Is(err, ErrRecordVersion) {
		t.Fatalf("LoadRecord err = %v want ErrRecordVersion", err)
	}
}

func TestUpdateConcurrentWritersBothLand(t *testing.T) {
	recordHome(t)
	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				if err := UpdateRecord(func(r *Record) bool {
					r.Sessions[name] = Entry{Socket: "proj-x"}
					return true
				}); err != nil {
					t.Errorf("UpdateRecord: %v", err)
				}
			}(fmt.Sprintf("x/%d-%d", g, i))
		}
	}
	wg.Wait()
	rec, err := LoadRecord()
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Sessions) != 40 {
		t.Fatalf("got %d entries want 40", len(rec.Sessions))
	}
}

func TestForget(t *testing.T) {
	recordHome(t)
	if err := UpdateRecord(func(r *Record) bool { r.Sessions["a/w"] = Entry{Socket: "proj-a"}; return true }); err != nil {
		t.Fatal(err)
	}
	if err := Forget("a/w"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	rec, _ := LoadRecord()
	if _, ok := rec.Sessions["a/w"]; ok {
		t.Fatal("entry survived Forget")
	}
	if err := Forget("missing/w"); err != nil {
		t.Fatalf("Forget(missing) = %v want nil", err)
	}
}
