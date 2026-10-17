package channel

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/imprint/npm"
	"github.com/subaud-tools/tackle/internal/imprint/ops"
	"github.com/subaud-tools/tackle/internal/imprint/registry"
)

// client drives a channel over pipes: answers by id, channel messages in
// the order sent.
type client struct {
	t       *testing.T
	w       io.Writer
	mu      sync.Mutex
	answers map[int]chan map[string]any
	notes   chan map[string]any
	id      int
}

func drive(t *testing.T, ch *Channel) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = ch.Run(ctx, inR, outW) }()
	t.Cleanup(func() { cancel(); _ = inW.Close() })
	c := &client{t: t, w: inW, answers: map[int]chan map[string]any{}, notes: make(chan map[string]any, 100)}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m struct {
				ID     *int           `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
				Result map[string]any `json:"result"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			if m.ID != nil {
				c.answer(*m.ID) <- m.Result
			} else if m.Method == "notifications/claude/channel" {
				c.notes <- m.Params
			}
		}
	}()
	return c
}

func (c *client) answer(id int) chan map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.answers[id] == nil {
		c.answers[id] = make(chan map[string]any, 1)
	}
	return c.answers[id]
}

func (c *client) call(method string, params any) map[string]any {
	c.t.Helper()
	c.mu.Lock()
	c.id++
	id := c.id
	c.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	select {
	case r := <-c.answer(id):
		return r
	case <-time.After(5 * time.Second):
		c.t.Fatalf("no answer to %s", method)
		return nil
	}
}

// tool calls a tool and returns its text.
func (c *client) tool(name string, args map[string]any) (string, bool) {
	c.t.Helper()
	r := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	content, _ := r["content"].([]any)
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	isErr, _ := r["isError"].(bool)
	return text, isErr
}

// note waits for the next channel message.
func (c *client) note() (string, map[string]any) {
	c.t.Helper()
	select {
	case p := <-c.notes:
		meta, _ := p["meta"].(map[string]any)
		s, _ := p["content"].(string)
		return s, meta
	case <-time.After(10 * time.Second):
		c.t.Fatal("no channel message")
		return "", nil
	}
}

// registryStub serves one package's packument, which a test replaces.
type registryStub struct {
	mu  sync.Mutex
	doc string
}

func (s *registryStub) set(doc string) { s.mu.Lock(); s.doc = doc; s.mu.Unlock() }

func (s *registryStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.doc == "" || r.URL.Path != "/demo-pkg" {
		http.NotFound(w, r)
		return
	}
	_, _ = io.WriteString(w, s.doc)
}

func newChannel(t *testing.T) (*Channel, *registryStub, string) {
	t.Helper()
	reg := &registryStub{}
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.Mkdir(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_NPM_SCRIPT", scripts)
	t.Setenv("FAKE_NPM_ARGS", filepath.Join(dir, "args"))
	bin, err := filepath.Abs("../npm/testdata/fake-npm")
	if err != nil {
		t.Fatal(err)
	}
	o := &ops.Ops{
		Reg:  &registry.Client{Base: srv.URL, HTTP: srv.Client()},
		NPM:  &npm.Runner{NPM: bin, LogDir: filepath.Join(dir, "logs"), Open: func(string) error { return nil }},
		Poll: 10 * time.Millisecond, Warn: time.Hour, GiveUp: 2 * time.Hour,
	}
	return New(o, "test"), reg, scripts
}

func tarball(t *testing.T, name, version string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := fmt.Sprintf(`{"name":%q,"version":%q}`, name, version)
	_ = tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	p := filepath.Join(t.TempDir(), name+"-"+version+".tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestToolsList(t *testing.T) {
	ch, _, _ := newChannel(t)
	c := drive(t, ch)
	r := c.call("tools/list", map[string]any{})
	list, _ := r["tools"].([]any)
	var names []string
	for _, x := range list {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	want := "imprint_login imprint_show imprint_publish imprint_trust imprint_deprecate imprint_expect imprint_status"
	if strings.Join(names, " ") != want {
		t.Errorf("tools %v", names)
	}
}

// imprint_publish returns at once; its messages arrive in order, live
// only once the registry shows the version; imprint_status shows it
// meanwhile.
func TestPublishReturnsAtOnceAndReports(t *testing.T) {
	ch, reg, scripts := newChannel(t)
	if err := os.WriteFile(filepath.Join(scripts, "publish"), []byte("print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/c1\nsleep 0.1\nprint + demo-pkg@1.0.0\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := drive(t, ch)
	text, isErr := c.tool("imprint_publish", map[string]any{"tarball": tarball(t, "demo-pkg", "1.0.0")})
	if isErr || !strings.HasPrefix(text, "started: publishing demo-pkg@1.0.0") || !strings.Contains(text, "progress arrives as messages from imprint") {
		t.Fatalf("%v %q", isErr, text)
	}
	msg, meta := c.note()
	if !strings.Contains(msg, "waiting for your approval: https://www.npmjs.com/auth/cli/c1") || meta["op"] != "imprint_publish" || meta["package"] != "demo-pkg" {
		t.Fatalf("%q %v", msg, meta)
	}
	if msg, _ := c.note(); msg != "approved" {
		t.Fatalf("%q", msg)
	}
	if msg, _ := c.note(); !strings.Contains(msg, "is processing it") {
		t.Fatalf("%q", msg)
	}
	status, _ := c.tool("imprint_status", nil)
	if !strings.Contains(status, "imprint_publish: publishing demo-pkg@1.0.0") || !strings.Contains(status, "is processing it") {
		t.Errorf("status %q", status)
	}
	reg.set(`{"name":"demo-pkg","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{}}}`)
	if msg, _ := c.note(); msg != "live on npm: demo-pkg@1.0.0" {
		t.Fatalf("%q", msg)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ = c.tool("imprint_status", nil)
		if strings.HasPrefix(status, "nothing is running") || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(status, "nothing is running") {
		t.Errorf("status after: %q", status)
	}
}

func TestMissingArgumentsAreErrors(t *testing.T) {
	ch, _, _ := newChannel(t)
	c := drive(t, ch)
	if text, isErr := c.tool("imprint_trust", map[string]any{"package": "demo-pkg"}); !isErr || !strings.Contains(text, "repo, workflow") {
		t.Errorf("%v %q", isErr, text)
	}
	if text, isErr := c.tool("imprint_show", map[string]any{"package": "demo-pkg"}); isErr || !strings.Contains(text, "not on npm") {
		t.Errorf("%v %q", isErr, text)
	}
}
