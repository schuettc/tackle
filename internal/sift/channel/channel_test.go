package channel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/serve"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
	"github.com/schuettc/tools-common/localweb"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t    *testing.T
	st   *store.Store
	logs *syncBuf
	repo string
}

// newEnv isolates sift's state, config and git, writes a fixture workspace
// (one published repo with a negative rule and a misplaced line) and its
// config, and runs a real serve that stops when the test ends.
func newEnv(t *testing.T) *env {
	t.Helper()
	st.Env(t)
	home := st.Home(t)
	t.Setenv("SIFT_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st.Write(t, home, ".codex/AGENTS.md", "# Global\n\n- In webapp, run the slow suite.\n")
	ws := t.TempDir()
	repo := st.Repo(t, filepath.Join(ws, "webapp"), map[string]string{"AGENTS.md": "# webapp\n\n- Never push to main.\n"})
	st.Publish(t, repo)
	c := config.Default()
	c.Profiles = []string{"codex"}
	c.Roots = []config.Root{{Path: ws}}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, logs: &syncBuf{}, repo: repo}
	s, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	e.st = s
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serve.Run(ctx, s, serve.Options{Ready: func(string) { close(ready) }, PollEvery: 50 * time.Millisecond})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not start")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("serve did not stop")
		}
	})
	return e
}

// api calls the running serve the way the page does.
func (e *env) api(method, path, body string) (int, string) {
	e.t.Helper()
	adv, err := serve.Running()
	if err != nil {
		e.t.Fatal(err)
	}
	req, _ := http.NewRequest(method, adv.Base+path, strings.NewReader(body))
	req.Header.Set(localweb.TokenHeader, adv.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// ---- an in-memory MCP client (copied from cull's channel tests) ---------------

type rpc struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type event struct {
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta"`
}

type client struct {
	t      *testing.T
	w      io.Writer
	mu     sync.Mutex
	next   int
	wait   map[int]chan rpc
	events chan event
	opened chan string
	ch     *Channel
}

func (e *env) connect(session string) *client {
	t := e.t
	t.Helper()
	cl := NewClient(nil)
	cl.Find = serve.Running
	ch := New(Identity{Session: session, Harness: "claude", Label: "claude · " + session, CWD: e.repo}, cl, "test")
	ch.Log = e.logs
	ch.LookPath = func(string) (string, error) { return "", errors.New("no gh in tests") }
	ch.Retry, ch.Presence, ch.Poll = 50*time.Millisecond, 200*time.Millisecond, time.Second
	c := &client{t: t, wait: map[int]chan rpc{}, events: make(chan event, 16), opened: make(chan string, 8), ch: ch}
	ch.Open = func(u string) error { c.opened <- u; return nil }
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c.w = inW
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ch.Run(ctx, inR, outW); close(done) }()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m rpc
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				t.Errorf("not an MCP frame on stdout: %q", sc.Text())
				continue
			}
			if m.ID == nil {
				if m.Method == "notifications/claude/channel" {
					var ev event
					_ = json.Unmarshal(m.Params, &ev)
					c.events <- ev
				}
				continue
			}
			c.mu.Lock()
			w := c.wait[*m.ID]
			c.mu.Unlock()
			if w != nil {
				w <- m
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		<-done
		_ = outW.Close()
	})
	c.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	return c
}

func (c *client) call(method string, params any) rpc {
	c.t.Helper()
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan rpc, 1)
	c.wait[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	select {
	case m := <-ch:
		return m
	case <-time.After(60 * time.Second):
		c.t.Fatalf("no answer to %s", method)
	}
	return rpc{}
}

func (c *client) tool(name string, args any) (string, bool) {
	c.t.Helper()
	m := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	var r struct {
		Content []struct{ Text string } `json:"content"`
		IsError bool                    `json:"isError"`
	}
	if err := json.Unmarshal(m.Result, &r); err != nil || len(r.Content) == 0 {
		c.t.Fatalf("%s: bad result %s (%v)", name, m.Result, err)
	}
	return r.Content[0].Text, r.IsError
}

func (c *client) toolJSON(name string, args any, out any) {
	c.t.Helper()
	text, isErr := c.tool(name, args)
	if isErr {
		c.t.Fatalf("%s failed: %s", name, text)
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		c.t.Fatalf("%s: not JSON: %v\n%s", name, err, text)
	}
}

// ---- tests -----------------------------------------------------------------

// The tool schemas are the contract with every agent: pin them.
func TestToolSchemasArePinned(t *testing.T) {
	want := map[string]string{
		"sift_check":  `{"type":"object","properties":{}}`,
		"sift_review": `{"type":"object","properties":{}}`,
		"sift_apply":  `{"type":"object","properties":{"round":{"type":"integer","description":"the round to apply (default: the latest)"},"dry_run":{"type":"boolean","description":"work out what would change and touch nothing"}}}`,
		"sift_status": `{"type":"object","properties":{}}`,
	}
	var names []string
	for _, tl := range Tools() {
		names = append(names, tl.Name)
		if string(tl.InputSchema) != want[tl.Name] {
			t.Errorf("%s schema %s", tl.Name, tl.InputSchema)
		}
		if !json.Valid(tl.InputSchema) || tl.Description == "" {
			t.Errorf("%s: invalid", tl.Name)
		}
	}
	if strings.Join(names, ",") != "sift_check,sift_review,sift_apply,sift_status" {
		t.Fatalf("tools %v", names)
	}
}

