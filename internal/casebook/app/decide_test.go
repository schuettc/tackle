package app

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/store"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

func TestActor(t *testing.T) {
	r := newRig(t)
	if got := r.app.Actor(); got != "schuettc" {
		t.Errorf("human actor %q", got)
	}
	t.Setenv("AGENT_SESSION_ID", "pi-7")
	if got := r.app.Actor(); got != "pi:pi-7" {
		t.Errorf("pi actor %q", got)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "cc-3")
	if got := r.app.Actor(); got != "claude:cc-3" {
		t.Errorf("claude inside pi (no marker) %q", got)
	}
	t.Setenv("AGENT_SESSION_CHILD", "1")
	if got := r.app.Actor(); got != "pi:pi-7" {
		t.Errorf("bridge child %q", got)
	}
}

func TestDecidePushesAndValidates(t *testing.T) {
	r := newRig(t)
	d, pushed, err := r.app.Decide(ctx, "repo:Schuettc/Hail", "keep", DecideOptions{Note: "active"})
	if err != nil || !pushed || d.DecidedBy != "schuettc" || !d.DecidedAt.Equal(r.now) {
		t.Fatalf("got %+v %v %v", d, pushed, err)
	}
	if got := testgit.Git(t, r.remote, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "decide repo:schuettc/hail → keep") {
		t.Errorf("remote head %q", got)
	}
	for _, bad := range [][2]string{{"repo:a/b", "merge"}, {"nonsense", "keep"}, {"pr:a/b#1", "wait"}} {
		if _, _, err := r.app.Decide(ctx, bad[0], bad[1], DecideOptions{}); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, _, err := r.app.Decide(ctx, "pr:a/b#1", "wait", DecideOptions{Until: "date(2026-10-01)", By: "court"}); err != nil {
		t.Error(err)
	}
}

func TestTriageRoundTrip(t *testing.T) {
	r := newRig(t)
	if _, err := r.app.Sync(ctx, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	res, _, _ := r.app.Build(ctx)
	f := string(TriageFile(res.Attention(), r.now))
	for _, want := range []string{`key = "pr:schuettc/hail#3"`, `key = "branch:schuettc/hail@feat/client"`, "# allowed: keep close merge wait watch ignore", `disposition = ""`} {
		if !strings.Contains(f, want) {
			t.Errorf("triage file lacks %q:\n%s", want, f)
		}
	}
	i := strings.Index(f, `key = "pr:schuettc/hail#3"`)
	filled := f[:i] + strings.Replace(f[i:], `disposition = ""`, `disposition = "close"`, 1)
	entries, err := ParseTriage([]byte(filled))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries %+v %v", entries, err)
	}
	n, errs, pushed := r.app.DecideBatch(ctx, append(entries, TriageEntry{Key: "repo:a/b", Disposition: "merge"}), DecideOptions{})
	if n != 1 || len(errs) != 1 || !pushed {
		t.Fatalf("applied %d errs %v pushed %v", n, errs, pushed)
	}
	if d, _ := r.app.Repo.ReadDecision(item.PRKey("schuettc/hail", 3)); d == nil || d.Disposition != item.Close {
		t.Errorf("decision %+v", d)
	}
	if _, err := ParseTriage([]byte("[[item]]\nkey = \"repo:a/b\"\ndispositon = \"keep\"\n")); err == nil {
		t.Error("typo accepted")
	}
}

func TestDecideRecordsProposal(t *testing.T) {
	r := newRig(t)
	d, _, err := r.app.Decide(ctx, "repo:schuettc/hail", "keep", DecideOptions{By: "court", ProposedBy: "pi:s-1", Rule: "active-repos"})
	if err != nil || d.ProposedBy != "pi:s-1" || d.Rule != "active-repos" {
		t.Fatalf("got %+v %v", d, err)
	}
	got, _ := r.app.Repo.ReadDecision(item.RepoKey("schuettc/hail"))
	if got == nil || got.ProposedBy != "pi:s-1" || got.Rule != "active-repos" {
		t.Fatalf("stored %+v", got)
	}
}

// TestPushHoldsTheSyncLock: Push never runs beside a sync: while another
// holder has the machine's sync lock it pushes nothing and says ErrSyncBusy;
// once the lock is free it pushes. An unreachable remote is store.ErrOffline.
func TestPushHoldsTheSyncLock(t *testing.T) {
	r := newRig(t)
	if _, _, err := r.app.Decide(ctx, "repo:schuettc/hail", "keep", DecideOptions{NoPush: true}); err != nil {
		t.Fatal(err)
	}
	unlock, err := LockSync()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.app.Push(ctx); !errors.Is(err, ErrSyncBusy) {
		t.Fatalf("push under a held sync lock: %v, want ErrSyncBusy", err)
	}
	if got := testgit.Git(t, r.remote, "log", "-1", "--format=%s", "main"); strings.Contains(got, "decide repo:schuettc/hail") {
		t.Fatal("pushed while the sync lock was held")
	}
	unlock()
	if err := r.app.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if got := testgit.Git(t, r.remote, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "decide repo:schuettc/hail → keep") {
		t.Fatalf("remote head %q", got)
	}
	if err := os.Rename(r.remote, r.remote+".away"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.app.Decide(ctx, "pr:schuettc/hail#3", "keep", DecideOptions{NoPush: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.app.Push(ctx); !errors.Is(err, store.ErrOffline) {
		t.Fatalf("push to a missing remote: %v, want store.ErrOffline", err)
	}
}
