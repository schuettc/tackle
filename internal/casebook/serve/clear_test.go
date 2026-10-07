package serve

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
)

type clearResult struct {
	Cleared     bool `json:"cleared"`
	PushedLater bool `json:"pushed_later"`
}

func (r *rig) decideOne(t *testing.T, key, disposition string) item.Decision {
	t.Helper()
	var res DecideResult
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{key}, "disposition": disposition}, &res); c != 200 || res.Decided != 1 {
		t.Fatalf("decide %s %s: %d %+v", key, disposition, c, res)
	}
	k, _ := item.ParseKey(key)
	d, err := r.App.Repo.ReadDecision(k)
	if err != nil || d == nil {
		t.Fatalf("read decision: %v %v", d, err)
	}
	return *d
}

func TestClearRemovesTheDecision(t *testing.T) {
	r := newRig(t)
	const key = "pr:schuettc/hail#3"
	d := r.decideOne(t, key, "keep")
	var out clearResult
	if c := r.do(t, "POST", "/api/decisions/clear", map[string]any{"key": key, "decided_at": d.DecidedAt}, &out); c != 200 {
		t.Fatalf("clear %d", c)
	}
	if !out.Cleared || !out.PushedLater {
		t.Fatalf("clear result %+v", out)
	}
	k, _ := item.ParseKey(key)
	if got, err := r.App.Repo.ReadDecision(k); err != nil || got != nil {
		t.Fatalf("decision after clear: %+v %v", got, err)
	}
	it, ok := r.s.Index.Item(key)
	if !ok || it.Decision != nil {
		t.Fatalf("index item after clear: %v %+v", ok, it.Decision)
	}
	log, err := git(t, r.App.Repo.Dir, "log", "-1", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(log), "clear "+key) {
		t.Fatalf("last commit %q, want it to name the clear", log)
	}
	// Clearing again is a no-op, not an error.
	out = clearResult{}
	if c := r.do(t, "POST", "/api/decisions/clear", map[string]any{"key": key, "decided_at": d.DecidedAt}, &out); c != 200 || out.Cleared {
		t.Fatalf("second clear %d %+v", c, out)
	}
}

// TestClearRefusesAStaleDecision: undo must not overwrite a decision made
// after the one the page is undoing.
func TestClearRefusesAStaleDecision(t *testing.T) {
	r := newRig(t)
	const key = "pr:schuettc/hail#3"
	first := r.decideOne(t, key, "keep")
	r.Now = r.Now.Add(time.Minute)
	second := r.decideOne(t, key, "ignore")
	if first.DecidedAt.Equal(second.DecidedAt) {
		t.Fatal("the two decisions share decided_at")
	}
	var e struct {
		Error string `json:"error"`
	}
	if c := r.do(t, "POST", "/api/decisions/clear", map[string]any{"key": key, "decided_at": first.DecidedAt}, &e); c != http.StatusConflict {
		t.Fatalf("stale clear %d, want 409", c)
	}
	if e.Error == "" {
		t.Error("409 without a message")
	}
	k, _ := item.ParseKey(key)
	got, err := r.App.Repo.ReadDecision(k)
	if err != nil || got == nil || got.Disposition != item.Ignore || !got.DecidedAt.Equal(second.DecidedAt) {
		t.Fatalf("decision after refused clear: %+v %v", got, err)
	}
}
