package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/serve"
)

// watchedFind is serve.Running that remembers the StartedAt of every serve
// it found, so a test knows when a channel's loop has reached a new serve.
type watchedFind struct {
	mu   sync.Mutex
	seen map[time.Time]int
}

func (f *watchedFind) find() (serve.Advert, error) {
	adv, err := serve.Running()
	if err == nil {
		f.mu.Lock()
		if f.seen == nil {
			f.seen = map[time.Time]int{}
		}
		f.seen[adv.StartedAt.UTC()]++
		f.mu.Unlock()
	}
	return adv, err
}

// reached waits until the loop has found the serve started at started at
// least twice: the first find is the one that notices the restart (and
// would notify), the second comes after that iteration finished.
func (f *watchedFind) reached(t *testing.T, started time.Time) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := f.seen[started.UTC()]
		f.mu.Unlock()
		if n >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the channel never reached the serve started at %v", started)
}

// startServeAt is startServe that also returns the new serve's advert. It
// leaves serve's Wait alone: channels here are already polling for the
// advert, so they may reach serve before a Started hook could set it (a
// race), and their own Poll (1s) caps the long-poll anyway.
func startServeAt(t *testing.T, r *apptest.Rig) (*serveCtl, serve.Advert) {
	t.Helper()
	started := make(chan serve.Advert, 1)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		errc <- serve.Run(ctx, r.App, serve.Options{Version: "test", Started: func(_ *serve.Server, a serve.Advert) { started <- a }})
	}()
	var adv serve.Advert
	select {
	case adv = <-started:
	case err := <-errc:
		t.Fatalf("serve: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not start")
	}
	c := &serveCtl{}
	var once sync.Once
	c.stop = func() { once.Do(func() { cancel(); <-errc }) }
	t.Cleanup(c.stop)
	return c, adv
}

// noRestartEvent fails the test if the channel emits anything within the
// window: a restart that isn't the session's business must not wake it.
func noRestartEvent(t *testing.T, who string, m *mcp, within time.Duration) {
	t.Helper()
	if v := m.nextTimeout(within); v != nil {
		t.Fatalf("%s was woken: %v", who, v)
	}
}

// A serve restart is the business only of the sessions it interrupted: A had
// a delivery in flight and is told (what, not the page's tab); B was idle
// and hears nothing. A second restart with nothing in flight tells no one.
func TestServeRestartTellsOnlyTheInterruptedSession(t *testing.T) {
	r := apptest.New(t)
	first, _ := startServeAt(t, r)
	findA, findB := &watchedFind{}, &watchedFind{}
	chA := New(Identity{Session: "a", Harness: "pi", Label: "pi · a", CWD: "/a"}, &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, Find: findA.find}, "test")
	chB := New(Identity{Session: "b", Harness: "pi", Label: "pi · b", CWD: "/b"}, &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, Find: findB.find}, "test")
	for _, ch := range []*Channel{chA, chB} {
		ch.Retry, ch.Poll = 50*time.Millisecond, time.Second
	}
	mA, mB := start(t, chA), start(t, chB)

	// Both sessions are present; Court sends A a message, which goes out
	// (A's delivery is in flight: A never settles it).
	c := NewClient()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var ss struct {
			Sessions []struct{ ID string } `json:"sessions"`
		}
		_, _ = c.Do(ctx, http.MethodGet, "/api/sessions", nil, &ss)
		if len(ss.Sessions) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no presence: %+v", ss)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var th struct {
		ID int64 `json:"id"`
	}
	_, _ = c.Do(ctx, http.MethodPost, "/api/threads", map[string]any{"session": "a", "name": "triage"}, &th)
	var msg struct {
		ID int64 `json:"id"`
	}
	_, _ = c.Do(ctx, http.MethodPost, "/api/messages", map[string]any{"thread": th.ID, "body": "look at pr:schuettc/hail#3"}, &msg)
	if ev := mA.next(10 * time.Second); !strings.Contains(ev["params"].(map[string]any)["content"].(string), "look at pr:schuettc/hail#3") {
		t.Fatalf("A's delivery %v", ev)
	}
	noRestartEvent(t, "B (before any restart)", mB, 200*time.Millisecond)

	// Restart 1: A's delivery is interrupted; B had nothing in flight.
	first.stop()
	time.Sleep(10 * time.Millisecond) // StartedAt must differ
	second, adv2 := startServeAt(t, r)
	ev := mA.next(10 * time.Second)
	params := ev["params"].(map[string]any)
	content := params["content"].(string)
	if params["meta"].(map[string]any)["event"] != "restarted" || !strings.Contains(content, "casebook serve restarted") {
		t.Fatalf("A's restart event %v", params)
	}
	if !strings.Contains(content, "1 message") || !strings.Contains(content, "interrupted") {
		t.Fatalf("A's notice doesn't say what was interrupted: %q", content)
	}
	if strings.Contains(content, "tab") {
		t.Fatalf("A's notice mentions the page's tab: %q", content)
	}
	findB.reached(t, adv2.StartedAt)
	findA.reached(t, adv2.StartedAt)
	noRestartEvent(t, "B (restart 1)", mB, 500*time.Millisecond)
	noRestartEvent(t, "A (a second notice for restart 1)", mA, 200*time.Millisecond)

	// Restart 2: nothing in flight anywhere (A's delivery is already
	// interrupted, not in flight): no one is told.
	second.stop()
	time.Sleep(10 * time.Millisecond)
	_, adv3 := startServeAt(t, r)
	findA.reached(t, adv3.StartedAt)
	findB.reached(t, adv3.StartedAt)
	noRestartEvent(t, "A (restart 2)", mA, 500*time.Millisecond)
	noRestartEvent(t, "B (restart 2)", mB, 200*time.Millisecond)
}

