package serve

// The slow page (casebook 0.4.2): a heartbeat for a session the page shows
// unchanged publishes nothing; GET /api/sessions answers only what the page
// uses; serve prunes the event log when it starts.

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/db"
)

// sessionsEvents counts the "sessions" events published after cursor.
func (r *rig) sessionsEvents(t *testing.T, after int64) int {
	t.Helper()
	evs, _, err := r.s.Bus.Since(ctx, after, 10000)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range evs {
		if e.Type == "sessions" {
			n++
		}
	}
	return n
}

// publishes runs step and returns how many "sessions" events it published.
func (r *rig) publishes(t *testing.T, step func()) int {
	t.Helper()
	before, err := r.s.Bus.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	step()
	return r.sessionsEvents(t, before)
}

func (r *rig) beat(t *testing.T, id, label string) {
	t.Helper()
	if c := r.do(t, "POST", "/api/agent/presence", map[string]any{"id": id, "harness": "pi", "label": label, "cwd": "/w", "pid": 1}, nil); c != 200 {
		t.Fatalf("presence %d", c)
	}
}

func (r *rig) info(t *testing.T, in map[string]any) {
	t.Helper()
	if c := r.do(t, "POST", "/api/agent/session-info", in, nil); c != 200 {
		t.Fatalf("session-info %d", c)
	}
}

func TestPresenceHeartbeatPublishesOnlyOnChange(t *testing.T) {
	r := newRig(t)
	r.s.Queue.LeftAfter = 300 * time.Millisecond

	if n := r.publishes(t, func() { r.beat(t, "s1", "pi · ws") }); n != 1 {
		t.Fatalf("a new session published %d sessions events, want 1", n)
	}
	for i := 0; i < 3; i++ {
		if n := r.publishes(t, func() { r.beat(t, "s1", "pi · ws") }); n != 0 {
			t.Fatalf("heartbeat %d for an unchanged session published %d sessions events, want 0", i, n)
		}
	}
	if n := r.publishes(t, func() { r.beat(t, "s1", "pi · other") }); n != 1 {
		t.Fatalf("a label change published %d, want 1", n)
	}
	if n := r.publishes(t, func() { r.info(t, map[string]any{"id": "s1", "name": "alpha", "pid": 1}) }); n != 1 {
		t.Fatalf("a rename published %d, want 1", n)
	}
	if n := r.publishes(t, func() { r.info(t, map[string]any{"id": "s1", "name": "alpha", "pid": 1}) }); n != 0 {
		t.Fatalf("the same name again published %d, want 0", n)
	}

	// Crossing into left: no heartbeat says so; the watch's leave announcer
	// does, once.
	time.Sleep(400 * time.Millisecond)
	known := map[string]bool{"s1": false}
	if n := r.publishes(t, func() { r.s.checkLeftCrossings(ctx, known) }); n != 1 {
		t.Fatalf("crossing into left published %d, want 1", n)
	}
	if n := r.publishes(t, func() { r.s.checkLeftCrossings(ctx, known) }); n != 0 {
		t.Fatalf("a second tick, still left, published %d, want 0", n)
	}
	// Back: the heartbeat that brings it back out of left publishes once.
	if n := r.publishes(t, func() { r.beat(t, "s1", "pi · other") }); n != 1 {
		t.Fatalf("a heartbeat out of left published %d, want 1", n)
	}
	if n := r.publishes(t, func() { r.beat(t, "s1", "pi · other") }); n != 0 {
		t.Fatalf("the next heartbeat published %d, want 0", n)
	}

	// Ended: once; an end reported again changes nothing.
	if n := r.publishes(t, func() { r.info(t, map[string]any{"id": "s1", "ended": true}) }); n != 1 {
		t.Fatalf("an end published %d, want 1", n)
	}
	if n := r.publishes(t, func() { r.info(t, map[string]any{"id": "s1", "ended": true}) }); n != 0 {
		t.Fatalf("the same end again published %d, want 0", n)
	}

	// A queued message changes the session's queued count: the next beat,
	// had the count moved since the last one shown, says so. (Sending
	// publishes its own events; the beat after it is unchanged.)
	r.beat(t, "s2", "pi · two")
	var th thread
	r.do(t, "POST", "/api/threads", map[string]any{"session": "s2", "name": "x"}, &th)
	r.send(t, th.ID, "hello", false)
	if n := r.publishes(t, func() { r.beat(t, "s2", "pi · two") }); n != 0 {
		t.Fatalf("a heartbeat after a send (count already announced) published %d, want 0", n)
	}
}

