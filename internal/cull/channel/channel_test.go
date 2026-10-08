package channel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tackle/internal/cull/store"
	"github.com/schuettc/tools-common/localweb"
)

const fakeKey = "ts-fake-0000"

// ---- fake Jev ----------------------------------------------------------

// fakeJev answers every question without a network call. cut maps a substring
// of the request body to the probability of the "cut" option.
type fakeJev struct {
	mu     sync.Mutex
	cut    map[string]float64
	status int // when set, every request gets this status, echoing the key in the body
}

func (f *fakeJev) set(m map[string]float64) { f.mu.Lock(); f.cut = m; f.mu.Unlock() }

func (f *fakeJev) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		status, cut := f.status, f.cut
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"detail":"bad key `+fakeKey+`"}`)
			return
		}
		p := 0.05
		for marker, v := range cut {
			if strings.Contains(string(b), marker) {
				p = v
			}
		}
		var req struct {
			Questions map[string]struct {
				Type     string          `json:"type"`
				Criteria json.RawMessage `json:"criteria"`
			} `json:"questions"`
		}
		_ = json.Unmarshal(b, &req)
		ans := map[string]any{}
		for k, q := range req.Questions {
			switch q.Type {
			case "noul":
				ans[k] = map[string]any{"type": "noul", "noul": 0.1}
			case "score":
				ans[k] = map[string]any{"type": "score", "score": 1.0, "confidence": 0.8}
			case "choice":
				var opts map[string]string
				_ = json.Unmarshal(q.Criteria, &opts)
				probs := map[string]float64{}
				chosen := ""
				for o := range opts {
					if o == "cut" {
						probs[o] = p
					} else {
						probs[o] = (1 - p) / float64(len(opts)-1)
						if chosen == "" && o != "consolidate" && o != "review" {
							chosen = o
						}
					}
				}
				if p >= 0.6 {
					chosen = "cut"
				}
				ans[k] = map[string]any{"type": "choice", "choice": chosen, "probabilities": probs, "confidence": 0.85}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-test", "answers": ans})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CULL_TYPESAFE_URL", srv.URL)
}

// ---- fixture projects ----------------------------------------------------

const calc = "package calc\n\nfunc Add(a, b int) int { return a + b }\n"

const calcTest = `package calc

import (
	"strings"
	"testing"
)

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}

func TestUpper(t *testing.T) {
	if strings.ToUpper("a") != "A" {
		t.Fatal("bad")
	}
}

func TestMaybe(t *testing.T) {
	for _, n := range []int{4, 5} {
		if Add(n, 0) != n {
			t.Fatal("bad")
		}
	}
}

func TestUsesAdd(t *testing.T) {
	TestAdd(t)
}
`

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// project makes a temp Go module that allows egress.
func project(t *testing.T) string {
	t.Helper()
	return projectIn(t, t.TempDir())
}

// projectIn makes the fixture project at root.
func projectIn(t *testing.T, root string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	writeFile(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
	writeFile(t, root, ".cull.toml", "egress = true\ntest_command = \"go test ./...\"\n")
	writeFile(t, root, "calc.go", calc)
	writeFile(t, root, "calc_test.go", calcTest)
	return root
}

// ---- serve in-process ----------------------------------------------------

type env struct {
	t    *testing.T
	st   *store.Store
	jev  *fakeJev
	logs *syncBuf
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// newEnv isolates cull's state, starts a fake Jev and a real serve, and stops
// the serve when the test ends.
func newEnv(t *testing.T, withServe bool) *env {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", fakeKey)
	e := &env{t: t, jev: &fakeJev{}, logs: &syncBuf{}}
	e.jev.start(t)
	st, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	e.st = st
	t.Cleanup(func() { _ = st.Close() })
	if !withServe {
		return e
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serve.Run(ctx, st, serve.Options{Ready: func(string) { close(ready) }})
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

// ---- an in-memory MCP client ----------------------------------------------

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
	all    *syncBuf // every byte the channel wrote: results and events
	opened chan string
	ch     *Channel
}

type ident struct{ session, label, cwd string }

func (e *env) connect(id ident) *client {
	t := e.t
	t.Helper()
	cl := NewClient(nil)
	cl.Find = serve.Running
	ch := New(Identity{Session: id.session, Harness: "claude", Label: id.label, CWD: id.cwd}, cl, "test")
	ch.Log = e.logs
	ch.Retry, ch.Presence, ch.Poll = 50*time.Millisecond, 200*time.Millisecond, time.Second
	c := &client{t: t, wait: map[int]chan rpc{}, events: make(chan event, 16), all: &syncBuf{}, opened: make(chan string, 8), ch: ch}
	ch.Open = func(u string) error { c.opened <- u; return nil }
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c.w = inW
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = ch.Run(ctx, inR, io.MultiWriter(outW, c.all)); close(done) }()
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
	case <-time.After(120 * time.Second):
		c.t.Fatalf("no answer to %s", method)
	}
	return rpc{}
}

// tool calls a tool and returns its text and whether it was an error result.
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

func (c *client) expectEvent(d time.Duration) (event, bool) {
	select {
	case ev := <-c.events:
		return ev, true
	case <-time.After(d):
		return event{}, false
	}
}

// ---- tests -----------------------------------------------------------------

// The instructions are Court's wording; pin them.
const wantInstructions = `cull judges automated tests with Jev and removes the ones that don't earn their place. Court reviews only what Jev is unsure about, on cull's review page.
- After you write or change tests, run cull_check with base set to the merge base of your branch (or no base for the whole suite when Court asks for it).
- Then run cull_apply with no ids: it removes every cut, both Jev's confident ones and Court's, runs the tests before and after, and puts everything back if they break. Never commit on cull's behalf; report what was removed.
- If cull_check reports to_review above 0, run cull_review once to open the page for Court, tell him how many items wait, and carry on with other work. Never ask Court about a test anywhere but the page, and never open the page repeatedly.
- Court's answers arrive as a channel event from cull. Follow its steps: cull_check, then cull_apply for his cuts; rewrite each group he chose to merge as one table test that keeps every row the event lists, then run cull_check_group on it and fix what it reports.
- cull_status tells you what is open, answered and sent for this project.
- When your session runs in a folder that holds several repositories (a workspace), pass the repository's path to every cull tool. The event from Court names the repository's path; use it.
- You never answer for Court. If cull reports an error, say what failed; don't work around it by editing tests by hand.`

