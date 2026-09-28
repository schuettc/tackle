package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
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
	c.Do(ctx, http.MethodPost, "/api/messages", map[string]any{"thread": th.ID, "body": "hi"}, nil)
	// Wait to get the delivery id (updated from old unconditional settled: now must pass delivery in shown).
	var waited struct {
		Delivery struct {
			ID int64 `json:"id"`
		} `json:"delivery"`
	}
	if code, _ := c.Do(ctx, http.MethodGet, "/api/agent/wait?session=cc-9&timeout=2", nil, &waited); code != 200 {
		t.Fatalf("wait %d", code)
	}
	// Create a temporary transcript showing the delivery.
	transcriptLine := fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"<channel source=\"casebook\" delivery=\"%d\" source=\"casebook\">\nhi\n</channel>"},"sessionId":"cc-9"}`,
		waited.Delivery.ID,
	)
	transcriptFile := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcriptFile, []byte(transcriptLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hookPayload := fmt.Sprintf(`{"session_id":"cc-9","hook_event_name":"Stop","transcript_path":%q}`, transcriptFile)
	if code, out := runIn(hookPayload, "settled", "--harness", "claude"); code != 0 || out != "" {
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

// TestSettledHarnessClaudeTranscript verifies that "settled --harness claude"
// scans the Stop hook payload's transcript_path for casebook deliveries and
// posts them as shown. The testdata fixture has delivery 6 (shown via
// attachment queued_command) and delivery 7 (shown via user entry); delivery 6
// via queue-operation entries must NOT count.
func TestSettledHarnessClaudeTranscript(t *testing.T) {
	wbSetup(t)
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/settled" {
			capturedBody, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	// Replace the settled client so it talks to our capture server.
	prev := newSettledClient
	newSettledClient = func() *channel.Client {
		c := channel.NewClient()
		c.Find = func() (serve.Advert, error) {
			return serve.Advert{Base: ts.URL, Token: "test"}, nil
		}
		return c
	}
	t.Cleanup(func() { newSettledClient = prev })

	// Use the testdata transcript fixture (lines 54, 55 = queue-operation, 59 = attachment, 82 = user).
	transcriptPath := filepath.Join("testdata", "transcript.jsonl")
	hookPayload := fmt.Sprintf(`{"session_id":"test-session-uuid-1","hook_event_name":"Stop","transcript_path":%q}`,
		transcriptPath)
	if code, out := runIn(hookPayload, "settled", "--harness", "claude"); code != 0 || out != "" {
		t.Fatalf("settled %d %q", code, out)
	}

	var body struct {
		Session string  `json:"session"`
		Shown   []int64 `json:"shown"`
	}
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("parse captured body %q: %v", capturedBody, err)
	}
	if body.Session != "test-session-uuid-1" {
		t.Fatalf("session %q, want test-session-uuid-1", body.Session)
	}
	sort.Slice(body.Shown, func(i, j int) bool { return body.Shown[i] < body.Shown[j] })
	if len(body.Shown) != 2 || body.Shown[0] != 6 || body.Shown[1] != 7 {
		t.Fatalf("shown %v, want [6 7]", body.Shown)
	}
}

// TestSettledShownFlag verifies that --shown N,x,M parses valid ids and ignores
// invalid ones, posting them to the server.
func TestSettledShownFlag(t *testing.T) {
	wbSetup(t)
	var capturedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/settled" {
			capturedBody, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	prev := newSettledClient
	newSettledClient = func() *channel.Client {
		c := channel.NewClient()
		c.Find = func() (serve.Advert, error) {
			return serve.Advert{Base: ts.URL, Token: "test"}, nil
		}
		return c
	}
	t.Cleanup(func() { newSettledClient = prev })

	if code, out := runIn("", "settled", "--session", "s-test", "--shown", "3,x,4"); code != 0 || out != "" {
		t.Fatalf("settled %d %q", code, out)
	}
	var body struct {
		Session string  `json:"session"`
		Shown   []int64 `json:"shown"`
	}
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("parse captured body %q: %v", capturedBody, err)
	}
	sort.Slice(body.Shown, func(i, j int) bool { return body.Shown[i] < body.Shown[j] })
	if len(body.Shown) != 2 || body.Shown[0] != 3 || body.Shown[1] != 4 {
		t.Fatalf("shown %v, want [3 4]", body.Shown)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestSettledNoServeShownFlag verifies that --shown works silently even when
// there is no serve running (no serve → silent exit 0).
func TestSettledNoServeShownFlag(t *testing.T) {
	wbSetup(t) // no serve started
	for _, args := range [][]string{
		{"settled", "--session", "x", "--shown", "3,4"},
		{"settled", "--shown", "7,9"},
	} {
		if code, out := runIn("", args...); code != 0 || out != "" {
			t.Errorf("%v: %d %q", args, code, out)
		}
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