func sessionIDs(t *testing.T, r *rig, path string) []string {
	t.Helper()
	var sv struct {
		Sessions []struct {
			ID   string `json:"id"`
			Left bool   `json:"left"`
		} `json:"sessions"`
	}
	if c := r.do(t, "GET", path, nil, &sv); c != 200 {
		t.Fatalf("GET %s: %d", path, c)
	}
	var ids []string
	for _, s := range sv.Sessions {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestSessionsListIsLiveNonWorkersPlusAttached(t *testing.T) {
	r := newRig(t)
	// live: a plain session and a parent
	r.beat(t, "live", "pi · a")
	if c := r.do(t, "POST", "/api/agent/presence", map[string]any{"id": "parent", "harness": "pi", "label": "pi · p", "cwd": "/w", "pid": 500}, nil); c != 200 {
		t.Fatal(c)
	}
	// a subagent worker in the parent's process
	if c := r.do(t, "POST", "/api/agent/presence", map[string]any{"id": "worker", "harness": "pi", "label": "pi · w", "cwd": "/w", "pid": 500}, nil); c != 200 {
		t.Fatal(c)
	}
	r.info(t, map[string]any{"id": "worker", "parent": "parent", "pid": 500})
	// exited
	r.beat(t, "gone", "pi · g")
	r.info(t, map[string]any{"id": "gone", "ended": true})

	if got := strings.Join(sessionIDs(t, r, "/api/sessions"), ","); got != "live,parent" {
		t.Fatalf("GET /api/sessions = %s, want live,parent (no exited session, no worker)", got)
	}
	if got := strings.Join(sessionIDs(t, r, "/api/sessions?session=gone"), ","); got != "gone,live,parent" {
		t.Fatalf("GET /api/sessions?session=gone = %s, want the attached session too", got)
	}
	if got := strings.Join(sessionIDs(t, r, "/api/sessions?session=gone,worker"), ","); got != "gone,live,parent,worker" {
		t.Fatalf("GET /api/sessions?session=gone,worker = %s", got)
	}
	if got := strings.Join(sessionIDs(t, r, "/api/sessions?all=1"), ","); got != "gone,live,parent,worker" {
		t.Fatalf("GET /api/sessions?all=1 = %s, want every session", got)
	}
}

// TestServePrunesEventsAtStart: serve keeps the newest EventsKeep events
// (or the last week's, if more) when it starts.
func TestServePrunesEventsAtStart(t *testing.T) {
	ar := apptest.New(t)
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	old := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	tx, _ := d.Begin()
	for i := 0; i < EventsKeep+500; i++ {
		if _, err := tx.Exec("INSERT INTO events(kind, payload, created_at) VALUES ('sessions', '{}', ?)", old); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, ar.App, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.pushWG.Wait)
	var n, lo int64
	if err := d.QueryRow("SELECT count(*), MIN(cursor) FROM events").Scan(&n, &lo); err != nil {
		t.Fatal(err)
	}
	// New may publish its own few (an index); the old ones beyond the
	// newest EventsKeep are gone.
	if n < EventsKeep || n > EventsKeep+20 || lo <= 500 {
		t.Fatalf("after New: %d events from cursor %d, want about %d from past 500", n, lo, EventsKeep)
	}
}

// TestSummaryCarriesTheLiveCursor: the page starts its live stream at the
// cursor its first summary carries (the log's head when serve answered),
// not at the start of the log.
func TestSummaryCarriesTheLiveCursor(t *testing.T) {
	r := newRig(t)
	r.beat(t, "s1", "pi · ws")
	head, _ := r.s.Bus.Head(ctx)
	var sv struct {
		Cursor *int64 `json:"cursor"`
	}
	if c := r.do(t, "GET", "/api/summary", nil, &sv); c != 200 {
		t.Fatalf("summary %d", c)
	}
	if sv.Cursor == nil || *sv.Cursor != head || head == 0 {
		t.Fatalf("summary cursor %v, want the head %d", sv.Cursor, head)
	}
}

// TestStuckCrossingsPublishDelivery: a delivery going stuck says so. The
// page used to learn it from the next heartbeat's "sessions" event (which
// re-read the delivery); heartbeats no longer publish, so the watch
// announces the crossing, once, as a "delivery" event for the session.
func TestStuckCrossingsPublishDelivery(t *testing.T) {
	r := newRig(t)
	r.s.Queue.StuckAfter = 100 * time.Millisecond
	th := r.attach(t, "s1")
	r.send(t, th, "stuck message", false)
	var w waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); c != 200 {
		t.Fatalf("wait %d", c)
	}
	known := map[int64]bool{}
	deliveries := func(step func()) []string {
		before, _ := r.s.Bus.Head(ctx)
		step()
		evs, _, _ := r.s.Bus.Since(ctx, before, 100)
		var out []string
		for _, e := range evs {
			if e.Type == "delivery" {
				out = append(out, string(e.Data))
			}
		}
		return out
	}
	if got := deliveries(func() { r.s.checkStuckCrossings(ctx, known) }); len(got) != 0 {
		t.Fatalf("not stuck yet: %v", got)
	}
	time.Sleep(200 * time.Millisecond)
	got := deliveries(func() { r.s.checkStuckCrossings(ctx, known) })
	if len(got) != 1 || !strings.Contains(got[0], `"session":"s1"`) || !strings.Contains(got[0], `"stuck":true`) {
		t.Fatalf("crossing into stuck: %v, want one delivery event for s1", got)
	}
	if got := deliveries(func() { r.s.checkStuckCrossings(ctx, known) }); len(got) != 0 {
		t.Fatalf("still stuck, the next tick: %v, want none", got)
	}
}
