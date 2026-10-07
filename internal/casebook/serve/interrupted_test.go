package serve

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

// interruptedView mirrors InterruptedView for the test.
type interruptedView struct {
	StartedAt  time.Time `json:"started_at"`
	Deliveries []int64   `json:"deliveries"`
	Messages   []int64   `json:"messages"`
}

// restartRig is a serve over a database a "previous serve" left behind:
// each restart is a new Server over the same database.
func (r *rig) restart(t *testing.T, d *db.DB) *rig {
	t.Helper()
	s, err := New(ctx, r.App, d)
	if err != nil {
		t.Fatal(err)
	}
	s.Wait = 2 * time.Second
	t.Cleanup(s.pushWG.Wait)
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return &rig{Rig: r.Rig, s: s, url: hs.URL}
}

// GET /api/agent/interrupted answers, for one session, what this serve's
// start interrupted of ITS deliveries: nothing for a session that had
// nothing in flight, nothing after a later restart, and 404
// unknown_session for a session serve doesn't know.
func TestInterruptedIsPerSessionAndPerStart(t *testing.T) {
	r := newRig(t)
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	r = r.restart(t, d) // a serve over a database this test can reopen

	thA := r.attach(t, "a")
	r.attach(t, "b")
	m1 := r.send(t, thA, "look at #3", false)
	m2 := r.send(t, thA, "and #4", false)
	var w waited
	if c := r.do(t, "GET", "/api/agent/wait?session=a", nil, &w); c != 200 || len(w.Delivery.Messages) != 2 {
		t.Fatalf("wait %d %+v", c, w)
	}
	// The agent settles one of the two before serve goes away.
	if c := r.do(t, "POST", "/api/agent/reply", map[string]any{"session": "a", "ids": []int64{m1.ID}, "state": "answered", "text": "done"}, nil); c != 200 {
		t.Fatalf("reply %d", c)
	}

	// Restart: A's delivery (m2 still unsettled) is interrupted.
	r2 := r.restart(t, d)
	var got interruptedView
	if c := r2.do(t, "GET", "/api/agent/interrupted?session=a", nil, &got); c != 200 {
		t.Fatalf("a: %d", c)
	}
	if len(got.Deliveries) != 1 || got.Deliveries[0] != w.Delivery.ID || len(got.Messages) != 1 || got.Messages[0] != m2.ID {
		t.Fatalf("a: %+v, want delivery %d message %d", got, w.Delivery.ID, m2.ID)
	}
	if !got.StartedAt.Equal(r2.s.StartedAt()) || got.StartedAt.IsZero() {
		t.Fatalf("started_at %v, serve's %v", got.StartedAt, r2.s.StartedAt())
	}
	got = interruptedView{}
	if c := r2.do(t, "GET", "/api/agent/interrupted?session=b", nil, &got); c != 200 || got.Deliveries == nil || len(got.Deliveries) != 0 || got.Messages == nil || len(got.Messages) != 0 {
		t.Fatalf("b: %d %+v", c, got)
	}
	var e struct {
		Code string `json:"code"`
	}
	if c := r2.do(t, "GET", "/api/agent/interrupted?session=ghost", nil, &e); c != 404 || e.Code != "unknown_session" {
		t.Fatalf("ghost: %d %+v", c, e)
	}
	if c := r2.do(t, "GET", "/api/agent/interrupted", nil, nil); c != 400 {
		t.Fatalf("no session: %d", c)
	}

	// Restart again with nothing in flight: no one was interrupted.
	r3 := r2.restart(t, d)
	for _, s := range []string{"a", "b"} {
		got = interruptedView{}
		if c := r3.do(t, "GET", "/api/agent/interrupted?session="+s, nil, &got); c != 200 || len(got.Deliveries) != 0 || len(got.Messages) != 0 {
			t.Fatalf("%s after restart 2: %d %+v", s, c, got)
		}
	}
}
