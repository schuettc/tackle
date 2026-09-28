package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/channel"
	"github.com/schuettc/tackle/internal/casebook/observe"
	"github.com/schuettc/tackle/internal/casebook/serve"
)

func wbSetup(t *testing.T) (*apptest.Rig, *[]string) {
	t.Helper()
	r := apptest.New(t)
	newRunner = func() observe.Runner { return apptest.FakeGh{} }
	opened := &[]string{}
	openBrowser = func(url string) error { *opened = append(*opened, url); return nil }
	t.Cleanup(func() {
		newRunner = func() observe.Runner { return observe.ExecRunner{} }
		openBrowser = nil
		startDetached = defaultStartDetached
	})
	return r, opened
}

func run(args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(""), &out, &errw)
	return code, out.String(), errw.String()
}

func runIn(stdin string, args ...string) (int, string) {
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errw)
	return code, out.String() + errw.String()
}

func waitRunning(t *testing.T) serve.Advert {
	t.Helper()
	for i := 0; i < 200; i++ {
		if a, err := serve.Running(); err == nil {
			return a
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("serve not running")
	return serve.Advert{}
}

func TestServeForegroundOpenStop(t *testing.T) {
	_, opened := wbSetup(t)
	done := make(chan int, 1)
	go func() { code, _, _ := run("serve", "--foreground", "--no-open"); done <- code }()
	adv := waitRunning(t)
	if code, out, _ := run("serve"); code != 0 || !strings.Contains(out, "casebook serve is running: "+adv.Base) {
		t.Fatalf("serve while running: %d %q", code, out)
	}
	if len(*opened) != 1 || !strings.Contains((*opened)[0], "?t="+adv.Token) {
		t.Fatalf("opened %v", *opened)
	}
	if code, out, _ := run("serve", "--stop"); code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop %d %q", code, out)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("foreground exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("foreground serve didn't exit")
	}
	if code, out, _ := run("serve", "--stop"); code != 0 || !strings.Contains(out, "not running") {
		t.Fatalf("stop when stopped %d %q", code, out)
	}
}

func TestServeStartsDetachedWhenNotRunning(t *testing.T) {
	_, opened := wbSetup(t)
	stop := make(chan struct{})
	startDetached = func(port int) (serve.Advert, error) {
		go func() { run("serve", "--foreground", "--no-open"); close(stop) }()
		return waitRunning(t), nil
	}
	if code, out, _ := run("serve"); code != 0 || !strings.Contains(out, "casebook serve started") || len(*opened) != 1 {
		t.Fatalf("start %d %q %v", code, out, *opened)
	}
	run("serve", "--stop")
	<-stop
}

func TestSettledFromClaudeStopHook(t *testing.T) {
	wbSetup(t)
	go run("serve", "--foreground", "--no-open")
	waitRunning(t)
	defer run("serve", "--stop")
	c := channel.NewClient()
	ctx := context.Background()
	c.Do(ctx, http.MethodPost, "/api/agent/presence", map[string]any{"id": "cc-9", "harness": "claude"}, nil)
	var th struct {
		ID int64 `json:"id"`
	}
	c.Do(ctx, http.MethodPost, "/api/threads", map[string]any{"session": "cc-9", "name": "t"}, &th)
	var m struct {
		ID int64 `json:"id"`
	}
	c.Do(ctx, http.MethodPost, "/api/messages", map[string]any{"thread": th.ID, "body": "hi"}, &m)
	if code, _ := c.Do(ctx, http.MethodGet, "/api/agent/wait?session=cc-9&timeout=2", nil, nil); code != 200 {
		t.Fatalf("wait %d", code)
	}
	if code, out := runIn(`{"session_id":"cc-9","hook_event_name":"Stop"}`, "settled", "--harness", "claude"); code != 0 || out != "" {
		t.Fatalf("settled %d %q", code, out)
	}
	var msgs struct {
		Messages []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"messages"`
	}
	c.Do(ctx, http.MethodGet, "/api/messages?thread="+itoa(th.ID), nil, &msgs)
	if len(msgs.Messages) != 1 || msgs.Messages[0].State != "unanswered" {
		t.Fatalf("messages %+v", msgs)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestMcpSelfRoutedInitialize verifies that "casebook mcp" is self-routed by
// Main (bypasses the tools.App dispatcher) and returns a valid MCP initialize
// response on stdout. No wake loop means no presence or wait requests.
func TestMcpSelfRoutedInitialize(t *testing.T) {
	wbSetup(t)
	// mcp should never auto-start serve; swap startDetached to be sure.
	origStart := startDetached
	startDetached = func(port int) (serve.Advert, error) {
		return serve.Advert{}, serve.ErrNotRunning
	}
	t.Cleanup(func() { startDetached = origStart })

	// Use pipes for both stdin and stdout to avoid data races on bytes.Buffer.
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- Main([]string{"mcp"}, stdinR, stdoutW, io.Discard)
	}()
	t.Cleanup(func() { stdinW.Close(); stdoutR.Close() })

	// Send MCP initialize.
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "t", "version": "1"},
		},
	})
	stdinW.Write(append(req, '\n'))

	// Read from stdout until we see the initialize result.
	sc := bufio.NewScanner(stdoutR)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	resultCh := make(chan string, 1)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if strings.Contains(line, `"result"`) {
				resultCh <- line
				return
			}
		}
		close(resultCh)
	}()

	var resultLine string
	select {
	case line, ok := <-resultCh:
		if !ok {
			t.Fatal("stdout closed before initialize result")
		}
		resultLine = line
	case <-time.After(5 * time.Second):
		t.Fatal("no MCP initialize result within 5s")
	}

	// Close stdin so the MCP server exits cleanly.
	stdinW.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mcp did not exit after stdin close")
	}
	if !strings.Contains(resultLine, "casebook") {
		t.Fatalf("missing casebook in MCP result: %q", resultLine)
	}
}

func TestSettledWithoutServeIsSilent(t *testing.T) {
	wbSetup(t)
	for _, args := range [][]string{{"settled", "--session", "x"}, {"settled"}, {"settled", "--harness", "claude"}, {"settled", "--bogus"}} {
		if code, out := runIn("not json", args...); code != 0 || out != "" {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
}

// TestSettledClaudeHungStdinReturns verifies that "settled --harness claude"
// returns promptly (well under 5 s) even when stdin never closes, prints
// nothing, and exits 0.
func TestSettledClaudeHungStdinReturns(t *testing.T) {
	wbSetup(t)
	pr, _ := io.Pipe() // writer is never closed – simulates a hung stdin
	defer pr.Close()

	type result struct {
		code int
		out  string
	}
	done := make(chan result, 1)
	go func() {
		var out, errw bytes.Buffer
		code := Main([]string{"settled", "--harness", "claude"}, pr, &out, &errw)
		done <- result{code, out.String() + errw.String()}
	}()

	select {
	case res := <-done:
		if res.code != 0 || res.out != "" {
			t.Fatalf("settled: code=%d output=%q", res.code, res.out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settled --harness claude with a never-closing stdin did not return within 5 s")
	}
}
