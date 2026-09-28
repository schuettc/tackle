package serve

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestPageOpenFlagAtomic verifies that the page_open flag in meta is always
// consistent with the open-stream count even when many goroutines open and
// close event-streams concurrently.
//
// Invariants checked:
//   - while at least one stream is open the flag is "1"
//   - after all streams close (and serve is not stopping) the flag is "0"
//
// Before the fix the counter increment/flag-write were not one critical section,
// so a rapid open→close could leave the flag at "1" after the last close.
func TestPageOpenFlagAtomic(t *testing.T) {
	const N = 20
	r := newRig(t)
	r.do(t, "POST", "/api/agent/presence", map[string]any{"id": "po", "harness": "pi"}, nil)

	flag := func() string {
		var v string
		_ = r.s.DB.QueryRow("SELECT value FROM meta WHERE key = 'page_open'").Scan(&v)
		return v
	}

	// Open N streams concurrently.
	closes := make([]func(), N)
	var openWG sync.WaitGroup
	openWG.Add(N)
	for i := range closes {
		i := i
		go func() {
			defer openWG.Done()
			closes[i] = stream(t, r.url, "")
		}()
	}
	openWG.Wait()

	// While at least one stream is open the flag must reach "1".
	eventually(t, "page_open flag to become 1", func() bool { return flag() == "1" })

	// Close all streams concurrently.
	var closeWG sync.WaitGroup
	closeWG.Add(N)
	for _, c := range closes {
		c := c
		go func() {
			defer closeWG.Done()
			c()
		}()
	}
	closeWG.Wait()

	// After all streams close the flag must return to "0".
	eventually(t, "page_open flag to return to 0", func() bool { return flag() == "0" })

	// Verify no stream is counted as open.
	if r.s.streams.Load() != 0 {
		t.Fatalf("streams counter = %d after all closed", r.s.streams.Load())
	}

	// Second round: rapid interleaved open/close must leave flag at "0".
	for range 5 {
		cls := stream(t, r.url, "")
		time.Sleep(time.Millisecond)
		cls()
	}
	eventually(t, "page_open flag to be 0 after rapid open/close", func() bool { return flag() == "0" })

	// Status endpoint must also report not open.
	var st struct {
		PageOpen bool `json:"page_open"`
	}
	if c := r.do(t, "GET", "/api/agent/status?session=po", nil, &st); c != http.StatusOK {
		t.Fatalf("status: got %d", c)
	}
	if st.PageOpen {
		t.Fatal("PageOpen is true after all streams closed")
	}
}
