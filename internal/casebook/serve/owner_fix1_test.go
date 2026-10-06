package serve

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/rules"
)

// countMessages is how many messages (drafts included) a thread holds.
func (r *rig) countMessages(t *testing.T, th int64) int {
	t.Helper()
	var n int
	if err := r.s.DB.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE thread_id = ?", th).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A page attached to s1 names s1 on every send. After a move hands its open
// thread to s2 behind the page's back (a stuck delivery moved, or "move
// to…"), the stale page's next message, draft and batch are refused with a
// 409: they never reach s2, whose name the page's header doesn't show.
func TestStalePageCannotSendToTheSessionAThreadMovedTo(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s2")
	th := r.attach(t, "s1")

	// A draft batch, then a message that goes in flight to s1.
	var draft message
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "session": "s1", "body": "later", "batch": true}, &draft); c != http.StatusOK {
		t.Fatalf("draft: %d", c)
	}
	var msgs struct {
		Batch int64 `json:"batch"`
	}
	if c := r.do(t, "GET", "/api/messages?thread="+itoa(th), nil, &msgs); c != http.StatusOK || msgs.Batch == 0 {
		t.Fatalf("batch: %d %+v", c, msgs)
	}
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "session": "s1", "body": "do it"}, nil); c != http.StatusOK {
		t.Fatalf("send: %d", c)
	}
	var w waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); c != http.StatusOK || w.Delivery.ID == 0 {
		t.Fatalf("wait: %d %+v", c, w)
	}
	// The delivery is stuck; Court moves it to s2. The thread goes with it.
	if c := r.do(t, "POST", "/api/deliveries/move", map[string]any{"id": w.Delivery.ID, "session": "s2"}, nil); c != http.StatusOK {
		t.Fatalf("move: %d", c)
	}

	before := r.countMessages(t, th)
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	// The stale page (still attached to s1) sends into the moved thread.
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "session": "s1", "body": "meant for s1"}, &e); c != http.StatusConflict || e.Code != "thread_moved" {
		t.Fatalf("message from a stale page: %d %+v, want 409 thread_moved", c, e)
	}
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "session": "s1", "body": "draft for s1", "batch": true}, nil); c != http.StatusConflict {
		t.Fatalf("draft from a stale page: %d, want 409", c)
	}
	if c := r.do(t, "POST", "/api/batches/send", map[string]any{"batch": msgs.Batch, "session": "s1"}, nil); c != http.StatusConflict {
		t.Fatalf("batch send from a stale page: %d, want 409", c)
	}
	if got := r.countMessages(t, th); got != before {
		t.Fatalf("thread has %d messages, want %d: something was written", got, before)
	}
	var state string
	if err := r.s.DB.QueryRowContext(ctx, "SELECT state FROM messages WHERE id = ?", draft.ID).Scan(&state); err != nil || state != "draft" {
		t.Fatalf("the draft is %q (%v), want still draft", state, err)
	}

	// A page attached to s2 (whose thread it now is) sends.
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "session": "s2", "body": "for s2"}, nil); c != http.StatusOK {
		t.Fatalf("message from s2's page: %d", c)
	}
	if c := r.do(t, "POST", "/api/batches/send", map[string]any{"batch": msgs.Batch, "session": "s2"}, nil); c != http.StatusOK {
		t.Fatalf("batch send from s2's page: %d", c)
	}
}

// "move to…" (a whole session's threads) leaves the same stale page; its
// sends are refused the same way.
func TestStalePageCannotSendAfterMoveTo(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s2")
	th := r.attach(t, "s1")
	if c := r.do(t, "POST", "/api/sessions/move", map[string]any{"session": "s1", "target": "s2"}, nil); c != http.StatusOK {
		t.Fatalf("move: %d", c)
	}
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "session": "s1", "body": "meant for s1"}, nil); c != http.StatusConflict {
		t.Fatalf("message from a stale page: %d, want 409", c)
	}
	if got := r.countMessages(t, th); got != 0 {
		t.Fatalf("thread has %d messages, want 0", got)
	}
}

// A rule draft's created_by ("pi:<id>") lives in the casebook repo, not the
// database: serve tells Prune about it, so the session that drafted a rule
// is not pruned while the draft names it.
func TestServeSparesASessionThatDraftedARule(t *testing.T) {
	ar := apptest.New(t)
	path := filepath.Join(t.TempDir(), "casebook.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	q := deliver.New(d)
	q.Now = func() time.Time { return time.Now().Add(-SessionRetention - time.Hour) }
	for _, id := range []string{"gone-drafter", "gone-empty"} {
		if err := q.Touch(ctx, deliver.Session{ID: id, Harness: "pi", CWD: "/w"}); err != nil {
			t.Fatal(err)
		}
	}
	ru := activeRepoRule("drafted-by-agent", ar.Now)
	ru.Status = rules.StatusDraft
	ru.CreatedBy = "pi:gone-drafter"
	if err := ar.App.Repo.WriteRule(ctx, ru, "rule drafted-by-agent created by pi:gone-drafter"); err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, ar.App, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.pushWG.Wait)
	if _, err := s.Queue.Session(ctx, "gone-drafter"); err != nil {
		t.Fatalf("the session that drafted a rule was pruned: %v", err)
	}
	if _, err := s.Queue.Session(ctx, "gone-empty"); err == nil {
		t.Fatal("the empty session was kept (the prune didn't run)")
	}
}
