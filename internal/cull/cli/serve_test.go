package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tackle/internal/cull/store"
)

func serveRig(t *testing.T) (root string, opened *[]string, started *int) {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	root = t.TempDir()
	opened, started = &[]string{}, new(int)
	origOpen, origStart := openBrowser, startDetached
	openBrowser = func(u string) error { *opened = append(*opened, u); return nil }
	startDetached = func(int) (serve.Advert, error) {
		*started++
		return serve.Advert{URL: "http://127.0.0.1:1/?t=tok", Base: "http://127.0.0.1:1"}, nil
	}
	t.Cleanup(func() { openBrowser, startDetached = origOpen, origStart })
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
	want := "http://127.0.0.1:1/?t=tok#/p/" + projectID(t, root)
	if len(*opened) != 1 || (*opened)[0] != want {
		t.Errorf("opened %v, want [%s]", *opened, want)
	}
}

// projectID is the store's id for the project rooted at root.
func projectID(t *testing.T, root string) string {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	p, err := st.Project(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(p.ID, 10)
}

func TestServeSubdirOpensSameProject(t *testing.T) {
	root, opened, _ := serveRig(t)
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root, sub} {
		if code, _, errw := run(t, "", "serve", p); code != 0 {
			t.Fatalf("code %d: %s", code, errw)
		}
	}
	want := "http://127.0.0.1:1/?t=tok#/p/" + projectID(t, real)
	if len(*opened) != 2 || (*opened)[0] != want || (*opened)[1] != want {
		t.Errorf("opened %v, want both %s", *opened, want)
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
	want := "http://127.0.0.1:2/?t=x#/p/" + projectID(t, root)
	if len(*opened) != 1 || (*opened)[0] != want {
		t.Errorf("opened %v, want [%s]", *opened, want)
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