// fakeRestartServe is a serve whose StartedAt the test moves (a restart),
// that keeps the long-poll quiet, and answers GET /api/agent/interrupted
// with interrupted (nil: as an older serve does, 404 page not found).
type fakeRestartServe struct {
	started  atomic.Int64 // StartedAt, unix ns
	finds    atomic.Int32 // finds since the last restart
	reopened bool
	asked    atomic.Int32
}

func newFakeRestartServe(t *testing.T, reopened bool, interrupted func(w http.ResponseWriter, r *http.Request)) (*fakeRestartServe, *Client) {
	t.Helper()
	f := &fakeRestartServe{reopened: reopened}
	f.started.Store(time.Now().UnixNano())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agent/presence", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
	mux.HandleFunc("GET /api/agent/wait", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(50 * time.Millisecond):
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if interrupted != nil {
		mux.HandleFunc("GET /api/agent/interrupted", func(w http.ResponseWriter, r *http.Request) {
			f.asked.Add(1)
			interrupted(w, r)
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := &Client{HTTP: ts.Client(), Find: func() (serve.Advert, error) {
		f.finds.Add(1)
		return serve.Advert{Base: ts.URL, Token: "tok", StartedAt: time.Unix(0, f.started.Load()).UTC(), Reopened: f.reopened}, nil
	}}
	return f, c
}

// restart moves StartedAt and waits until the loop has found the new serve
// twice (the iteration that noticed it has finished).
func (f *fakeRestartServe) restart(t *testing.T) time.Time {
	t.Helper()
	at := time.Now().Add(time.Second)
	f.started.Store(at.UnixNano())
	f.finds.Store(0)
	deadline := time.Now().Add(5 * time.Second)
	for f.finds.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the loop never found the restarted serve")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return at.UTC()
}

func runFakeChannel(t *testing.T, c *Client) *mcp {
	t.Helper()
	ch := New(Identity{Session: "s1", Harness: "pi"}, c, "test")
	ch.Retry, ch.Poll = 20*time.Millisecond, time.Second
	m := start(t, ch)
	time.Sleep(100 * time.Millisecond) // the loop reaches the first serve
	return m
}

// An older serve can't say whose delivery a restart interrupted (no
// GET /api/agent/interrupted): the channel says nothing rather than waking
// a session that may have had nothing in flight. So does a serve that
// fails to answer.
func TestRestartIsSilentWhenServeCantSay(t *testing.T) {
	for name, handler := range map[string]func(w http.ResponseWriter, r *http.Request){
		"old serve (404)": nil,
		"error (500)": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
		},
		"unknown session": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unknown session","code":"unknown_session"}`))
		},
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
	} {
		t.Run(name, func(t *testing.T) {
			apptest.New(t)
			f, c := newFakeRestartServe(t, true, handler)
			m := runFakeChannel(t, c)
			f.restart(t)
			noRestartEvent(t, "the session", m, 300*time.Millisecond)
		})
	}
}

// The notice names what was interrupted (the messages, by count and id) and
// says nothing about Court's browser, even when the page reopened.
func TestRestartNoticeNamesTheInterruptedMessagesNotTheTab(t *testing.T) {
	apptest.New(t)
	var at atomic.Int64
	f, c := newFakeRestartServe(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("session") != "s1" {
			http.Error(w, "wrong session", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"started_at": time.Unix(0, at.Load()).UTC(), "deliveries": []int64{4}, "messages": []int64{7, 8}})
	})
	m := runFakeChannel(t, c)
	at.Store(time.Now().Add(time.Second).UnixNano())
	f.started.Store(at.Load())
	ev := m.next(5 * time.Second)
	params := ev["params"].(map[string]any)
	content := params["content"].(string)
	if params["meta"].(map[string]any)["event"] != "restarted" {
		t.Fatalf("event %v", params)
	}
	for _, want := range []string{"casebook serve restarted", "2 messages", "7, 8", "interrupted", "nothing was lost"} {
		if !strings.Contains(content, want) {
			t.Fatalf("notice lacks %q: %q", want, content)
		}
	}
	if strings.Contains(content, "tab") || strings.Contains(content, "page was open") {
		t.Fatalf("notice mentions the page's tab: %q", content)
	}
	if f.asked.Load() != 1 {
		t.Fatalf("asked serve %d times, want 1", f.asked.Load())
	}
	noRestartEvent(t, "the session (twice)", m, 200*time.Millisecond)
}

// An answer about a different serve than the restart the channel noticed
// (serve restarted again in between) is not trusted: silence. The next
// iteration notices the newer serve and asks it.
func TestRestartAnswerForAnotherServeIsSilent(t *testing.T) {
	apptest.New(t)
	f, c := newFakeRestartServe(t, false, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"started_at":"2020-01-01T00:00:00Z","deliveries":[4],"messages":[7]}`))
	})
	m := runFakeChannel(t, c)
	f.restart(t)
	noRestartEvent(t, "the session", m, 300*time.Millisecond)
	if n := f.asked.Load(); n != 1 {
		t.Fatalf("asked serve %d times for one restart, want 1", n)
	}
}

// A serve that interrupted nothing of this session's says so (empty lists):
// silence.
func TestRestartWithNothingInterruptedIsSilent(t *testing.T) {
	apptest.New(t)
	var f *fakeRestartServe
	var c *Client
	f, c = newFakeRestartServe(t, false, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"started_at": time.Unix(0, f.started.Load()).UTC(), "deliveries": []int64{}, "messages": []int64{}})
	})
	m := runFakeChannel(t, c)
	f.restart(t)
	noRestartEvent(t, "the session", m, 300*time.Millisecond)
	if f.asked.Load() == 0 {
		t.Fatal("the channel never asked serve")
	}
}
