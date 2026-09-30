package serve

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tools-common/localweb"
)

func TestRunAdvertStopAndInterrupt(t *testing.T) {
	r := apptest.New(t)
	started := make(chan Advert, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- Run(context.Background(), r.App, Options{Version: "test", Started: func(_ *Server, a Advert) { started <- a }})
	}()
	var adv Advert
	select {
	case adv = <-started:
	case err := <-errc:
		t.Fatalf("run: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not start")
	}
	got, err := Running()
	if err != nil || got.PID != os.Getpid() || got.Token == "" || got.Base != adv.Base {
		t.Fatalf("advert %+v %v", got, err)
	}
	fi, _ := os.Stat(AdvertPath())
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("advert mode %v", fi.Mode().Perm())
	}
	// Header auth works for the channel; no token is refused.
	req, _ := http.NewRequest("GET", adv.Base+"/api/summary", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	req.Header.Set(localweb.TokenHeader, adv.Token)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if err := Run(context.Background(), r.App, Options{}); err == nil {
		t.Fatal("second serve started")
	}
	if err := Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("run returned %v", err)
	}
	if _, err := Running(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("after stop: %v", err)
	}
}

func TestStaleAdvertIsNotRunning(t *testing.T) {
	apptest.New(t)
	_ = writeAdvert(Advert{PID: 999999, Base: "http://127.0.0.1:1"})
	if _, err := Running(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(AdvertPath()); !os.IsNotExist(err) {
		t.Fatal("stale advert not removed")
	}
}

// runOnce starts serve, returns its advert and whether it was told the page
// was open, and a stop function.
func runOnce(t *testing.T, r *apptest.Rig, opened *[]string) (Advert, bool, func()) {
	t.Helper()
	started := make(chan Advert, 1)
	was := make(chan bool, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- Run(context.Background(), r.App, Options{Version: "test",
			Ready:   func(_ string, pageWasOpen bool) { was <- pageWasOpen },
			Open:    func(url string) error { *opened = append(*opened, url); return nil },
			Started: func(_ *Server, a Advert) { started <- a }})
	}()
	var adv Advert
	select {
	case adv = <-started:
	case err := <-errc:
		t.Fatalf("run: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not start")
	}
	return adv, <-was, func() {
		if err := Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-errc
	}
}

func call(t *testing.T, adv Advert, method, path string) int {
	t.Helper()
	req, _ := http.NewRequest(method, adv.Base+path, strings.NewReader("{}"))
	req.Header.Set(localweb.TokenHeader, adv.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestRestartReopensAnOpenPage(t *testing.T) {
	r := apptest.New(t)
	var opened []string
	// No tab: a restart doesn't open one, and agent traffic doesn't count.
	adv, was, stop := runOnce(t, r, &opened)
	if was || adv.Reopened {
		t.Fatalf("first start: was %v, reopened %v", was, adv.Reopened)
	}
	call(t, adv, "POST", "/api/agent/presence")
	call(t, adv, "GET", "/api/summary")
	stop()
	adv, was, stop = runOnce(t, r, &opened)
	if was || adv.Reopened {
		t.Fatal("requests without a connected tab made the restart reopen the page")
	}
	// A tab connects, then closes: the page is closed, so no reopen.
	closeTab := stream(t, adv.Base, adv.Token)
	time.Sleep(100 * time.Millisecond)
	closeTab()
	time.Sleep(100 * time.Millisecond)
	stop()
	adv, was, stop = runOnce(t, r, &opened)
	if was || adv.Reopened {
		t.Fatal("a tab closed before the restart was reopened")
	}
	// A tab is connected when serve stops: the next serve reopens it, however
	// long the gap. casebook_open works too.
	closeTab = stream(t, adv.Base, adv.Token)
	defer closeTab()
	time.Sleep(100 * time.Millisecond)
	if c := call(t, adv, "POST", "/api/agent/open"); c != http.StatusOK || len(opened) != 1 || !strings.Contains(opened[0], "?t="+adv.Token) {
		t.Fatalf("open: %d %v", c, opened)
	}
	stop()
	adv, was, stop = runOnce(t, r, &opened)
	if !was || !adv.Reopened {
		t.Fatalf("restart with a connected tab: was %v, reopened %v", was, adv.Reopened)
	}
	if got, _ := Running(); !got.Reopened {
		t.Fatal("advert doesn't say the page was reopened")
	}
	// The flag was taken: another restart with no tab doesn't reopen again.
	stop()
	adv, was, stop = runOnce(t, r, &opened)
	defer stop()
	if was || adv.Reopened {
		t.Fatal("the page was reopened twice from one connected tab")
	}
}

func callBody(t *testing.T, adv Advert, path, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", adv.Base+path, strings.NewReader(body))
	req.Header.Set(localweb.TokenHeader, adv.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestOpenLandsOnAnItemOrAView(t *testing.T) {
	r := apptest.New(t)
	var opened []string
	adv, _, stop := runOnce(t, r, &opened)
	defer stop()
	for _, c := range []struct {
		body string
		code int
		url  string // suffix of the opened URL; "" when nothing opens
	}{
		{`{}`, 200, "?t=" + adv.Token},
		{`{"key":"pr:schuettc/hail#3"}`, 200, "?t=" + adv.Token + "#/item/pr:schuettc%2Fhail%233"},
		{`{"view":"proposed"}`, 200, "?t=" + adv.Token + "#/attention/proposed"},
		{`{"view":"board"}`, 200, "#/attention/board"},
		{`{"key":"pr:schuettc/hail#999"}`, 404, ""},
		{`{"key":"not a key"}`, 400, ""},
		{`{"view":"everything"}`, 400, ""},
		{`{"key":"pr:schuettc/hail#3","view":"new"}`, 400, ""},
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
}
