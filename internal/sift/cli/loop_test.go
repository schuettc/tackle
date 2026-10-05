package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/serve"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
	"github.com/schuettc/tools-common/localweb"
)

// ---- serve ------------------------------------------------------------------

func serveRig(t *testing.T) (opened *[]string, started *int) {
	t.Helper()
	siftEnv(t)
	opened, started = &[]string{}, new(int)
	origOpen, origStart := openBrowser, startDetached
	openBrowser = func(u string) error { *opened = append(*opened, u); return nil }
	startDetached = func(int) (serve.Advert, error) {
		*started++
		return serve.Advert{URL: "http://127.0.0.1:1/?t=tok", Base: "http://127.0.0.1:1"}, nil
	}
	t.Cleanup(func() { openBrowser, startDetached = origOpen, origStart })
	return opened, started
}

func TestServeStartsAndOpens(t *testing.T) {
	opened, started := serveRig(t)
	code, out, errw := run(t, "", "serve")
	if code != 0 || *started != 1 || !strings.Contains(out, "sift serve started: http://127.0.0.1:1") || !strings.Contains(out, "run sift check") {
		t.Fatalf("code %d started %d out %q err %q", code, *started, out, errw)
	}
	if len(*opened) != 1 || (*opened)[0] != "http://127.0.0.1:1/?t=tok" {
		t.Errorf("opened %v", *opened)
	}
}

func TestServeNoOpenAndRunning(t *testing.T) {
	opened, started := serveRig(t)
	seedRound(t)
	a := serve.Advert{URL: "http://127.0.0.1:2/?t=x", Base: "http://127.0.0.1:2", PID: os.Getpid()}
	if err := os.MkdirAll(serve.LiveDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(a)
	if err := os.WriteFile(serve.AdvertPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "", "serve", "--no-open")
	if code != 0 || *started != 0 || len(*opened) != 0 || !strings.Contains(out, "is running: http://127.0.0.1:2") || strings.Contains(out, "no round") {
		t.Fatalf("code %d started %d opened %v out %q", code, *started, *opened, out)
	}
}

func TestServeStopWhenNotRunning(t *testing.T) {
	serveRig(t)
	if code, out, _ := run(t, "", "serve", "--stop"); code != 0 || !strings.Contains(out, "not running") {
		t.Fatalf("%d %q", code, out)
	}
}

// ---- wait (cull's wait_test shape) ------------------------------------------

// waitRig runs serve in-process.
func waitRig(t *testing.T) *store.Store {
	t.Helper()
	siftEnv(t)
	s, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serve.Run(ctx, s, serve.Options{Ready: func(string) { close(ready) }}) }()
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
	return s
}

// decideAndPressSend records a round of one row, rejects it with a note and
// presses Send.
func decideAndPressSend(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	r := row.Row{ID: "r1", Check: "negative-rule", Source: row.Source{File: "/w/a/CLAUDE.md", Start: 3, End: 3}, Passage: "- Never push."}
	id, err := s.RecordRound(ctx, store.Round{Kind: "on-demand"}, []row.Row{r})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(ctx, id, "r1", row.Decision{Action: "reject", Note: "it is fine"}); err != nil {
		t.Fatal(err)
	}
	adv, err := serve.Running()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", adv.Base+"/api/send", strings.NewReader(fmt.Sprintf(`{"round":%d}`, id)))
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
	s := waitRig(t)
	res := startWait()
	time.Sleep(300 * time.Millisecond)
	decideAndPressSend(t, s)
	select {
	case r := <-res:
		if r.code != 0 || !strings.HasPrefix(r.out, "The user sent their decisions for sift round 1: 0 accepted, 0 edited, 1 rejected") ||
			!strings.Contains(r.out, "- /w/a/CLAUDE.md:3 · negative-rule: it is fine\n") {
			t.Errorf("code %d\nout %q\nerr %q", r.code, r.out, r.errw)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("wait never returned")
	}
}

func TestWaitTimesOutWithExit3(t *testing.T) {
	waitRig(t)
	res := <-startWait("--timeout", "1s")
	if res.code != 3 || res.out != "" || !strings.Contains(res.errw, "nothing sent") {
		t.Errorf("code %d out %q err %q", res.code, res.out, res.errw)
	}
}

func TestWaitBadTimeoutIsUsage(t *testing.T) {
	siftEnv(t)
	code, _, errw := run(t, "", "wait", "--timeout", "0s")
	if code != 2 || !strings.Contains(errw, "--timeout") {
		t.Errorf("code %d err %q", code, errw)
	}
}

// ---- apply and reconcile ------------------------------------------------------

// applyRig is a published repo with a round of one accepted rewrite.
func applyRig(t *testing.T) string {
	t.Helper()
	siftEnv(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\n- Never push to main.\n"})
	st.Publish(t, repo)
	s, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r := row.Row{ID: "neg", Check: "negative-rule", Passage: "- Never push to main.", Verdict: "rewrite", Text: "- Push to a branch.",
		Source: row.Source{File: filepath.Join(repo, "CLAUDE.md"), Repo: repo, Ref: "origin/main", Path: "CLAUDE.md", Start: 3, End: 3}}
	id, err := s.RecordRound(context.Background(), store.Round{Kind: "on-demand"}, []row.Row{r})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(context.Background(), id, "neg", row.Decision{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestApplyWithoutGhLeavesTheBranch(t *testing.T) {
	repo := applyRig(t)
	code, out, errw := run(t, "", "apply")
	if code != 0 || !strings.Contains(out, "branch sift/round-1 (1 applied, 0 skipped)") || !strings.Contains(out, "no gh") {
		t.Fatalf("code %d out %q err %q", code, out, errw)
	}
	if got := st.Git(t, repo, "show", "sift/round-1:CLAUDE.md"); got != "# App\n\n- Push to a branch." {
		t.Errorf("branch content %q", got)
	}
	code, out, _ = run(t, "", "reconcile")
	if code != 0 || !strings.Contains(out, "1 row(s), 0 problem(s)") {
		t.Fatalf("reconcile %d %q", code, out)
	}
	// Again: the branch exists, so the repo is held (exit 1).
	code, out, _ = run(t, "", "apply")
	if code != 1 || !strings.Contains(out, "held") {
		t.Fatalf("again: %d %q", code, out)
	}
}

func TestApplyWithGhOpensAPullRequest(t *testing.T) {
	repo := applyRig(t)
	bare := st.Git(t, repo, "remote", "get-url", "origin")
	st.Git(t, repo, "remote", "set-url", "origin", "https://github.com/owner/app.git")
	st.Git(t, repo, "config", "url."+bare+".insteadOf", "https://github.com/owner/app.git")
	lookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	orig := ghRunner
	ghRunner = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		return []byte("https://github.com/owner/app/pull/3\n"), nil
	}
	t.Cleanup(func() { ghRunner = orig })
	code, out, errw := run(t, "", "apply", "--json")
	var res apply.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != 0 {
		t.Fatalf("code %d out %q err %q", code, out, errw)
	}
	if res.Repos[0].State != "pr" || res.Repos[0].PR != "https://github.com/owner/app/pull/3" {
		t.Fatalf("%+v", res.Repos[0])
	}
}

func TestReconcileWithNothingApplied(t *testing.T) {
	siftEnv(t)
	seedRound(t)
	code, out, _ := run(t, "", "reconcile")
	if code != 0 || !strings.Contains(out, "no branch to reconcile") {
		t.Fatalf("%d %q", code, out)
	}
}
