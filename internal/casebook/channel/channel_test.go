package channel

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/serve"
)

type mcp struct {
	t  *testing.T
	w  io.WriteCloser
	sc *bufio.Scanner
	id int
}

// start runs the channel over pipes and performs the MCP handshake.
func start(t *testing.T, ch *Channel) *mcp {
	t.Helper()
	clientOut, serverIn := io.Pipe()
	serverOut, clientIn := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = ch.Run(ctx, clientOut, clientIn) }()
	t.Cleanup(func() { cancel(); serverIn.Close() })
	sc := bufio.NewScanner(serverOut)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	m := &mcp{t: t, w: serverIn, sc: sc}
	res := m.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if !strings.Contains(string(res["instructions"].(string)), "settle EVERY message with casebook_reply") {
		t.Fatalf("instructions %v", res["instructions"])
	}
	return m
}

func (m *mcp) next(timeout time.Duration) map[string]any {
	m.t.Helper()
	done := make(chan bool, 1)
	go func() { done <- m.sc.Scan() }()
	select {
	case ok := <-done:
		if !ok {
			m.t.Fatal("channel closed its output")
		}
	case <-time.After(timeout):
		m.t.Fatal("nothing from the channel")
	}
	var v map[string]any
	if err := json.Unmarshal(m.sc.Bytes(), &v); err != nil {
		m.t.Fatalf("%q: %v", m.sc.Text(), err)
	}
	return v
}

// call sends a request and returns its result, skipping notifications.
func (m *mcp) call(method string, params any) map[string]any {
	m.t.Helper()
	m.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.id, "method": method, "params": params})
	io.WriteString(m.w, string(b)+"\n")
	for {
		v := m.next(10 * time.Second)
		if v["method"] != nil {
			continue
		}
		res, _ := v["result"].(map[string]any)
		return res
	}
}

func (m *mcp) tool(name string, args any) (string, bool) {
	m.t.Helper()
	res := m.call("tools/call", map[string]any{"name": name, "arguments": args})
	content := res["content"].([]any)[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return content, isErr
}

// runServe starts casebook serve for the rig and returns a client for it.
func runServe(t *testing.T, r *apptest.Rig) *Client {
	t.Helper()
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		errc <- serve.Run(ctx, r.App, serve.Options{Version: "test", Started: func(s *serve.Server, _ serve.Advert) { s.Wait = 3 * time.Second; close(started) }})
	}()
	select {
	case <-started:
	case err := <-errc:
		t.Fatalf("serve: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not start")
	}
	t.Cleanup(func() { cancel(); <-errc })
	return NewClient()
}

func TestChannelEndToEnd(t *testing.T) {
	r := apptest.New(t)
	c := runServe(t, r)
	ch := New(Identity{Session: "s1", Harness: "pi", Label: "pi · w", CWD: "/w"}, c, "test")
	ch.Retry, ch.Poll = 100*time.Millisecond, 2*time.Second
	m := start(t, ch)

	// Presence arrives; Court opens a thread and sends a message from the page.
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var ss struct {
			Sessions []struct{ ID string } `json:"sessions"`
		}
		c.Do(ctx, http.MethodGet, "/api/sessions", nil, &ss)
		if len(ss.Sessions) == 1 && ss.Sessions[0].ID == "s1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no presence")
		}
		time.Sleep(50 * time.Millisecond)
	}
	var th struct {
		ID int64 `json:"id"`
	}
	c.Do(ctx, http.MethodPost, "/api/threads", map[string]any{"session": "s1", "name": "triage"}, &th)
	var msg struct {
		ID int64 `json:"id"`
	}
	c.Do(ctx, http.MethodPost, "/api/messages", map[string]any{"thread": th.ID, "body": "please look at pr:schuettc/hail#3"}, &msg)

	ev := m.next(10 * time.Second)
	if ev["method"] != "notifications/claude/channel" {
		t.Fatalf("event %v", ev)
	}
	params := ev["params"].(map[string]any)
	meta := params["meta"].(map[string]any)
	if !strings.Contains(params["content"].(string), "please look at pr:schuettc/hail#3") || meta["source"] != "casebook" || meta["messages"] == "" {
		t.Fatalf("notification %v", params)
	}

	if out, isErr := m.tool("casebook_attention", map[string]any{"view": "waiting"}); isErr || !strings.Contains(out, "pr:schuettc/hail#3") {
		t.Fatalf("attention %q %v", out, isErr)
	}
	if out, isErr := m.tool("casebook_propose", map[string]any{"keys": []string{"pr:schuettc/hail#3"}, "disposition": "keep", "note": "active work"}); isErr || !strings.Contains(out, `"proposed": 1`) {
		t.Fatalf("propose %q %v", out, isErr)
	}
	if _, isErr := m.tool("casebook_progress", map[string]any{"text": "reading the PR", "n": 1, "total": 1}); isErr {
		t.Fatal("progress")
	}
	if out, isErr := m.tool("casebook_reply", map[string]any{"ids": []int64{msg.ID}, "state": "answered", "text": "proposed keep"}); isErr || !strings.Contains(out, "settled") {
		t.Fatalf("reply %q %v", out, isErr)
	}
	if out, isErr := m.tool("casebook_status", map[string]any{}); isErr || !strings.Contains(out, "counts") || !strings.Contains(out, "page_open") {
		t.Fatalf("status %q %v", out, isErr)
	}
	if out, isErr := m.tool("casebook_open", map[string]any{}); !isErr || !strings.Contains(out, "can't open a browser") {
		t.Fatalf("open without a browser %q %v", out, isErr)
	}
	if out, isErr := m.tool("casebook_show", map[string]any{"key": "pr:schuettc/hail#3"}); isErr || !strings.Contains(out, "proposal") {
		t.Fatalf("show %q %v", out, isErr)
	}
}

