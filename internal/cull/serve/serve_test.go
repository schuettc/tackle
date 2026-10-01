package serve

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/store"
	"github.com/schuettc/tools-common/localweb"
)

type running struct {
	done chan error
	url  chan string
	log  *strings.Builder
}

func startRun(t *testing.T, o Options) *running {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "cull.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &running{done: make(chan error, 1), url: make(chan string, 1), log: &strings.Builder{}}
	o.Log = &lockedWriter{w: r.log}
	o.Ready = func(u string) { r.url <- u }
	go func() { r.done <- Run(ctx, st, o) }()
	select {
	case <-r.url:
	case err := <-r.done:
		t.Fatalf("run exited: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	return r
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func wait(t *testing.T, r *running) {
	t.Helper()
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not exit")
	}
}

func TestAdvertPrivateAndRemovedOnExit(t *testing.T) {
	r := startRun(t, Options{Version: "v"})
	st, err := os.Stat(AdvertPath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("advert %v", st.Mode().Perm())
	}
	if d, _ := os.Stat(LiveDir()); d.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v", d.Mode().Perm())
	}
	adv, err := Running()
	if err != nil || adv.PID != os.Getpid() || adv.Version != "v" || adv.Token == "" {
		t.Fatalf("%+v %v", adv, err)
	}
	// a second serve is refused while this one lives
	st2, _ := store.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"))
	defer func() { _ = st2.Close() }()
	if err := Run(context.Background(), st2, Options{}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second run: %v", err)
	}
	if err := Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	wait(t, r)
	if _, err := os.Stat(AdvertPath()); !os.IsNotExist(err) {
		t.Fatalf("advert remains: %v", err)
	}
	if _, err := Running(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("running: %v", err)
	}
	if l := r.log.String(); !strings.Contains(l, "started") || !strings.Contains(l, "stopped") {
		t.Fatalf("log %q", l)
	}
}

func TestStaleAdvertRemoved(t *testing.T) {
	t.Setenv("CULL_HOME", t.TempDir())
	if err := writeAdvert(Advert{PID: 1 << 30, Token: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Running(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(AdvertPath()); !os.IsNotExist(err) {
		t.Fatal("stale advert kept")
	}
}

func TestIdleExit(t *testing.T) {
	r := startRun(t, Options{Idle: 200 * time.Millisecond})
	wait(t, r)
	if _, err := Running(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("%v", err)
	}
}

func TestRemoveAdvertLeavesOtherPID(t *testing.T) {
	t.Setenv("CULL_HOME", t.TempDir())
	if err := writeAdvert(Advert{PID: 4242, Token: "x"}); err != nil {
		t.Fatal(err)
	}
	removeAdvert(os.Getpid())
	if a, err := readAdvert(); err != nil || a.PID != 4242 {
		t.Fatalf("advert gone or changed: %+v %v", a, err)
	}
	removeAdvert(4242)
	if _, err := os.Stat(AdvertPath()); !os.IsNotExist(err) {
		t.Fatalf("own advert kept: %v", err)
	}
}

func TestStopRequiresToken(t *testing.T) {
	r := startRun(t, Options{})
	adv, _ := Running()
	resp, err := http.Post(adv.Base+"/api/stop", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("stop without token: %d", resp.StatusCode)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := Running(); err != nil {
		t.Fatalf("server stopped: %v", err)
	}
	select {
	case err := <-r.done:
		t.Fatalf("run exited: %v", err)
	default:
	}
	_ = Stop(context.Background())
	wait(t, r)
}

func TestIdleExitWithIdleStreamOpen(t *testing.T) {
	r := startRun(t, Options{Idle: 300 * time.Millisecond})
	adv, _ := Running()
	req, _ := http.NewRequest("GET", adv.Base+"/api/events", nil)
	req.Header.Set(localweb.TokenHeader, adv.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	wait(t, r) // an open but idle tab doesn't keep the server alive
}

func TestAPIRequiresToken(t *testing.T) {
	r := startRun(t, Options{})
	adv, _ := Running()
	resp, err := http.Get(adv.Base + "/api/poll")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", adv.Base+"/api/poll", nil)
	req.Header.Set(localweb.TokenHeader, adv.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("token: %d", resp.StatusCode)
	}
	if resp, err = http.Get(adv.Base + "/"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("index: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	_ = Stop(context.Background())
	wait(t, r)
}
