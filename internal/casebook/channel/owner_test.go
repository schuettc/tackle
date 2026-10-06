package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/serve"
)

// recorder is a fake serve that keeps every agent request's JSON body.
type recorder struct {
	mu     sync.Mutex
	bodies map[string][]map[string]any
}

func fakeServe(t *testing.T, status func(path string, n int) int) (*recorder, *Client) {
	t.Helper()
	rec := &recorder{bodies: map[string][]map[string]any{}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies[r.URL.Path] = append(rec.bodies[r.URL.Path], body)
		n := len(rec.bodies[r.URL.Path])
		rec.mu.Unlock()
		code := http.StatusOK
		if status != nil {
			code = status(r.URL.Path, n)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if code == http.StatusNotFound {
			_, _ = w.Write([]byte(`{"error":"unknown session","code":"unknown_session"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	return rec, &Client{HTTP: ts.Client(), Find: func() (serve.Advert, error) { return serve.Advert{Base: ts.URL, Token: "tok"}, nil }}
}

func (r *recorder) get(path string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.bodies[path]...)
}

// The channel announces the HARNESS process's pid (its parent: pi or
// Claude Code spawned it), not its own: pi-subagents runs workers inside
// the parent's pi process, so a parent live in the same harness pid is what
// tells serve a session is a worker. Each channel's own pid is always different.
func TestPresenceReportsTheHarnessProcess(t *testing.T) {
	rec, c := fakeServe(t, nil)
	ch := New(Identity{Session: "s1", Harness: "pi", Label: "pi · w", CWD: "/w"}, c, "test")
	if err := ch.presence(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	got := rec.get("/api/agent/presence")
	if len(got) != 1 {
		t.Fatalf("presence bodies %v", got)
	}
	if pid, _ := got[0]["pid"].(float64); int(pid) != os.Getppid() {
		t.Fatalf("presence pid %v, want the harness's %d (not the channel's %d)", got[0]["pid"], os.Getppid(), os.Getpid())
	}
}

// casebook_open sends the channel's session, so the page opens attached to
// it. When serve doesn't know the session yet it registers presence and
// retries, like every session-bound call. A channel with no session opens
// a page that belongs to no one.
func TestOpenSendsTheChannelsSession(t *testing.T) {
	rec, c := fakeServe(t, func(path string, n int) int {
		if path == "/api/agent/open" && n == 1 {
			return http.StatusNotFound // serve doesn't know s1 yet
		}
		return http.StatusOK
	})
	ch := New(Identity{Session: "s1", Harness: "pi", CWD: "/w"}, c, "test")
	if _, err := ch.Call(context.Background(), "casebook_open", json.RawMessage(`{"view":"proposed"}`)); err != nil {
		t.Fatal(err)
	}
	opens := rec.get("/api/agent/open")
	if len(opens) != 2 || opens[1]["session"] != "s1" || opens[1]["view"] != "proposed" {
		t.Fatalf("open bodies %v", opens)
	}
	if len(rec.get("/api/agent/presence")) != 1 {
		t.Fatalf("no presence before the retry: %v", rec.get("/api/agent/presence"))
	}

	rec2, c2 := fakeServe(t, nil)
	none := New(Identity{Harness: "pi", CWD: "/w"}, c2, "test")
	if _, err := none.Call(context.Background(), "casebook_open", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if o := rec2.get("/api/agent/open"); len(o) != 1 || o[0]["session"] != nil && o[0]["session"] != "" {
		t.Fatalf("open without a session sent %v", o)
	}
}

// A new channel against a serve started before sessions could own a page:
// that serve refuses the "session" field (400 unknown field). casebook_open
// opens the page anyway, without the session (the page then asks Court to
// choose), and says a serve restart is needed to attach it.
func TestOpenAgainstAnOldServeRetriesWithoutTheSession(t *testing.T) {
	var mu sync.Mutex
	var opens []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/agent/open" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		mu.Lock()
		opens = append(opens, body)
		mu.Unlock()
		if _, ok := body["session"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad request body: json: unknown field \"session\""}`))
			return
		}
		_, _ = w.Write([]byte(`{"url":"http://127.0.0.1:1/?t=x"}`))
	}))
	t.Cleanup(ts.Close)
	c := &Client{HTTP: ts.Client(), Find: func() (serve.Advert, error) { return serve.Advert{Base: ts.URL, Token: "tok"}, nil }}
	ch := New(Identity{Session: "s1", Harness: "pi", CWD: "/w"}, c, "test")
	out, err := ch.Call(context.Background(), "casebook_open", json.RawMessage(`{"view":"proposed"}`))
	if err != nil {
		t.Fatalf("open against an old serve: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(opens) != 2 || opens[0]["session"] != "s1" || opens[1]["session"] != nil || opens[1]["view"] != "proposed" {
		t.Fatalf("open bodies %v", opens)
	}
	if !strings.Contains(out, "restart") || !strings.Contains(out, "casebook serve --stop") {
		t.Fatalf("the result doesn't say a serve restart is needed: %q", out)
	}
}
