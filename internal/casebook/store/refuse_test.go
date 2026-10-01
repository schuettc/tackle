package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// writeScript writes an executable shell script.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755))
}

// runs counts the lines a script appended to a tally file.
func runs(t *testing.T, tally string) int {
	t.Helper()
	b, err := os.ReadFile(tally)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	must(t, err)
	return strings.Count(string(b), "\n")
}

// TestRemoteRefusalIsNotARace: a remote that refuses the push (a
// pre-receive hook, branch protection) is reported once, with git's
// "remote rejected" line and the remote's own words; it isn't retried as a
// race ("kept racing") and isn't offline (which would hide it).
func TestRemoteRefusalIsNotARace(t *testing.T) {
	a, remote := newStore(t)
	tally := filepath.Join(t.TempDir(), "hook-runs")
	writeScript(t, filepath.Join(remote, "hooks", "pre-receive"),
		"echo run >> '"+tally+"'\necho 'casebook-data is read-only today' >&2\nexit 1\n")
	must(t, a.Decide(ctx, item.RepoKey("a/b"), dec(item.Keep, "c", time.Now())))

	_, err := a.Sync(ctx)
	if err == nil {
		t.Fatal("a refused push reported success")
	}
	if errors.Is(err, ErrOffline) {
		t.Fatalf("a refused push reads as offline: %v", err)
	}
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v, want ErrRefused", err)
	}
	msg := err.Error()
	t.Logf("refusal: %s", msg)
	for _, want := range []string{"remote rejected", "pre-receive hook declined", "casebook-data is read-only today"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "racing") {
		t.Errorf("a refusal reads as a race: %q", msg)
	}
	if n := runs(t, tally); n != 1 {
		t.Errorf("the remote was asked %d times, want once (a refusal isn't retried)", n)
	}
	if got := testgit.Git(t, a.Dir, "rev-list", "--count", "origin/main..HEAD"); got != "1" {
		t.Errorf("queued %s, want the decision still queued (1)", got)
	}
}

// TestRefusalThatSaysNonFastForwardIsNotARace: only git's own
// "! [rejected] … (non-fast-forward)" line is a race. A hook whose message
// mentions non-fast-forward still refuses, once, with its words.
func TestRefusalThatSaysNonFastForwardIsNotARace(t *testing.T) {
	a, remote := newStore(t)
	tally := filepath.Join(t.TempDir(), "hook-runs")
	writeScript(t, filepath.Join(remote, "hooks", "pre-receive"),
		"echo run >> '"+tally+"'\necho 'policy: non-fast-forward and fetch first rules apply; pushes are frozen' >&2\nexit 1\n")
	must(t, a.Decide(ctx, item.RepoKey("a/b"), dec(item.Keep, "c", time.Now())))

	_, err := a.Sync(ctx)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v, want ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "pushes are frozen") {
		t.Errorf("error %q lacks the hook's words", err)
	}
	if n := runs(t, tally); n != 1 {
		t.Errorf("the remote was asked %d times, want once", n)
	}
}

// TestPushRaceRebasesAndRetries: another machine pushes between this
// machine's fetch and its push. git rejects the push (fetch first); Sync
// fetches, rebases onto the other machine's commit and pushes again.
func TestPushRaceRebasesAndRetries(t *testing.T) {
	a, b, remote := twoMachines(t)
	must(t, b.Decide(ctx, item.RepoKey("b/b"), dec(item.Keep, "court@b", time.Now())))
	must(t, a.Decide(ctx, item.RepoKey("a/a"), dec(item.Keep, "court@a", time.Now())))

	// a's receive-pack: on its first run, b pushes first, so a's push
	// finds a remote that moved after a's fetch.
	tally := filepath.Join(t.TempDir(), "receive-runs")
	wrap := filepath.Join(t.TempDir(), "receive-pack")
	writeScript(t, wrap, "if [ ! -f '"+tally+"' ]; then\n"+
		"  git -C '"+b.Dir+"' push -q origin HEAD:main >&2 || exit 1\n"+
		"fi\n"+
		"echo run >> '"+tally+"'\n"+
		"exec git-receive-pack \"$@\"\n")
	testgit.Git(t, a.Dir, "config", "remote.origin.receivepack", wrap)

	res, err := a.Sync(ctx)
	if err != nil {
		t.Fatalf("a push race: %v", err)
	}
	if !res.Pushed || !res.Pulled {
		t.Errorf("result %+v, want pulled (rebased) and pushed", res)
	}
	if n := runs(t, tally); n != 2 {
		t.Errorf("pushed %d times, want 2 (the race, then the retry)", n)
	}
	l := testgit.Git(t, remote, "log", "--format=%s", "main")
	for _, want := range []string{"decide repo:a/a", "decide repo:b/b"} {
		if !strings.Contains(l, want) {
			t.Errorf("remote lacks %q:\n%s", want, l)
		}
	}
	if testgit.Git(t, a.Dir, "rev-list", "--merges", "--count", "HEAD") != "0" {
		t.Error("history is not linear")
	}
}

// TestPushUnreachableIsOffline: a remote that fetches but can't be pushed
// to (gone, no route) is offline: the decision stays queued, no refusal.
func TestPushUnreachableIsOffline(t *testing.T) {
	a, _ := newStore(t)
	testgit.Git(t, a.Dir, "config", "remote.origin.pushurl", filepath.Join(t.TempDir(), "gone.git"))
	must(t, a.Decide(ctx, item.RepoKey("a/b"), dec(item.Keep, "c", time.Now())))
	_, err := a.Sync(ctx)
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v, want ErrOffline", err)
	}
	if errors.Is(err, ErrRefused) {
		t.Fatalf("an unreachable remote reads as a refusal: %v", err)
	}
}
