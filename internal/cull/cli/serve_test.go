package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/serve"
)

func serveRig(t *testing.T) (root string, opened *[]string, started *int) {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	root = t.TempDir()
	opened, started = &[]string{}, new(int)
	openBrowser = func(u string) error { *opened = append(*opened, u); return nil }
	startDetached = func(int) (serve.Advert, error) {
		*started++
		return serve.Advert{URL: "http://127.0.0.1:1/?t=tok", Base: "http://127.0.0.1:1"}, nil
	}
	t.Cleanup(func() { openBrowser, startDetached = nil, defaultStartDetached })
	return root, opened, started
}

func writeAdvertFile(t *testing.T, a serve.Advert) {
	t.Helper()
	a.PID = os.Getpid()
	if err := os.MkdirAll(serve.LiveDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(a)
	if err := os.WriteFile(serve.AdvertPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestServeStartsAndOpensProject(t *testing.T) {
	root, opened, started := serveRig(t)
	code, out, errw := run(t, "", "serve", root)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errw)
	}
	if *started != 1 || !strings.Contains(out, "cull serve started: http://127.0.0.1:1") {
		t.Errorf("started=%d out=%q", *started, out)
	}
	if !strings.Contains(out, "no review for") || !strings.Contains(out, "run cull check") {
		t.Errorf("want no-review note: %q", out)
	}
	if len(*opened) != 1 || !strings.HasPrefix((*opened)[0], "http://127.0.0.1:1/?t=tok#/p/") {
		t.Errorf("opened %v", *opened)
	}
}

func TestServeReusesRunning(t *testing.T) {
	root, opened, started := serveRig(t)
	writeAdvertFile(t, serve.Advert{URL: "http://127.0.0.1:2/?t=x", Base: "http://127.0.0.1:2"})
	code, out, errw := run(t, "", "serve", root)
	if code != 0 {
		t.Fatalf("code %d: %s", code, errw)
	}
	if *started != 0 || !strings.Contains(out, "cull serve is running: http://127.0.0.1:2") {
		t.Errorf("started=%d out=%q", *started, out)
	}
	if len(*opened) != 1 || !strings.Contains((*opened)[0], "#/p/") {
		t.Errorf("opened %v", *opened)
	}
}

func TestServeNoOpen(t *testing.T) {
	root, opened, _ := serveRig(t)
	if code, _, errw := run(t, "", "serve", root, "--no-open"); code != 0 {
		t.Fatalf("code %d: %s", code, errw)
	}
	if len(*opened) != 0 {
		t.Errorf("opened %v", *opened)
	}
}

func TestServeStop(t *testing.T) {
	serveRig(t)
	code, out, _ := run(t, "", "serve", "--stop")
	if code != 0 || !strings.Contains(out, "cull serve is not running") {
		t.Errorf("code %d out %q", code, out)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = os.Remove(serve.AdvertPath())
	}))
	defer ts.Close()
	writeAdvertFile(t, serve.Advert{URL: ts.URL, Base: ts.URL})
	code, out, errw := run(t, "", "serve", "--stop")
	if code != 0 || !strings.Contains(out, "cull serve stopped") {
		t.Errorf("code %d out %q err %q", code, out, errw)
	}
}

func TestServeMissingPathExits2(t *testing.T) {
	serveRig(t)
	code, _, errw := run(t, "", "serve", filepath.Join(t.TempDir(), "nope"))
	if code != 2 || !strings.Contains(errw, "nope") {
		t.Errorf("code %d err %q", code, errw)
	}
}