// serveCtl runs casebook serve in-process and can stop it.
type serveCtl struct {
	stop func()
}

func startServe(t *testing.T, r *apptest.Rig) *serveCtl {
	t.Helper()
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		errc <- serve.Run(ctx, r.App, serve.Options{Version: "test", Started: func(s *serve.Server, _ serve.Advert) { s.Wait = 2 * time.Second; close(started) }})
	}()
	select {
	case <-started:
	case err := <-errc:
		t.Fatalf("serve: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not start")
	}
	c := &serveCtl{}
	var once sync.Once
	c.stop = func() { once.Do(func() { cancel(); <-errc }) }
	t.Cleanup(c.stop)
	return c
}

func TestToolCallStartsServeTheLoopDoesnt(t *testing.T) {
	r := apptest.New(t)
	var starts atomic.Int32
	c := NewClient()
	c.Start = func() (serve.Advert, error) {
		starts.Add(1)
		startServe(t, r)
		return serve.Running()
	}
	ch := New(Identity{Session: "s1", Harness: "pi"}, c, "test")
	ch.Retry, ch.Poll = 50*time.Millisecond, time.Second
	m := start(t, ch)
	time.Sleep(300 * time.Millisecond)
	if n := starts.Load(); n != 0 {
		t.Fatalf("the wake loop started serve %d time(s)", n)
	}
	if out, isErr := m.tool("casebook_history", map[string]any{"key": "repo:schuettc/hail"}); isErr || !strings.Contains(out, "decisions") || !strings.Contains(out, "events") {
		t.Fatalf("history %q %v", out, isErr)
	}
	if out, isErr := m.tool("casebook_attention", map[string]any{"view": "new"}); isErr || !strings.Contains(out, "repo:schuettc/hail") {
		t.Fatalf("attention %q %v", out, isErr)
	}
	if out, isErr := m.tool("casebook_show", map[string]any{"key": "repo:schuettc/hail"}); isErr || !strings.Contains(out, "repo:schuettc/hail") {
		t.Fatalf("show %q %v", out, isErr)
	}
	if n := starts.Load(); n != 1 {
		t.Fatalf("serve started %d time(s), want 1", n)
	}
	// A client that doesn't start serve (the Stop hook's) just says so.
	ch2 := New(Identity{Session: "s2", Harness: "pi"}, &Client{HTTP: c.HTTP, Find: func() (serve.Advert, error) { return serve.Advert{}, serve.ErrNotRunning }}, "test")
	ch2.Retry = time.Hour
	m2 := start(t, ch2)
	if out, isErr := m2.tool("casebook_propose", map[string]any{"keys": []string{"repo:schuettc/hail"}, "disposition": "keep"}); !isErr || !strings.Contains(out, "casebook serve isn't running") {
		t.Fatalf("propose without serve %q %v", out, isErr)
	}
}

func TestServeRestartIsAnnounced(t *testing.T) {
	r := apptest.New(t)
	first := startServe(t, r)
	ch := New(Identity{Session: "s1", Harness: "pi"}, NewClient(), "test")
	ch.Retry, ch.Poll = 50*time.Millisecond, time.Second
	m := start(t, ch)
	time.Sleep(300 * time.Millisecond) // the loop reaches the first serve
	first.stop()
	time.Sleep(10 * time.Millisecond) // StartedAt must differ
	startServe(t, r)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ev := m.next(10 * time.Second)
		if ev["method"] != "notifications/claude/channel" {
			continue
		}
		params := ev["params"].(map[string]any)
		if params["meta"].(map[string]any)["event"] != "restarted" || !strings.Contains(params["content"].(string), "casebook serve restarted") {
			t.Fatalf("event %v", params)
		}
		return
	}
	t.Fatal("no restart event")
}

func TestNoSessionIsReadOnly(t *testing.T) {
	apptest.New(t) // hermetic CASEBOOK_HOME
	ch := New(Identity{}, NewClient(), "test")
	m := start(t, ch)
	if out, isErr := m.tool("casebook_reply", map[string]any{"ids": []int64{1}, "state": "answered"}); !isErr || !strings.Contains(out, "no session id") {
		t.Fatalf("reply without session %q %v", out, isErr)
	}
	res := m.call("tools/list", map[string]any{})
	if n := len(res["tools"].([]any)); n != 9 {
		t.Fatalf("%d tools", n)
	}
}

func TestFromEnvHarness(t *testing.T) {
	t.Setenv("AGENT_SESSION_CHILD", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("AGENT_SESSION_ID", "pi-1")
	if id := FromEnv(); id.Session != "pi-1" || id.Harness != "pi" {
		t.Fatalf("pi %+v", id)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "cc-1")
	if id := FromEnv(); id.Session != "cc-1" || id.Harness != "claude" {
		t.Fatalf("claude %+v", id)
	}
	t.Setenv("AGENT_SESSION_CHILD", "1")
	if id := FromEnv(); id.Session != "pi-1" || id.Harness != "pi" {
		t.Fatalf("bridge child %+v", id)
	}
}
