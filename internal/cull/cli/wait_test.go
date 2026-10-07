package cli

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tackle/internal/cull/store"
	"github.com/schuettc/tools-common/localweb"
)

// waitRig runs serve in-process and returns its store.
func waitRig(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	st, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serve.Run(ctx, st, serve.Options{Ready: func(string) { close(ready) }}) }()
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
	return st
}

func waitProject(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// answerAndPressSend records one open item, answers it cut and presses Send.
func answerAndPressSend(t *testing.T, st *store.Store, root string) {
	t.Helper()
	ctx := context.Background()
	p, err := st.Project(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := st.RecordRun(ctx, store.Run{ProjectID: p.ID, Mode: "suite"},
		[]store.Item{{ID: "go:a_test.go:TestMaybe", Kind: "test", Hash: "h1", File: "a_test.go", Name: "TestMaybe"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveAnswers(ctx, p.ID, runID, []store.Answer{{ItemID: "go:a_test.go:TestMaybe", Hash: "h1", Kind: "test", Value: "cut", Note: "not needed", Via: "item"}}); err != nil {
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

type waitResult struct {
	code      int
	out, errw string
}

func startWait(args ...string) <-chan waitResult {
	ch := make(chan waitResult, 1)
	go func() {
		var o, e strings.Builder
		code := Main(append([]string{"wait"}, args...), strings.NewReader(""), &o, &e)
		ch <- waitResult{code, o.String(), e.String()}
	}()
	return ch
}

func TestWaitPrintsTheSendAndExits0(t *testing.T) {
	st := waitRig(t)
	root := waitProject(t)
	res := startWait(root)
	time.Sleep(300 * time.Millisecond)
	answerAndPressSend(t, st, root)
	select {
	case r := <-res:
		want := "Court sent his answers for " + filepath.Base(root) + ": 1 cut, 0 keep, 0 merge, 0 separate.\n" +
			"Notes:\n- TestMaybe: not needed\n" +
			"Next: run cull_check with path " + root + ", then cull_apply with path " + root + " to remove the cuts.\n"
		if r.code != 0 || r.out != want {
			t.Errorf("code %d\nout  %q\nwant %q\nerr  %q", r.code, r.out, want, r.errw)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("wait never returned")
	}
}

func TestWaitTimesOutWithExit3(t *testing.T) {
	waitRig(t)
	res := <-startWait(waitProject(t), "--timeout", "1s")
	if res.code != 3 || res.out != "" || !strings.Contains(res.errw, "nothing sent") {
		t.Errorf("code %d out %q err %q", res.code, res.out, res.errw)
	}
}

func TestWaitDoesNotTakeAnotherProjectsSend(t *testing.T) {
	st := waitRig(t)
	a, b := waitProject(t), waitProject(t)
	res := startWait(a, "--timeout", "2s")
	time.Sleep(300 * time.Millisecond)
	answerAndPressSend(t, st, b)
	r := <-res
	if r.code != 3 || r.out != "" {
		t.Errorf("code %d out %q err %q", r.code, r.out, r.errw)
	}
}

func TestWaitBadTimeoutIsUsage(t *testing.T) {
	code, _, errw := run(t, "", "wait", "--timeout", "0s")
	if code != 2 || !strings.Contains(errw, "--timeout") {
		t.Errorf("code %d err %q", code, errw)
	}
}

// cull wait in a workspace folder takes a Send for a repository inside it.
func TestWaitInAWorkspaceTakesARepositorysSend(t *testing.T) {
	st := waitRig(t)
	ws := waitProject(t)
	repo := filepath.Join(ws, "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	res := startWait(ws, "--timeout", "10s")
	time.Sleep(300 * time.Millisecond)
	answerAndPressSend(t, st, repo)
	r := <-res
	if r.code != 0 || !strings.Contains(r.out, "with path "+repo) {
		t.Errorf("code %d out %q err %q", r.code, r.out, r.errw)
	}
}