func TestInstructionsAreExact(t *testing.T) {
	if Instructions != wantInstructions {
		t.Errorf("instructions changed:\n%s", Instructions)
	}
}

func TestInitializeAndToolsList(t *testing.T) {
	e := newEnv(t, true)
	c := e.connect(ident{"s-1", "claude · one", project(t)})
	m := c.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	var init struct {
		Instructions string `json:"instructions"`
		ServerInfo   struct{ Name string }
		Caps         struct {
			Experimental map[string]any `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(m.Result, &init); err != nil {
		t.Fatal(err)
	}
	if init.ServerInfo.Name != "cull" || init.Instructions != wantInstructions {
		t.Errorf("initialize = %+v", init)
	}
	if _, ok := init.Caps.Experimental["claude/channel"]; !ok {
		t.Error("no claude/channel capability")
	}
	var list struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	_ = json.Unmarshal(c.call("tools/list", nil).Result, &list)
	var names []string
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	want := "cull_apply,cull_check,cull_check_group,cull_review,cull_status"
	got := append([]string(nil), names...)
	sortStrings(got)
	if strings.Join(got, ",") != want {
		t.Errorf("tools = %v", names)
	}
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

type checkResult struct {
	Summary  map[string]int `json:"summary"`
	Cut      []string       `json:"cut"`
	Merge    []string       `json:"merge"`
	ToReview int            `json:"to_review"`
}

type applyResult struct {
	Applied        []string            `json:"applied"`
	NeedsAgent     []map[string]string `json:"needs_agent"`
	ImportsRemoved map[string][]string `json:"imports_removed"`
	Verify         string              `json:"verify"`
	Exit           string              `json:"exit"`
	Reason         string              `json:"reason"`
	RolledBack     bool                `json:"rolled_back"`
}

func TestCheckThenApplyRemovesTheCut(t *testing.T) {
	e := newEnv(t, false)
	root := project(t)
	e.jev.set(map[string]float64{`"test_name":"TestUpper"`: 0.9, `"test_name":"TestMaybe"`: 0.4})
	c := e.connect(ident{"s-1", "claude · one", root})

	var ck checkResult
	c.toolJSON("cull_check", map[string]any{}, &ck)
	if len(ck.Cut) != 1 || ck.Cut[0] != "go:calc_test.go:TestUpper" || ck.ToReview != 1 || ck.Summary["cut"] != 1 {
		t.Fatalf("check = %+v", ck)
	}

	var ap applyResult
	c.toolJSON("cull_apply", map[string]any{}, &ap)
	if ap.Exit != "applied" || ap.Verify != "ok" || len(ap.Applied) != 1 || ap.Applied[0] != "go:calc_test.go:TestUpper" {
		t.Fatalf("apply = %+v", ap)
	}
	if len(ap.ImportsRemoved["calc_test.go"]) == 0 {
		t.Errorf("tidy report missing: %+v", ap)
	}
	b, _ := os.ReadFile(filepath.Join(root, "calc_test.go"))
	if strings.Contains(string(b), "TestUpper") || !strings.Contains(string(b), "TestAdd") {
		t.Errorf("calc_test.go after apply:\n%s", b)
	}
	assertNoKey(t, e, c)
}

func TestApplyRollbackIsReported(t *testing.T) {
	e := newEnv(t, false)
	root := project(t)
	e.jev.set(map[string]float64{`"test_name":"TestAdd"`: 0.9}) // TestUsesAdd calls it: removing it breaks the build
	c := e.connect(ident{"s-1", "claude · one", root})
	var ck checkResult
	c.toolJSON("cull_check", map[string]any{}, &ck)
	if len(ck.Cut) != 1 {
		t.Fatalf("check = %+v", ck)
	}
	var ap applyResult
	c.toolJSON("cull_apply", map[string]any{}, &ap)
	if ap.Exit != "rolled back" || !ap.RolledBack || ap.Verify != "failed" || ap.Reason == "" {
		t.Fatalf("apply = %+v", ap)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "calc_test.go")); string(b) != calcTest {
		t.Error("not restored byte-for-byte")
	}
}

func TestApplyRefusedIsReported(t *testing.T) {
	e := newEnv(t, false)
	root := project(t)
	c := e.connect(ident{"s-1", "claude · one", root})
	var ck checkResult
	c.toolJSON("cull_check", map[string]any{}, &ck)
	var ap applyResult
	c.toolJSON("cull_apply", map[string]any{"ids": []string{"go:calc_test.go:TestNope"}}, &ap)
	if ap.Exit != "refused" || ap.Reason == "" || len(ap.Applied) != 0 {
		t.Fatalf("apply = %+v", ap)
	}
}

func TestCheckErrorsAreOneLine(t *testing.T) {
	e := newEnv(t, false)
	root := project(t)
	c := e.connect(ident{"s-1", "claude · one", root})

	t.Setenv("TYPESAFE_API_KEY", "")
	text, isErr := c.tool("cull_check", map[string]any{})
	if !isErr || text != "no TypeSafe key: Court needs to run cull init" {
		t.Errorf("no key: %v %q", isErr, text)
	}
	t.Setenv("TYPESAFE_API_KEY", fakeKey)

	writeFile(t, root, ".cull.toml", "test_command = \"go test ./...\"\n")
	text, isErr = c.tool("cull_check", map[string]any{})
	if !isErr || text != "egress is off for "+root+": Court needs to run cull init" {
		t.Errorf("egress off: %v %q", isErr, text)
	}
	writeFile(t, root, ".cull.toml", "egress = true\n")

	e.jev.mu.Lock()
	e.jev.status = 401
	e.jev.mu.Unlock()
	text, isErr = c.tool("cull_check", map[string]any{})
	if !isErr || strings.Contains(text, fakeKey) || strings.Contains(text, "\n") {
		t.Errorf("401: %v %q", isErr, text)
	}
	assertNoKey(t, e, c)
}

func TestCheckGroupUnknownIDIsAnError(t *testing.T) {
	e := newEnv(t, false)
	root := project(t)
	c := e.connect(ident{"s-1", "claude · one", root})
	var ck checkResult
	c.toolJSON("cull_check", map[string]any{}, &ck)
	text, isErr := c.tool("cull_check_group", map[string]any{"id": "nope"})
	if !isErr || strings.Contains(text, "\n") {
		t.Errorf("unknown group: %v %q", isErr, text)
	}
}

func TestReviewOpensTheProjectAndOwnsIt(t *testing.T) {
	e := newEnv(t, true)
	root := project(t)
	e.jev.set(map[string]float64{`"test_name":"TestMaybe"`: 0.4})
	c := e.connect(ident{"s-1", "claude · one", root})

	// Nothing recorded yet: nothing to review, and no browser.
	text, isErr := c.tool("cull_review", map[string]any{})
	if isErr || !strings.Contains(text, "nothing to review") {
		t.Fatalf("empty review: %v %q", isErr, text)
	}
	select {
	case u := <-c.opened:
		t.Fatalf("opened %s with nothing to review", u)
	default:
	}

	var ck checkResult
	c.toolJSON("cull_check", map[string]any{}, &ck)
	if ck.ToReview != 1 {
		t.Fatalf("check = %+v", ck)
	}
	var rv struct {
		URL  string `json:"url"`
		Open int    `json:"open"`
	}
	c.toolJSON("cull_review", map[string]any{}, &rv)
	p, err := e.st.Project(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	frag := "#/p/" + strconv.FormatInt(p.ID, 10)
	select {
	case u := <-c.opened:
		if !strings.HasSuffix(u, frag) || u != rv.URL {
			t.Errorf("opened %q, result %q, want suffix %q", u, rv.URL, frag)
		}
	default:
		t.Fatal("the page was not opened")
	}
	if rv.Open != 1 {
		t.Errorf("open = %d", rv.Open)
	}
	p, _ = e.st.Project(context.Background(), root)
	if p.OwnerSession != "s-1" {
		t.Errorf("owner = %q", p.OwnerSession)
	}

	var st struct {
		Open  int    `json:"open"`
		Owner string `json:"owner"`
	}
	c.toolJSON("cull_status", map[string]any{}, &st)
	if st.Open != 1 || st.Owner != "claude · one" {
		t.Errorf("status = %+v", st)
	}
	assertNoKey(t, e, c)
}

// answerAndSend saves one cut answer for the project's open item and presses
// Send through the API, as the page would.
func answerAndSend(t *testing.T, e *env, root string) {
	t.Helper()
	ctx := context.Background()
	p, err := e.st.Project(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	run, items, err := e.st.LatestRun(ctx, p.ID)
	if err != nil || len(items) == 0 {
		t.Fatalf("no items: %v", err)
	}
	it := items[0]
	if err := e.st.SaveAnswers(ctx, p.ID, run.ID, []store.Answer{{ItemID: it.ID, Hash: it.Hash, Kind: it.Kind, Value: "cut", Note: "not needed", Via: "item"}}); err != nil {
		t.Fatal(err)
	}
	adv, err := serve.Running()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", adv.Base+"/api/send", strings.NewReader(`{"project":`+strconv.FormatInt(p.ID, 10)+`}`))
	req.Header.Set(localweb.TokenHeader, adv.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("send: %d %s", resp.StatusCode, b)
	}
}

func TestSendReachesThisChannelOnceAndNoOtherProject(t *testing.T) {
	e := newEnv(t, true)
	rootA, rootB := project(t), project(t)
	e.jev.set(map[string]float64{`"test_name":"TestMaybe"`: 0.4})
	a := e.connect(ident{"s-1", "claude · a", rootA})
	b := e.connect(ident{"s-2", "claude · b", rootB})

	var ck checkResult
	a.toolJSON("cull_check", map[string]any{}, &ck)
	a.toolJSON("cull_review", map[string]any{}, &struct{}{})
	b.toolJSON("cull_check", map[string]any{}, &ck)

	answerAndSend(t, e, rootA)

	ev, ok := a.expectEvent(10 * time.Second)
	if !ok {
		t.Fatal("channel A never got the send")
	}
	for _, want := range []string{"Court sent his answers for " + filepath.Base(rootA), "1 cut", "not needed", "cull_check", "cull_apply"} {
		if !strings.Contains(ev.Content, want) {
			t.Errorf("event missing %q:\n%s", want, ev.Content)
		}
	}
	if ev.Meta["source"] != "cull" || ev.Meta["project"] != rootA || ev.Meta["send_id"] == "" {
		t.Errorf("meta = %v", ev.Meta)
	}
	if _, again := a.expectEvent(1500 * time.Millisecond); again {
		t.Error("the send was delivered twice")
	}
	if ev, got := b.expectEvent(300 * time.Millisecond); got {
		t.Errorf("a channel in another project got the send: %s", ev.Content)
	}
	assertNoKey(t, e, a, b)
}

func TestClientStartsServeWhenNotRunning(t *testing.T) {
	t.Setenv("CULL_HOME", t.TempDir())
	started := 0
	cl := NewClient(func() (serve.Advert, error) { started++; return serve.Advert{}, errors.New("cannot start in a test") })
	_, err := cl.Do(context.Background(), "GET", "/api/agent/status", nil, nil)
	if started != 1 || err == nil || !strings.Contains(err.Error(), "starting cull serve") {
		t.Errorf("started %d, err %v", started, err)
	}
	// The passive copy used by nothing else never starts it.
	if _, err := NewClient(nil).Do(context.Background(), "GET", "/x", nil, nil); !errors.Is(err, ErrNoServe) {
		t.Errorf("err = %v", err)
	}
}

func TestReviewWithoutServeSaysSo(t *testing.T) {
	e := newEnv(t, false)
	c := e.connect(ident{"s-1", "claude · one", project(t)})
	text, isErr := c.tool("cull_status", map[string]any{})
	if !isErr || strings.Contains(text, "\n") || !strings.Contains(text, "cull serve") {
		t.Errorf("status without serve: %v %q", isErr, text)
	}
}

// assertNoKey fails if the fake key appears in anything the channel wrote or logged.
func assertNoKey(t *testing.T, e *env, cs ...*client) {
	t.Helper()
	for _, c := range cs {
		if strings.Contains(c.all.String(), fakeKey) {
			t.Errorf("the key appears in the channel's output")
		}
	}
	if strings.Contains(e.logs.String(), fakeKey) {
		t.Errorf("the key appears in the channel's log")
	}
}

func TestClientSerializesServeStarts(t *testing.T) {
	var mu sync.Mutex
	var live *serve.Advert
	var starts, active, maxActive int32
	cl := NewClient(func() (serve.Advert, error) {
		n := atomic.AddInt32(&active, 1)
		if n > atomic.LoadInt32(&maxActive) {
			atomic.StoreInt32(&maxActive, n)
		}
		atomic.AddInt32(&starts, 1)
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		a := serve.Advert{Base: "http://127.0.0.1:1", Token: "t", PID: 1}
		mu.Lock()
		live = &a
		mu.Unlock()
		return a, nil
	})
	cl.Find = func() (serve.Advert, error) {
		mu.Lock()
		defer mu.Unlock()
		if live == nil {
			return serve.Advert{}, serve.ErrNotRunning
		}
		return *live, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = cl.Do(context.Background(), "GET", "/x", nil, nil) }()
	}
	wg.Wait()
	if starts != 1 || maxActive != 1 {
		t.Errorf("starts %d, max concurrent %d; want 1, 1", starts, maxActive)
	}
}

// A session started in a workspace folder works on a repository inside it:
// cull_check, cull_review and cull_status take the repository's path, the
// session owns that repository's review, and Court's Send reaches it.
func TestWorkspaceSessionReviewsARepositoryInside(t *testing.T) {
	e := newEnv(t, true)
	ws := t.TempDir()
	if r, err := filepath.EvalSymlinks(ws); err == nil {
		ws = r
	}
	repo := projectIn(t, filepath.Join(ws, "shop"))
	e.jev.set(map[string]float64{`"test_name":"TestMaybe"`: 0.4})
	c := e.connect(ident{"s-ws", "claude · workspace", ws})

	var ck checkResult
	c.toolJSON("cull_check", map[string]any{"path": "shop"}, &ck)
	if ck.ToReview != 1 {
		t.Fatalf("check = %+v", ck)
	}
	var rv struct {
		URL  string `json:"url"`
		Open int    `json:"open"`
	}
	c.toolJSON("cull_review", map[string]any{"path": "shop"}, &rv)
	if rv.Open != 1 {
		t.Fatalf("review = %+v", rv)
	}
	select {
	case <-c.opened:
	default:
		t.Fatal("the page was not opened")
	}
	p, err := e.st.Project(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if p.OwnerSession != "s-ws" {
		t.Fatalf("owner = %q", p.OwnerSession)
	}
	var st struct {
		Open int `json:"open"`
	}
	c.toolJSON("cull_status", map[string]any{"path": "shop"}, &st)
	if st.Open != 1 {
		t.Fatalf("status open = %d", st.Open)
	}

	answerAndSend(t, e, repo)
	ev, ok := c.expectEvent(10 * time.Second)
	if !ok {
		t.Fatal("the workspace session never got the send")
	}
	if !strings.Contains(ev.Content, "with path "+repo) || ev.Meta["project"] != repo {
		t.Fatalf("event %q meta %v", ev.Content, ev.Meta)
	}
}

func TestCheckResultCarriesTheSpeedDigest(t *testing.T) {
	e := newEnv(t, false)
	root := project(t)
	writeFile(t, root, "sleepy_test.go", "package fixture\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSleepy(t *testing.T) {\n\ttime.Sleep(3 * time.Second)\n}\n")
	c := e.connect(ident{"s-1", "claude · one", root})
	var ck struct {
		Speed struct {
			Counts           map[string]int `json:"counts"`
			FixedWaitSeconds float64        `json:"fixed_wait_seconds"`
			Top              []struct {
				File string `json:"file"`
				Line int    `json:"line"`
				Kind string `json:"kind"`
			} `json:"top"`
		} `json:"speed"`
	}
	c.toolJSON("cull_check", map[string]any{}, &ck)
	if ck.Speed.Counts["fixed_wait"] != 1 || ck.Speed.FixedWaitSeconds != 3 || len(ck.Speed.Top) != 1 || ck.Speed.Top[0].Line != 9 {
		t.Fatalf("speed = %+v", ck.Speed)
	}
}
