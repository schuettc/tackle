package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/rules"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// remoteAhead puts a commit on casebook-data's remote that serve's clone
// doesn't have (another machine's sync), so serve's next push rebases.
func remoteAhead(t *testing.T, r *rig, file string) {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	testgit.Git(t, filepath.Dir(other), "clone", "-q", r.Remote, other)
	testgit.Commit(t, other, file, "from another machine\n")
	testgit.Git(t, other, "push", "-q", "origin", "HEAD:main")
}

// onMain fails unless serve's clone is on main, with no rebase in progress.
func onMain(t *testing.T, r *rig) {
	t.Helper()
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(r.App.Repo.Dir, ".git", d)); err == nil {
			t.Fatalf("casebook-data is left mid-rebase (.git/%s)", d)
		}
	}
	if ref, err := git(t, r.App.Repo.Dir, "symbolic-ref", "-q", "HEAD"); err != nil || ref != "refs/heads/main" {
		t.Fatalf("HEAD %q (%v), want refs/heads/main", ref, err)
	}
}

// TestRebuildNeverReadsAMidRebaseTree: while serve's push rebases onto a
// remote that's ahead, the working tree is the remote's commits without the
// local ones, so it lacks a decision just made. A rebuild then (the watch
// loop, another request's) waits for the rebase: the decided item never
// shows as undecided, and an active rule never proposes it.
func TestRebuildNeverReadsAMidRebaseTree(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#4"
	ru := rules.Rule{
		ID: "close-issues", Name: "Close issues", Status: rules.StatusActive,
		CreatedBy: "court", CreatedAt: r.s.Now(), EditedAt: r.s.Now(),
		Match:   []rules.Condition{{Field: "kind", Op: "is", Value: "issue"}},
		Propose: rules.RuleAction{Disposition: "close"},
	}
	if err := r.App.Repo.WriteRule(ctx, ru, "rule close-issues created by court"); err != nil {
		t.Fatal(err)
	}
	// The rule is on the remote too: the mid-rebase tree has it.
	if err := r.App.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.s.rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := r.s.Props.Pending(ctx); p[key].ID == 0 {
		t.Fatalf("the rule didn't propose %s: %+v", key, p)
	}

	started, release := blockPush(t, r)
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{key}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	<-started
	if p, _ := r.s.Props.Pending(ctx); p[key].ID != 0 {
		t.Fatalf("the decide left %s proposed: %+v", key, p[key])
	}
	remoteAhead(t, r, "notes/other.txt")
	hold := testgit.HoldRebase(t, r.App.Repo.Dir)
	release()
	hold.Started()

	// A rebuild while the rebase is held.
	rebuilt := make(chan error, 1)
	go func() { rebuilt <- r.s.rebuild(ctx) }()
	select {
	case err := <-rebuilt:
		// It read the tree mid-rebase: what did it see?
		if err != nil {
			t.Fatal(err)
		}
		it, _ := r.s.Index.Item(key)
		p, _ := r.s.Props.Pending(ctx)
		t.Fatalf("a rebuild ran mid-rebase: %s decided %v, proposed %v", key, it.Decision != nil, p[key].ID != 0)
	case <-time.After(500 * time.Millisecond):
	}
	hold.Release()
	if err := <-rebuilt; err != nil {
		t.Fatal(err)
	}
	if it, ok := r.s.Index.Item(key); !ok || it.Decision == nil || it.Decision.Disposition != "keep" {
		t.Fatalf("after the rebase %s reads %+v, want decided keep", key, it.Decision)
	}
	if p, _ := r.s.Props.Pending(ctx); p[key].ID != 0 {
		t.Fatalf("the rule proposed %s, decided: %+v", key, p[key])
	}
	waitIdle(t, r)
	onMain(t, r)
	if l := r.remoteLog(t); !strings.Contains(l, "decide "+key) {
		t.Fatalf("the decision never reached the remote:\n%s", l)
	}
}

// TestCommitsDuringARealPushRebase: decides that land while serve's real
// push is rebasing onto a remote that's ahead wait for the rebase (never
// commit into it), then go out with the follow-up push: every decision on
// the remote, no error, the clone on main.
func TestCommitsDuringARealPushRebase(t *testing.T) {
	r := newRig(t)
	started, release := blockPush(t, r)
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"pr:schuettc/hail#3"}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	<-started
	remoteAhead(t, r, "notes/other.txt")
	hold := testgit.HoldRebase(t, r.App.Repo.Dir)
	release()
	hold.Started()

	const n = 4
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var res DecideResult
			c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{fmt.Sprintf("issue:schuettc/hail#%d", 4+i)}, "disposition": "keep"}, &res)
			if c != 200 || res.Decided != 1 || len(res.Errors) != 0 {
				errs <- fmt.Sprintf("decide %d: %d %+v", i, c, res)
			}
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("the decides committed while the push's rebase was held")
	case <-time.After(300 * time.Millisecond):
	}
	hold.Release()
	within(t, 10*time.Second, "the decides", func() { <-done })
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	eventually(t, "every decision on the remote", func() bool {
		l := r.remoteLog(t)
		for i := 0; i < n; i++ {
			if !strings.Contains(l, fmt.Sprintf("decide issue:schuettc/hail#%d ", 4+i)) {
				return false
			}
		}
		return strings.Contains(l, "decide pr:schuettc/hail#3 ") && strings.Contains(l, "update notes/other.txt")
	})
	waitIdle(t, r)
	onMain(t, r)
	if n := r.offlineQueued(t); n != 0 {
		t.Fatalf("offline_queued %d after the pushes, want 0", n)
	}
}

// TestShutdownMidRebaseLeavesNoRebase: serve stopping while its push
// rebases doesn't kill the rebase: it finishes (on a context shutdown
// doesn't cancel, bounded by a timeout), and casebook-data is on main with
// the decision kept.
func TestShutdownMidRebaseLeavesNoRebase(t *testing.T) {
	r := newRig(t)
	life, stop := context.WithCancel(context.Background())
	d, err := db.Open(life, filepath.Join(t.TempDir(), "second.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	s, err := New(life, r.App, d)
	if err != nil {
		t.Fatal(err)
	}
	r2 := &rig{Rig: r.Rig, s: s, url: serveURL(t, s)}
	remoteAhead(t, r, "notes/other.txt")
	hold := testgit.HoldRebase(t, r.App.Repo.Dir)
	if c := r2.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep"}, nil); c != 200 {
		t.Fatalf("decide %d", c)
	}
	hold.Started()
	stop()
	time.Sleep(200 * time.Millisecond) // a killed rebase would be gone by now
	hold.Release()
	within(t, 10*time.Second, "the pusher stopping", s.pushWG.Wait)
	onMain(t, r)
	if d, err := r.App.Repo.ReadDecision(mustKey(t, "issue:schuettc/hail#4")); err != nil || d == nil {
		t.Fatalf("the decision after shutdown: %v %v", d, err)
	}
	if b, err := r.App.Repo.ReadFile("notes/other.txt"); err != nil || len(b) == 0 {
		t.Fatalf("the rebase didn't finish onto the remote: %v", err)
	}
}
