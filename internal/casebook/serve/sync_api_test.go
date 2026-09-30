package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/config"
)

// syncEvents returns the "sync" and "index" events published after cursor,
// in order, as "sync running", "index", "sync done", "sync failed: <why>".
func syncEvents(t *testing.T, r *rig, cursor int64) []string {
	t.Helper()
	evs, _, err := r.s.Bus.Since(ctx, cursor, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		switch e.Type {
		case "index":
			out = append(out, "index")
		case "sync":
			var se SyncEvent
			if err := json.Unmarshal(e.Data, &se); err != nil {
				t.Fatal(err)
			}
			s := "sync " + se.State
			if se.Error != "" {
				s += ": " + se.Error
			}
			out = append(out, s)
		}
	}
	return out
}

// waitSyncEnd waits for a "sync done" or "sync failed" event after cursor.
func waitSyncEnd(t *testing.T, r *rig, cursor int64) []string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		evs := syncEvents(t, r, cursor)
		for _, e := range evs {
			if e == "sync done" || strings.HasPrefix(e, "sync failed") {
				return evs
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no sync end announced; got %v", syncEvents(t, r, cursor))
	return nil
}

// TestSyncRouteRunsOneSyncAndAnnouncesIt: POST /api/sync starts the sync in
// the background and answers at once; requests while it runs join it (one
// sync, not two); the live wire says it started, then (after the index is
// rebuilt from it) that it is done; the summary says a sync is running
// meanwhile, and gives the sync interval the page ages observations by.
func TestSyncRouteRunsOneSyncAndAnnouncesIt(t *testing.T) {
	r := newRig(t)
	var calls atomic.Int32
	release := make(chan struct{})
	r.s.runSync = func(context.Context) error {
		calls.Add(1)
		<-release
		return nil
	}
	before, _ := r.s.Bus.Head(ctx)

	var first, second, third SyncView
	if c := r.do(t, "POST", "/api/sync", map[string]any{}, &first); c != http.StatusOK {
		t.Fatalf("POST /api/sync: %d", c)
	}
	if !first.Running || !first.Started {
		t.Fatalf("first request: %+v, want running and started", first)
	}
	for i := 0; i < 3; i++ {
		if c := r.do(t, "POST", "/api/sync", map[string]any{}, &second); c != http.StatusOK {
			t.Fatalf("POST /api/sync again: %d", c)
		}
		if !second.Running || second.Started {
			t.Fatalf("a request while syncing: %+v, want running, not started", second)
		}
	}
	var sum SummaryView
	r.do(t, "GET", "/api/summary", nil, &sum)
	if !sum.Syncing {
		t.Error("summary: syncing = false while the sync runs")
	}
	if sum.SyncIntervalMS != (30 * time.Minute).Milliseconds() {
		t.Errorf("summary: sync_interval_ms = %d, want the 30m default", sum.SyncIntervalMS)
	}
	// Give a second sync every chance to start, then check it didn't.
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("syncs run = %d while one was running, want 1", n)
	}
	if got := syncEvents(t, r, before); strings.Join(got, ",") != "sync running" {
		t.Fatalf("events while syncing = %v, want [sync running]", got)
	}

	close(release)
	got := waitSyncEnd(t, r, before)
	if strings.Join(got, ",") != "sync running,index,sync done" {
		t.Fatalf("events = %v, want [sync running index sync done]", got)
	}
	r.do(t, "GET", "/api/summary", nil, &sum)
	if sum.Syncing {
		t.Error("summary: syncing = true after the sync ended")
	}

	// A request after it ended starts a new one.
	r.s.runSync = func(context.Context) error { calls.Add(1); return nil }
	if c := r.do(t, "POST", "/api/sync", map[string]any{}, &third); c != http.StatusOK || !third.Started {
		t.Fatalf("after the sync ended: %d %+v, want a new sync started", c, third)
	}
	mid, _ := r.s.Bus.Head(ctx)
	_ = waitSyncEnd(t, r, mid-1)
	if n := calls.Load(); n != 2 {
		t.Fatalf("syncs run = %d, want 2", n)
	}
}

// TestSyncRouteAnnouncesAFailure: a sync that fails (here another process
// holds the machine's sync lock) is announced as failed, in serve's words,
// and the index is not rebuilt.
func TestSyncRouteAnnouncesAFailure(t *testing.T) {
	r := newRig(t)
	r.s.runSync = func(context.Context) error { return app.ErrSyncBusy }
	before, _ := r.s.Bus.Head(ctx)
	if c := r.do(t, "POST", "/api/sync", map[string]any{}, nil); c != http.StatusOK {
		t.Fatalf("POST /api/sync: %d", c)
	}
	got := waitSyncEnd(t, r, before)
	want := "sync running,sync failed: another casebook sync is running on this machine"
	if strings.Join(got, ",") != want {
		t.Fatalf("events = %v, want %s", got, want)
	}
	if r.s.Syncing() {
		t.Error("still syncing after the failure")
	}
}

// TestSyncRouteRunsTheRealSync: with no seam, the route runs App.Sync (the
// rig's hermetic casebook: a local bare remote, the fake gh). A plan refused
// because the observation is stale builds once the sync is done: the index
// is rebuilt from the sync, and its built_at is new.
func TestSyncRouteRunsTheRealSync(t *testing.T) {
	r := newRig(t)
	// Something to plan once the observation is fresh (nothing decided is a
	// refusal of its own, 422).
	if c := r.do(t, "POST", "/api/decide", map[string]any{
		"keys": []string{"pr:schuettc/hail#3"}, "disposition": "close", "note": "stale",
	}, nil); c != http.StatusOK {
		t.Fatalf("decide: %d", c)
	}
	stale := time.Now().Add(-2 * time.Hour)
	r.s.Index.set(r.s.Index.Result(), r.s.Index.Head(), stale)
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, nil); c != http.StatusConflict {
		t.Fatalf("stale plan: %d, want 409", c)
	}
	// App.Sync refreshes the GitHub cache (the summary's synced_at is its
	// mtime): age it, so a sync that ran shows.
	if err := os.Chtimes(config.CachePath(), stale, stale); err != nil {
		t.Fatal(err)
	}
	before, _ := r.s.Bus.Head(ctx)
	if c := r.do(t, "POST", "/api/sync", map[string]any{}, nil); c != http.StatusOK {
		t.Fatalf("POST /api/sync: %d", c)
	}
	got := waitSyncEnd(t, r, before)
	if got[len(got)-1] != "sync done" {
		t.Fatalf("events = %v, want the sync done", got)
	}
	if !r.s.Index.BuiltAt().After(stale.Add(time.Hour)) {
		t.Fatalf("built_at = %v after the sync, want now", r.s.Index.BuiltAt())
	}
	var sum SummaryView
	r.do(t, "GET", "/api/summary", nil, &sum)
	if time.Since(sum.SyncedAt) > time.Minute {
		t.Errorf("synced_at = %v after the sync, want now (App.Sync refreshed the GitHub cache)", sum.SyncedAt)
	}
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"all": true}, nil); c != http.StatusOK {
		t.Fatalf("plan after the sync: %d, want 200", c)
	}
}