// The guidance is written as guidance: no "never" or "don't".
func TestInstructionsAreGuidance(t *testing.T) {
	low := strings.ToLower(Instructions)
	for _, w := range []string{"never", "don't", "do not", "must not"} {
		if strings.Contains(low, w) {
			t.Errorf("instructions say %q", w)
		}
	}
}

func TestInitializeListsTheTools(t *testing.T) {
	e := newEnv(t)
	c := e.connect("s1")
	m := c.call("tools/list", map[string]any{})
	var r struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(m.Result, &r)
	if len(r.Tools) != 4 {
		t.Fatalf("%s", m.Result)
	}
}

func TestCheckReviewSendApply(t *testing.T) {
	e := newEnv(t)
	c := e.connect("s1")
	var chk struct {
		Round   int64          `json:"round"`
		Rows    int            `json:"rows"`
		ToJudge int            `json:"to_judge"`
		Summary map[string]int `json:"summary"`
	}
	c.toolJSON("sift_check", map[string]any{}, &chk)
	if chk.Round == 0 || chk.Summary["negative-rule"] != 1 || chk.Summary["misplaced"] != 1 || chk.ToJudge != chk.Rows {
		t.Fatalf("check %+v", chk)
	}

	var rv struct {
		URL    string `json:"url"`
		Open   int    `json:"open"`
		Opened bool   `json:"opened"`
	}
	c.toolJSON("sift_review", map[string]any{}, &rv)
	if rv.Open != chk.Rows || !rv.Opened || !strings.Contains(rv.URL, "t=") {
		t.Fatalf("review %+v", rv)
	}
	select {
	case u := <-c.opened:
		if u != rv.URL {
			t.Errorf("opened %q", u)
		}
	case <-time.After(time.Second):
		t.Fatal("the page was not opened")
	}
	var stat map[string]any
	c.toolJSON("sift_status", map[string]any{}, &stat)
	if stat["owner"] != "claude · s1" || stat["open"] != float64(chk.Rows) {
		t.Fatalf("status %v", stat)
	}

	// The agent proposes, the user decides and sends; the event reaches
	// this session.
	_, rows, _ := e.st.LatestRound(context.Background())
	var neg string
	for _, r := range rows {
		if r.Check == "negative-rule" {
			neg = r.ID
		}
	}
	if _, err := e.st.AddRows(context.Background(), chk.Round, rowsIn(neg)); err != nil {
		t.Fatal(err)
	}
	if code, body := e.api("PUT", "/api/decisions", fmt.Sprintf(`{"round":%d,"decisions":[{"id":%q,"action":"accept","note":"yes"}]}`, chk.Round, neg)); code != 204 {
		t.Fatalf("decide %d %s", code, body)
	}
	if code, body := e.api("POST", "/api/send", fmt.Sprintf(`{"round":%d}`, chk.Round)); code != 200 || !strings.Contains(body, `"to":"claude · s1"`) {
		t.Fatalf("send %d %s", code, body)
	}
	select {
	case ev := <-c.events:
		if !strings.Contains(ev.Content, "1 accepted") || !strings.Contains(ev.Content, "yes") || ev.Meta["source"] != "sift" {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}

	var res struct {
		Repos []struct {
			State   string `json:"state"`
			Applied []any  `json:"applied"`
		} `json:"repos"`
	}
	c.toolJSON("sift_apply", map[string]any{"dry_run": true}, &res)
	if len(res.Repos) != 1 || res.Repos[0].State != "planned" || len(res.Repos[0].Applied) != 1 {
		t.Fatalf("apply %+v", res)
	}
}

func TestReviewWithNothingToReview(t *testing.T) {
	e := newEnv(t)
	c := e.connect("s1")
	text, isErr := c.tool("sift_review", map[string]any{})
	if isErr || !strings.Contains(text, "nothing to review") {
		t.Fatalf("%v %s", isErr, text)
	}
}

func TestCheckWithoutConfigSaysWhatToRun(t *testing.T) {
	e := newEnv(t)
	c := e.connect("s1")
	c.ch.LoadConfig = func() (config.Config, error) { return config.Config{}, config.ErrMissing }
	text, isErr := c.tool("sift_check", map[string]any{})
	if !isErr || !strings.Contains(text, "sift init") {
		t.Fatalf("%v %s", isErr, text)
	}
}

func rowsIn(id string) []row.Row {
	return []row.Row{{ID: id, Verdict: "rewrite", Text: "- Push to a branch and open a pull request.", Reason: "guidance"}}
}
