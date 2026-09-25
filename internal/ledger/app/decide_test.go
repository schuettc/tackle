package app

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/ledger/item"
	"github.com/schuettc/tackle/internal/ledger/testgit"
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
