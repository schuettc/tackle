package serve

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/deliver"
)

// session reads one session from GET /api/sessions, the list the page gets.
func (r *rig) sessionView(t *testing.T, id string) (deliver.Session, bool) {
	t.Helper()
	var sv SessionsView
	if c := r.do(t, "GET", "/api/sessions", nil, &sv); c != http.StatusOK {
		t.Fatalf("GET /api/sessions: %d", c)
	}
	for _, s := range sv.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return deliver.Session{}, false
}

// pi-casebook reports a session's name and parent on
// POST /api/agent/session-info; the page's session list carries them, and
// whether each session is a worker and eligible.
func TestSessionInfoNamesASessionAndMarksWorkers(t *testing.T) {
	r := newRig(t)
	head, _ := r.s.Bus.Head(ctx)
	if c := r.do(t, "POST", "/api/agent/presence", map[string]any{"id": "parent", "harness": "pi", "label": "pi · lm", "cwd": "/lm", "pid": 92602}, nil); c != http.StatusOK {
		t.Fatalf("presence: %d", c)
	}
	if c := r.do(t, "POST", "/api/agent/session-info", map[string]any{"id": "parent", "name": "luminary-meridian/site", "harness": "pi", "cwd": "/lm", "pid": 92602}, nil); c != http.StatusOK {
		t.Fatalf("session-info: %d", c)
	}
	if c := r.do(t, "POST", "/api/agent/session-info", map[string]any{"id": "w", "name": "worker#40c0f7e1", "harness": "pi", "cwd": "/lm", "pid": 92602, "parent": "parent"}, nil); c != http.StatusOK {
		t.Fatalf("session-info (worker): %d", c)
	}
	p, ok := r.sessionView(t, "parent")
	if !ok || p.Name != "luminary-meridian/site" || p.Worker || !p.Eligible {
		t.Fatalf("parent = %+v", p)
	}
	w, ok := r.sessionView(t, "w")
	if !ok || w.Name != "worker#40c0f7e1" || !w.Worker || w.Eligible {
		t.Fatalf("worker = %+v", w)
	}
	// A rename reaches the page as a sessions event.
	if c := r.do(t, "POST", "/api/agent/session-info", map[string]any{"id": "parent", "name": "luminary-meridian/owner", "harness": "pi", "cwd": "/lm", "pid": 92602}, nil); c != http.StatusOK {
		t.Fatalf("rename: %d", c)
	}
	if p, _ := r.sessionView(t, "parent"); p.Name != "luminary-meridian/owner" {
		t.Fatalf("after the rename: %+v", p)
	}
	evs, _, _ := r.s.Bus.Since(ctx, head, 100)
	n := 0
	for _, e := range evs {
		if e.Type == "sessions" {
			n++
		}
	}
	if n < 3 {
		t.Fatalf("%d sessions events for presence + 3 session-infos, want ≥ 3", n)
	}
	if c := r.do(t, "POST", "/api/agent/session-info", map[string]any{"name": "x"}, nil); c != http.StatusBadRequest {
		t.Fatalf("session-info without an id: %d", c)
	}
}

// casebook_open from a session opens the page attached to that session: the
// URL carries ?session=<id> (kept through the token exchange and a reload),
// before any route fragment. A session serve doesn't know is refused, so the
// channel registers presence and retries; no session opens a page that
// belongs to no one.
func TestOpenAttachesThePageToTheCallingSession(t *testing.T) {
	r := apptest.New(t)
	var opened []string
	adv, _, stop := runOnce(t, r, &opened)
	defer stop()
	if c := callBody(t, adv, "/api/agent/presence", `{"id":"s1","harness":"pi","cwd":"/w","pid":7}`); c != http.StatusOK {
		t.Fatalf("presence: %d", c)
	}
	for _, c := range []struct {
		body string
		code int
		url  string // suffix of the opened URL; "" when nothing opens
	}{
		{`{"session":"s1"}`, 200, "?t=" + adv.Token + "&session=s1"},
		{`{"session":"s1","key":"pr:schuettc/hail#3"}`, 200, "?t=" + adv.Token + "&session=s1#/item/pr:schuettc%2Fhail%233"},
		{`{"session":"s1","view":"proposed"}`, 200, "&session=s1#/attention/proposed"},
		{`{}`, 200, "?t=" + adv.Token},
		{`{"session":"nobody"}`, 404, ""},
	} {
		before := len(opened)
		if got := callBody(t, adv, "/api/agent/open", c.body); got != c.code {
			t.Fatalf("%s: %d, want %d", c.body, got, c.code)
		}
		if c.url == "" {
			if len(opened) != before {
				t.Fatalf("%s opened %v", c.body, opened[before:])
			}
			continue
		}
		if len(opened) != before+1 || !strings.HasSuffix(opened[before], c.url) {
			t.Fatalf("%s opened %v, want a URL ending %q", c.body, opened[before:], c.url)
		}
	}
	// An id that needs escaping survives as one query value.
	if c := callBody(t, adv, "/api/agent/presence", `{"id":"a b&c","harness":"pi","cwd":"/w","pid":7}`); c != http.StatusOK {
		t.Fatalf("presence: %d", c)
	}
	before := len(opened)
	if c := callBody(t, adv, "/api/agent/open", `{"session":"a b&c"}`); c != http.StatusOK || len(opened) != before+1 {
		t.Fatalf("open: %d %v", c, opened[before:])
	}
	u, err := url.Parse(opened[before])
	if err != nil || u.Query().Get("session") != "a b&c" || u.Query().Get("t") != adv.Token {
		t.Fatalf("opened %q (%v)", opened[before], err)
	}
}

// serve prunes long-gone empty sessions when it starts, so the table stops
// growing with every session that ever announced itself.
func TestServePrunesLongGoneEmptySessionsAtStart(t *testing.T) {
	ar := apptest.New(t)
	path := filepath.Join(t.TempDir(), "casebook.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	q := deliver.New(d)
	q.Now = func() time.Time { return time.Now().Add(-SessionRetention - time.Hour) }
	for _, id := range []string{"gone-empty", "gone-thread"} {
		if err := q.Touch(context.Background(), deliver.Session{ID: id, Harness: "pi", CWD: "/w"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.NewThread(ctx, "gone-thread", "triage"); err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, ar.App, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.pushWG.Wait)
	ss, err := s.Queue.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range ss {
		ids = append(ids, x.ID)
	}
	if len(ids) != 1 || ids[0] != "gone-thread" {
		t.Fatalf("sessions after start: %v, want [gone-thread]", ids)
	}
}
