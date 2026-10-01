package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/observe"
	"github.com/schuettc/tackle/internal/casebook/store"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

// pruneFixture is a journal written before temp folders were skipped: this
// machine's day files mix real and temp lines (one unparseable line, one
// without a trailing newline), one day is all temp, and another machine's
// files hold temp lines prune must never touch. This machine's snapshot gets
// a temp clone; the other machine's keeps its own.
type pruneFixture struct {
	day, dayWant, allTemp, other, otherSnap string
}

func writePruneFixture(t *testing.T, r *rig) pruneFixture {
	t.Helper()
	real := `{"v":1,"ts":"2026-09-24T10:00:00Z","src":"git-hook","hook":"post-commit","cwd":"` + r.clone + `","repo":"schuettc/hail","machine":"mbp"}`
	agent := `{"v":1,"ts":"2026-09-24T10:00:01Z","src":"claude","cwd":"/tmp/x","actions":[{"tool":"gh","verb":"pr merge","repo":"schuettc/hail","number":3}],"machine":"mbp"}`
	probe := `{"v":1,"ts":"2026-09-24T10:00:02Z","src":"git-hook","hook":"reference-transaction","args":["committed"],"cwd":"` + r.clone + `","git_dir":"/private/var/folders/92/ab/T/casebook-probe-48603/data/repo/.git","repo":"schuettc/hail","machine":"mbp"}`
	copier := `{"v":1,"ts":"2026-09-24T10:00:03Z","src":"git-hook","hook":"post-checkout","cwd":"/var/folders/92/ab/T/copier._vcs.clone.k3j2x9","machine":"mbp"}`
	scratch := `{"v":1,"ts":"2026-09-24T10:00:04Z","src":"git-hook","hook":"post-commit","cwd":"/private/tmp/scratch-1/clone","machine":"mbp"}`
	notTmp := `{"v":1,"ts":"2026-09-24T10:00:05Z","src":"git-hook","hook":"post-commit","cwd":"/tmpfoo/clone","machine":"mbp"}`
	garbage := `not json at all /tmp/x`
	f := pruneFixture{
		day: real + "\n" + probe + "\n" + agent + "\n" + copier + "\n" + garbage + "\n" + scratch + "\n\n" + notTmp + "\n" + copier,
		// Every line but the temp ones, byte for byte and in order; the last
		// kept line keeps its own (missing) terminator only if it was last.
		dayWant:   real + "\n" + agent + "\n" + garbage + "\n" + "\n" + notTmp + "\n",
		allTemp:   copier + "\n" + scratch + "\n",
		other:     strings.ReplaceAll(copier+"\n"+real+"\n", `"machine":"mbp"`, `"machine":"other"`),
		otherSnap: `{"version":1,"machine":"other","roots":["/x"],"clones":[{"path":"/tmp/other-clone"}]}` + "\n",
	}
	files := map[string]string{
		"journal/mbp/2026/09-24.jsonl":   f.day,
		"journal/mbp/2026/09-23.jsonl":   f.allTemp,
		"journal/other/2026/09-24.jsonl": f.other,
		"machines/other.json":            f.otherSnap,
	}
	for rel, body := range files {
		p := filepath.Join(r.app.Repo.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// This machine's snapshot gains a temp clone (outside the configured root).
	snap, ok, err := r.app.MachineSnapshot()
	if err != nil || !ok {
		t.Fatalf("snapshot %v %v", ok, err)
	}
	snap.Clones = append(snap.Clones, observe.Clone{Path: "/private/tmp/scratch-1/clone", Repo: "schuettc/hail"})
	b, _ := observe.EncodeSnapshot(snap)
	if err := os.WriteFile(filepath.Join(r.app.Repo.Dir, "machines", "mbp.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.app.Repo.Commit(ctx, "fixture: a journal from before temp folders were skipped"); err != nil {
		t.Fatal(err)
	}
	return f
}

func syncedRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	if _, err := r.app.Sync(ctx, SyncOptions{NoGitHub: true, NoPush: true}); err != nil {
		t.Fatal(err)
	}
	return r
}

func head(t *testing.T, r *rig) string {
	t.Helper()
	return testgit.Git(t, r.app.Repo.Dir, "rev-parse", "HEAD")
}

func readRepo(t *testing.T, r *rig, rel string) string {
	t.Helper()
	b, err := r.app.Repo.ReadFile(rel)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func TestPruneTempDryRunChangesNothing(t *testing.T) {
	r := syncedRig(t)
	f := writePruneFixture(t, r)
	before := head(t, r)
	rep, err := r.app.PruneTemp(ctx, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied || rep.Removed != 6 || rep.Kept != 5 || len(rep.Files) != 2 {
		t.Fatalf("dry run report %+v", rep)
	}
	byPath := map[string]PruneFile{}
	for _, pf := range rep.Files {
		byPath[pf.Path] = pf
	}
	if pf := byPath["journal/mbp/2026/09-24.jsonl"]; pf.Removed != 4 || pf.Kept != 5 || pf.Delete {
		t.Errorf("09-24: %+v", pf)
	}
	if pf := byPath["journal/mbp/2026/09-23.jsonl"]; pf.Removed != 2 || pf.Kept != 0 || !pf.Delete {
		t.Errorf("09-23: %+v", pf)
	}
	want := map[string]int{"/var/folders/92/ab/T/casebook-probe-*": 1, "/var/folders/92/ab/T/copier._vcs.clone.*": 3, "/tmp/scratch-*": 2}
	for _, p := range rep.Prefixes {
		if want[p.Prefix] != p.Lines {
			t.Errorf("prefix %s: %d lines, want %d (all: %+v)", p.Prefix, p.Lines, want[p.Prefix], rep.Prefixes)
		}
	}
	if len(rep.Clones) != 1 || rep.Clones[0] != "/private/tmp/scratch-1/clone" {
		t.Errorf("temp clones %v", rep.Clones)
	}
	if head(t, r) != before || readRepo(t, r, "journal/mbp/2026/09-24.jsonl") != f.day {
		t.Error("the dry run changed the repo")
	}
}

func TestPruneTempApply(t *testing.T) {
	r := syncedRig(t)
	f := writePruneFixture(t, r)
	rep, err := r.app.PruneTemp(ctx, PruneOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || !rep.Committed || rep.Removed != 6 || !rep.Pushed {
		t.Fatalf("apply report %+v", rep)
	}
	if got := readRepo(t, r, "journal/mbp/2026/09-24.jsonl"); got != f.dayWant {
		t.Errorf("09-24 after prune:\n%q\nwant\n%q", got, f.dayWant)
	}
	if _, err := r.app.Repo.ReadFile("journal/mbp/2026/09-23.jsonl"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an all-temp day file is still there: %v", err)
	}
	// Another machine's files are untouched, byte for byte.
	if readRepo(t, r, "journal/other/2026/09-24.jsonl") != f.other || readRepo(t, r, "machines/other.json") != f.otherSnap {
		t.Error("prune touched another machine's files")
	}
	// This machine's snapshot loses the temp clone and keeps the real one.
	var snap observe.Snapshot
	if err := json.Unmarshal([]byte(readRepo(t, r, "machines/mbp.json")), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Clones) != 1 || snap.Clones[0].Path != r.clone {
		t.Errorf("snapshot clones after prune: %+v", snap.Clones)
	}
	if strings.Contains(readRepo(t, r, "MACHINES.md"), "/private/tmp/scratch-1/clone") {
		t.Error("MACHINES.md still lists the temp clone")
	}
	subject := testgit.Git(t, r.app.Repo.Dir, "log", "-1", "--format=%s")
	if !strings.HasPrefix(subject, "prune mbp: 6 temp-folder journal events") {
		t.Errorf("commit subject %q", subject)
	}
	if got := testgit.Git(t, r.remote, "log", "-1", "--format=%s", "main"); got != subject {
		t.Errorf("remote head %q, want the prune pushed", got)
	}
	// The old lines stay in history: nothing is rewritten.
	if old := testgit.Git(t, r.app.Repo.Dir, "show", "HEAD~1:journal/mbp/2026/09-24.jsonl"); old != strings.TrimRight(f.day, "\n") {
		t.Error("history lost the pruned lines")
	}
	// Idempotent: a second apply removes nothing and commits nothing.
	before := head(t, r)
	rep, err = r.app.PruneTemp(ctx, PruneOptions{Apply: true, NoPush: true})
	if err != nil || rep.Removed != 0 || rep.Committed || len(rep.Clones) != 0 || head(t, r) != before {
		t.Errorf("second apply: %+v %v", rep, err)
	}
}

func TestPruneTempApplyAbortsWhenLocked(t *testing.T) {
	r := syncedRig(t)
	f := writePruneFixture(t, r)
	before := head(t, r)

	// casebook-data's lock, held by another process.
	lf, err := os.OpenFile(r.app.Repo.LockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = r.app.PruneTemp(ctx, PruneOptions{Apply: true, LockWait: 100 * time.Millisecond})
	if !errors.Is(err, store.ErrLocked) || time.Since(start) > 5*time.Second {
		t.Errorf("apply under a held lock: %v after %s", err, time.Since(start))
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
	_ = lf.Close()

	// This machine's sync lock, held by a running sync.
	unlock, err := LockSync()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.app.PruneTemp(ctx, PruneOptions{Apply: true}); !errors.Is(err, ErrSyncBusy) {
		t.Errorf("apply during a sync: %v", err)
	}
	unlock()

	if head(t, r) != before || readRepo(t, r, "journal/mbp/2026/09-24.jsonl") != f.day {
		t.Error("an aborted apply changed the repo")
	}
	if out := testgit.Git(t, r.app.Repo.Dir, "status", "--porcelain"); out != "" {
		t.Errorf("an aborted apply left changes:\n%s", out)
	}
	_ = config.SpoolDir() // the rig's spool is untouched by prune
}

func TestGeneralize(t *testing.T) {
	for in, want := range map[string]string{
		"casebook-probe-48603":       "casebook-probe-*",
		"casebook-probe-10551-x":     "casebook-probe-*",
		"tmp1jyhsb7j":                "tmp*",
		"tmp.AbCdEf":                 "tmp.*",
		"copier._vcs.clone.k3j2x9":   "copier._vcs.clone.*",
		"copier._vcs.clone.v_abc":    "copier._vcs.clone.*",
		"copier._vcs.clone._abc1":    "copier._vcs.clone._*",
		"TestEvidenceExitCodes2_bad": "TestEvidenceExitCodes*",
		"pytest-of-courtschuett":     "pytest-of-courtschuett",
		"bridge-rebase":              "bridge-rebase",
		"muda-audit-export":          "muda-audit-export",
	} {
		if got := generalize(in); got != want {
			t.Errorf("generalize(%q) = %q, want %q", in, got, want)
		}
	}
}
