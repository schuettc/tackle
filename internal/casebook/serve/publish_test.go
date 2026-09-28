package serve

import (
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/bus"
	"github.com/schuettc/tackle/internal/casebook/db"
)

// TestPublishFailureDoesNotFailRequest verifies that a Bus.Publish failure
// (e.g. the events table is unavailable) is logged to stderr and does not
// cause the enclosing HTTP handler to return an error to the caller.
//
// We inject a Bus backed by a separately-opened and immediately-closed DB so
// that Publish fails with "sql: database is closed", while the server's main
// DB (used by Queue, Props, Apply, etc.) stays open and serves the request
// normally.
func TestPublishFailureDoesNotFailRequest(t *testing.T) {
	r := newRig(t)

	// Open a second DB just for the Bus and close it immediately so every
	// Publish call returns an error.
	badDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "bad.db"))
	if err != nil {
		t.Fatal(err)
	}
	r.s.Bus = bus.New(badDB)
	if err := badDB.Close(); err != nil {
		t.Fatal(err)
	}

	// POST /api/agent/presence: Queue.Touch succeeds (main DB is fine), then
	// s.publish("sessions", …) hits the dead Bus.  The handler must still
	// reply 200.
	var out any
	code := r.do(t, "POST", "/api/agent/presence",
		map[string]string{"id": "probe-publish-fail"}, &out)
	if code != 200 {
		t.Fatalf("expected 200 after publish failure, got %d", code)
	}
}
