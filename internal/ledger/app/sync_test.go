package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/ledger/config"
	"github.com/schuettc/tackle/internal/ledger/journal"
	"github.com/schuettc/tackle/internal/ledger/spool"
	"github.com/schuettc/tackle/internal/ledger/testgit"
)

func TestOpenUninitialized(t *testing.T) {
	testgit.Env(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LEDGER_HOME", t.TempDir())
	if _, err := Open(&fakeGh{}); !errors.Is(err, config.ErrNotInitialized) {
		t.Fatalf("got %v", err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	r := newRig(t)
	cfg, err := Init(ctx, InitOptions{Remote: r.remote, Machine: "mbp", User: "schuettc"}, &fakeGh{})
	if err != nil || cfg.Roots[0] != r.root {
		t.Fatalf("re-init: %+v %v (must keep the existing config)", cfg, err)
	}
}

func TestSyncEndToEnd(t *testing.T) {
	r := newRig(t)
	ev := journal.Event{V: 1, TS: r.now, Src: "git-hook", Hook: "pre-push", CWD: r.clone, Args: []string{"origin", "x"}}
	claude := journal.Event{V: 1, TS: r.now, Src: "claude", CWD: filepath.Join(r.clone, "sub"),
		Actions: []journal.Action{{Tool: "gh", Verb: "pr merge", Number: 3}}}
	for _, e := range []journal.Event{ev, claude} {
		if err := spool.Append(config.SpoolDir(), e); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := r.app.Sync(ctx, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != 2 || rep.Clones != 1 || !rep.Committed || !rep.Pushed || rep.Offline || len(rep.GitHubErrors) != 0 {
		t.Fatalf("report %+v", rep)
	}
	j, err := r.app.Repo.ReadFile("journal/mbp/2026/09-24.jsonl")
	if err != nil || strings.Count(string(j), "\n") != 2 || !strings.Contains(string(j), `"repo":"schuettc/hail"`) || !strings.Contains(string(j), `"machine":"mbp"`) {
		t.Fatalf("journal %s %v", j, err)
	}
	evs, _ := r.app.Events()
	if len(evs) != 2 || evs[1].Actions[0].Repo != "schuettc/hail" {
		t.Errorf("gh action repo not filled from cwd: %+v", evs)
	}
	readme, _ := r.app.Repo.ReadFile("README.md")
	for _, want := range []string{"branch:schuettc/hail@feat/client", "pr:schuettc/hail#3"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README lacks %s", want)
		}
	}
	if _, err := r.app.Repo.ReadFile("machines/mbp.json"); err != nil {
		t.Error(err)
	}
	if got := testgit.Git(t, r.remote, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "sync mbp") {
		t.Errorf("remote head %q", got)
	}
	rep2, err := r.app.Sync(ctx, SyncOptions{})
	if err != nil || rep2.Committed || rep2.Events != 0 {
		t.Fatalf("idle sync committed: %+v %v", rep2, err)
	}
}

func TestSyncOfflineCommitsLocallyAndDrains(t *testing.T) {
	r := newRig(t)
	moved := r.remote + ".away"
	os.Rename(r.remote, moved)
	spool.Append(config.SpoolDir(), journal.Event{V: 1, TS: r.now, Src: "git-hook", Hook: "post-commit", CWD: r.clone})
	rep, err := r.app.Sync(ctx, SyncOptions{NoGitHub: true})
	if err != nil || !rep.Offline || !rep.Committed || rep.Pushed {
		t.Fatalf("offline sync %+v %v", rep, err)
	}
	b, _ := spool.Drain(config.SpoolDir())
	if len(b.Events) != 0 {
		t.Error("events left in the spool after a local commit")
	}
	b.Close()
	os.Rename(moved, r.remote)
	rep, err = r.app.Sync(ctx, SyncOptions{NoGitHub: true})
	if err != nil || !rep.Pushed {
		t.Fatalf("reconnected sync %+v %v", rep, err)
	}
}

func TestSyncKeepsSpoolWhenCommitFails(t *testing.T) {
	r := newRig(t)
	spool.Append(config.SpoolDir(), journal.Event{V: 1, TS: r.now, Src: "git-hook", Hook: "post-commit", CWD: r.clone})
	lock := filepath.Join(r.app.Repo.Dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.app.Sync(ctx, SyncOptions{NoGitHub: true, NoPush: true}); err == nil {
		t.Fatal("sync succeeded although git could not commit")
	}
	os.Remove(lock)
	b, _ := spool.Drain(config.SpoolDir())
	defer b.Close()
	if len(b.Events) != 1 {
		t.Fatalf("spool lost the event: %d", len(b.Events))
	}
}

func TestSyncGitHubFailureKeepsCache(t *testing.T) {
	r := newRig(t)
	if _, err := r.app.Sync(ctx, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	r.app.Gh = &fakeGh{fail: true}
	r.now = r.now.Add(time.Hour)
	rep, err := r.app.Sync(ctx, SyncOptions{})
	if err != nil || len(rep.GitHubErrors) == 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	res, _, _ := r.app.Build(ctx)
	if _, ok := res.Find("pr:schuettc/hail#3"); !ok {
		t.Error("failed refresh dropped a known PR")
	}
}
