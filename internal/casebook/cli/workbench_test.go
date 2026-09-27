package cli

import (
	"bytes"
	"context"
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

func TestSettledWithoutServeIsSilent(t *testing.T) {
	wbSetup(t)
	for _, args := range [][]string{{"settled", "--session", "x"}, {"settled"}, {"settled", "--harness", "claude"}, {"settled", "--bogus"}} {
		if code, out := runIn("not json", args...); code != 0 || out != "" {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
}
