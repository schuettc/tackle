package serve

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/sift/store"
)

func (f *fixture) presence(session, label string) {
	f.t.Helper()
	if w := f.do("POST", "/api/agent/presence", fmt.Sprintf(`{"session":%q,"harness":"pi","label":%q}`, session, label)); w.Code != 204 {
		f.t.Fatalf("presence %d %s", w.Code, w.Body)
	}
}

func (f *fixture) wait(session string) *httptest.ResponseRecorder {
	return f.do("GET", "/api/agent/wait?session="+session+"&timeout=1", "")
}

func (f *fixture) sendNow() {
	f.t.Helper()
	f.decide("r-neg", "reject", `,"note":"keep it"`)
	if w := f.do("POST", "/api/send", fmt.Sprintf(`{"round":%d}`, f.round)); w.Code != 200 {
		f.t.Fatalf("send %d", w.Code)
	}
}

func TestReviewMakesTheCallerTheOwner(t *testing.T) {
	f := newFixture(t)
	f.presence("a", "pi · a")
	w := f.do("POST", "/api/agent/review", `{"session":"a"}`)
	var got struct {
		Round int64 `json:"round"`
		Open  int   `json:"open"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Round != f.round || got.Open != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if f.review().Round.Owner != "pi · a" {
		t.Fatalf("owner %q", f.review().Round.Owner)
	}
}

func TestSendGoesToThePresentOwner(t *testing.T) {
	f := newFixture(t)
	f.srv.waitUnit = 20 * time.Millisecond
	f.presence("owner", "pi · owner")
	f.presence("other", "pi · other")
	f.do("POST", "/api/agent/review", `{"session":"owner"}`)
	f.sendNow()
	if w := f.wait("other"); w.Code != 204 {
		t.Fatalf("another session took the owner's send: %d %s", w.Code, w.Body)
	}
	w := f.wait("owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "keep it") {
		t.Fatalf("owner: %d %s", w.Code, w.Body)
	}
}

func TestOwnerGoneAnySessionClaims(t *testing.T) {
	f := newFixture(t)
	f.srv.waitUnit = 20 * time.Millisecond
	now := time.Now()
	f.srv.now = func() time.Time { return now }
	f.presence("owner", "")
	f.do("POST", "/api/agent/review", `{"session":"owner"}`)
	f.sendNow()
	now = now.Add(presenceTTL + time.Second)
	if w := f.wait("next"); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestTwoWaitersOneSend(t *testing.T) {
	f := newFixture(t)
	f.srv.waitUnit = 50 * time.Millisecond
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func() { defer wg.Done(); codes[i] = f.wait(fmt.Sprintf("s%d", i)).Code }()
	}
	time.Sleep(20 * time.Millisecond)
	f.sendNow()
	wg.Wait()
	if codes[0]+codes[1] != 200+204 {
		t.Fatalf("codes %v", codes)
	}
}

func TestStatus(t *testing.T) {
	f := newFixture(t)
	f.decide("r-neg", "accept", "")
	w := f.do("GET", "/api/agent/status", "")
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["open"] != float64(1) || got["decided"] != float64(1) || got["applied"] != float64(1) || got["round"] != float64(f.round) {
		t.Fatalf("%s", w.Body)
	}
}

func TestSendTextFormat(t *testing.T) {
	got := SendText(store.Send{Round: 4, Counts: store.Counts{Accept: 2, Edit: 1, Reject: 3, Applied: 1, Undone: 1, Redone: 1},
		Notes: []store.Note{{Row: "/w/a/CLAUDE.md:3 · negative-rule", Note: "shorter"}}})
	want := "The user sent their decisions for sift round 4: 2 accepted, 1 edited, 3 rejected; 1 certain fix(es) to apply, 1 undone, 1 redone.\n" +
		"Notes:\n- /w/a/CLAUDE.md:3 · negative-rule: shorter\n" +
		"Next: run sift_apply (or `sift apply`): a branch per repo with the accepted and edited rows and the certain fixes. Then run `sift reconcile` and review each branch before it merges."
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// Each pending send goes to its own owner, as recorded when it was sent,
// not to the latest round's owner: two rounds, two owners present, two
// pending sends, and each owner gets only its own.
func TestEachSendGoesToItsOwnOwner(t *testing.T) {
	f := newFixture(t)
	f.srv.waitUnit = 20 * time.Millisecond
	f.presence("a", "pi · a")
	f.presence("b", "pi · b")
	f.do("POST", "/api/agent/review", `{"session":"a"}`)
	f.sendNow()
	first := f.round
	f.record()
	f.do("POST", "/api/agent/review", `{"session":"b"}`)
	f.sendNow()
	var got struct {
		Round int64 `json:"round"`
	}
	w := f.wait("b")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Round != f.round {
		t.Fatalf("b got round %d (%d), want its own %d", got.Round, w.Code, f.round)
	}
	if w := f.wait("b"); w.Code != 204 {
		t.Fatalf("b took a's send too: %d %s", w.Code, w.Body)
	}
	w = f.wait("a")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Round != first {
		t.Fatalf("a got round %d (%d), want %d", got.Round, w.Code, first)
	}
}
