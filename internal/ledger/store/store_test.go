package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/ledger/item"
	"github.com/schuettc/tackle/internal/ledger/testgit"
)

var ctx = context.Background()

func newStore(t *testing.T) (*Repo, string) {
	t.Helper()
	testgit.Env(t)
	remote := testgit.NewBare(t)
	r, err := Init(ctx, filepath.Join(t.TempDir(), "ledger"), remote)
	if err != nil {
		t.Fatal(err)
	}
	return r, remote
}

func dec(d item.Disposition, by string, at time.Time) item.Decision {
	return item.Decision{Disposition: d, DecidedBy: by, DecidedAt: at}
}

func TestInitBootstrapsEmptyRemote(t *testing.T) {
	r, remote := newStore(t)
	for _, f := range []string{"ledger.toml", "policy.toml", "README.md"} {
		if _, err := os.Stat(filepath.Join(r.Dir, f)); err != nil {
			t.Errorf("%s missing: %v", f, err)
		}
	}
	if got := testgit.Git(t, remote, "log", "--format=%s", "main"); !strings.Contains(got, "init ledger") {
		t.Errorf("remote log %q", got)
	}
	// A second machine's init clones the existing ledger.
	r2, err := Init(ctx, filepath.Join(t.TempDir(), "ledger2"), remote)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := r2.Policy(); err != nil || p != item.DefaultPolicy() {
		t.Fatalf("policy %+v %v", p, err)
	}
}

func TestOpenRejectsNewerFormat(t *testing.T) {
	r, _ := newStore(t)
	if err := os.WriteFile(filepath.Join(r.Dir, "ledger.toml"), []byte("format_version = 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(r.Dir); err == nil || !strings.Contains(err.Error(), "format_version 99") {
		t.Fatalf("got %v", err)
	}
}

func TestDecideWritesFileAndCommit(t *testing.T) {
	r, _ := newStore(t)
	k, _ := item.ParseKey("branch:schuettc/hail@feat/client")
	d := dec(item.Keep, "court", time.Now())
	d.Note = "wip client"
	if err := r.Decide(ctx, k, d); err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDecision(k)
	if err != nil || got == nil || got.Disposition != item.Keep || got.Note != "wip client" {
		t.Fatalf("got %+v %v", got, err)
	}
	subj := testgit.Git(t, r.Dir, "log", "-1", "--format=%s")
	if subj != `decide branch:schuettc/hail@feat/client → keep ("wip client") by court` {
		t.Errorf("subject %q", subj)
	}
	if err := r.Decide(ctx, k, dec(item.Archive, "court", time.Now())); err == nil {
		t.Error("archive accepted for a branch")
	}
	all, errs := r.Decisions()
	if len(errs) != 0 || len(all) != 1 || all[k.String()].Note != "wip client" {
		t.Fatalf("decisions %v %v", all, errs)
	}
	log, err := r.Log(ctx, k.File())
	if err != nil || len(log) != 1 || !strings.HasPrefix(log[0].Subject, "decide ") {
		t.Fatalf("log %+v %v", log, err)
	}
}

func TestReadDecisionAbsent(t *testing.T) {
	r, _ := newStore(t)
	d, err := r.ReadDecision(item.RepoKey("a/b"))
	if d != nil || err != nil {
		t.Fatalf("got %v %v", d, err)
	}
}

func TestWriteFileReportsChange(t *testing.T) {
	r, _ := newStore(t)
	for i, want := range []bool{true, false} {
		changed, err := r.WriteFile("machines/mbp.json", []byte("{}\n"))
		if err != nil || changed != want {
			t.Fatalf("write %d: changed=%v err=%v", i, changed, err)
		}
	}
	if err := r.AppendFile("journal/mbp/2026/09-24.jsonl", []byte("{\"v\":1}\n")); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendFile("journal/mbp/2026/09-24.jsonl", []byte("{\"v\":1}\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := r.ReadFile("journal/mbp/2026/09-24.jsonl")
	if strings.Count(string(b), "\n") != 2 {
		t.Fatalf("append: %q", b)
	}
	if ok, err := r.Commit(ctx, "sync mbp"); !ok || err != nil {
		t.Fatalf("commit %v %v", ok, err)
	}
	if ok, err := r.Commit(ctx, "sync mbp"); ok || err != nil {
		t.Fatalf("empty commit %v %v", ok, err)
	}
	files, err := r.Glob("journal/*/*/*.jsonl")
	if err != nil || len(files) != 1 || files[0] != "journal/mbp/2026/09-24.jsonl" {
		t.Fatalf("glob %v %v", files, err)
	}
}

func TestValidateReportsBadFiles(t *testing.T) {
	r, _ := newStore(t)
	if errs := r.Validate(); len(errs) != 0 {
		t.Fatalf("fresh ledger invalid: %v", errs)
	}
	bad := map[string]string{
		"items/repo/a/b.toml":       "disposition = \"merge\"\ndecided_by = \"c\"\ndecided_at = 2026-09-24T00:00:00Z\n",
		"items/pr/a/b/x.toml":       "disposition = \"keep\"\n",
		"items/issue/a/b/2.toml":    "not toml",
		"items/branch/a/b/wip.toml": "disposition = \"wait\"\ndecided_by = \"c\"\ndecided_at = 2026-09-24T00:00:00Z\n",
	}
	for f, c := range bad {
		if _, err := r.WriteFile(f, []byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.WriteFile("policy.toml", []byte("unpushed_days = -1\n")); err != nil {
		t.Fatal(err)
	}
	errs := r.Validate()
	if len(errs) != 5 {
		t.Fatalf("want 5 errors, got %d: %v", len(errs), errs)
	}
	for _, e := range errs {
		if !strings.Contains(e.Error(), ".toml") {
			t.Errorf("error lacks the file: %v", e)
		}
	}
}
