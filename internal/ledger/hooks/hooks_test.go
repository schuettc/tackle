package hooks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/ledger/config"
	"github.com/schuettc/tackle/internal/ledger/spool"
	"github.com/schuettc/tackle/internal/ledger/testgit"
)

var ctx = context.Background()

type rig struct {
	opts Options
	log  string // what the stub "ledger" received
	repo string
}

func setup(t *testing.T) rig {
	t.Helper()
	testgit.Env(t)
	base := t.TempDir()
	log := filepath.Join(base, "stub.log")
	stub := filepath.Join(base, "ledger-stub")
	script := "#!/bin/sh\n{ echo \"ARGS $*\"; cat; } >>'" + log + "'\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Dir: filepath.Join(base, "hooks"), StatePath: filepath.Join(base, "state", "hooks.json"), Binary: stub}
	return rig{opts: o, log: log, repo: testgit.NewRepo(t)}
}

func (r rig) install(t *testing.T) {
	t.Helper()
	if err := Install(ctx, r.opts); err != nil {
		t.Fatal(err)
	}
}

func (r rig) stubLog(t *testing.T) string {
	b, _ := os.ReadFile(r.log)
	return string(b)
}

func repoHook(t *testing.T, repo, name, body string) {
	t.Helper()
	p := filepath.Join(repo, ".git", "hooks", name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func gitErr(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func TestShimChainsBlockingRepoHook(t *testing.T) {
	r := setup(t)
	r.install(t)
	repoHook(t, r.repo, "pre-commit", "exit 1")
	os.WriteFile(filepath.Join(r.repo, "f"), []byte("x"), 0o644)
	testgit.Git(t, r.repo, "add", "f")
	if gitErr(r.repo, "commit", "-q", "-m", "blocked") == nil {
		t.Fatal("repo pre-commit exit 1 did not block the commit")
	}
	repoHook(t, r.repo, "pre-commit", "exit 0")
	repoHook(t, r.repo, "commit-msg", "echo seen > \"$(dirname \"$1\")/commit-msg-ran\"")
	testgit.Git(t, r.repo, "commit", "-q", "-m", "ok")
	if _, err := os.Stat(filepath.Join(r.repo, ".git", "commit-msg-ran")); err != nil {
		t.Error("repo commit-msg hook did not run")
	}
	if !strings.Contains(r.stubLog(t), "ARGS hook post-commit") {
		t.Errorf("post-commit not recorded:\n%s", r.stubLog(t))
	}
}

func TestShimPrePushStdinReachesBoth(t *testing.T) {
	r := setup(t)
	remote := testgit.NewBare(t)
	testgit.Git(t, r.repo, "remote", "add", "origin", remote)
	r.install(t)
	got := filepath.Join(t.TempDir(), "repo-hook-stdin")
	repoHook(t, r.repo, "pre-push", "cat > '"+got+"'")
	testgit.Git(t, r.repo, "push", "-q", "origin", "main")
	head := testgit.Git(t, r.repo, "rev-parse", "HEAD")
	b, _ := os.ReadFile(got)
	if !strings.Contains(string(b), "refs/heads/main "+head) {
		t.Errorf("repo pre-push stdin = %q", b)
	}
	log := r.stubLog(t)
	if !strings.Contains(log, "ARGS hook pre-push origin "+remote) || !strings.Contains(log, "refs/heads/main "+head) {
		t.Errorf("ledger pre-push record:\n%s", log)
	}
}

func TestShimPassthroughPreservesExit(t *testing.T) {
	r := setup(t)
	r.install(t)
	repoHook(t, r.repo, "pre-push", "cat >/dev/null; exit 7")
	cmd := exec.Command("sh", filepath.Join(r.opts.Dir, "pre-push"), "origin", "/nowhere")
	cmd.Dir = r.repo
	cmd.Stdin = strings.NewReader("refs/heads/main 1 refs/heads/main 0\n")
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 7 {
		t.Fatalf("shim exit = %v, want 7", err)
	}
	// No repo hook at all: exit 0.
	os.Remove(filepath.Join(r.repo, ".git", "hooks", "pre-push"))
	cmd = exec.Command("sh", filepath.Join(r.opts.Dir, "pre-push"), "origin", "/nowhere")
	cmd.Dir = r.repo
	cmd.Stdin = strings.NewReader("x\n")
	if err := cmd.Run(); err != nil {
		t.Fatalf("no repo hook: %v", err)
	}
}

func TestReferenceTransactionRecordsCommittedOnly(t *testing.T) {
	r := setup(t)
	r.install(t)
	testgit.Commit(t, r.repo, "g", "y")
	log := r.stubLog(t)
	if !strings.Contains(log, "ARGS hook reference-transaction committed") {
		t.Errorf("committed not recorded:\n%s", log)
	}
	if strings.Contains(log, "reference-transaction prepared") {
		t.Errorf("prepared recorded:\n%s", log)
	}
}

func TestLedgerDisableSkipsRecordingOnly(t *testing.T) {
	r := setup(t)
	r.install(t)
	marker := filepath.Join(t.TempDir(), "ran")
	repoHook(t, r.repo, "post-commit", "touch '"+marker+"'")
	t.Setenv("LEDGER_DISABLE", "1")
	testgit.Commit(t, r.repo, "h", "z")
	if r.stubLog(t) != "" {
		t.Errorf("recorded despite LEDGER_DISABLE:\n%s", r.stubLog(t))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("repo hook skipped under LEDGER_DISABLE")
	}
}

func TestShimChainsPreviousHooksPath(t *testing.T) {
	r := setup(t)
	prev := t.TempDir()
	os.WriteFile(filepath.Join(prev, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	testgit.Git(t, r.repo, "config", "--global", "core.hooksPath", prev)
	r.install(t)
	r.install(t) // idempotent: must not record itself as the previous path
	st, err := GetStatus(ctx, r.opts)
	if err != nil || !st.Installed || st.Prev != prev || len(st.Missing) != 0 || !st.BinaryOK {
		t.Fatalf("status %+v %v", st, err)
	}
	os.WriteFile(filepath.Join(r.repo, "f"), []byte("x"), 0o644)
	testgit.Git(t, r.repo, "add", "f")
	if gitErr(r.repo, "commit", "-q", "-m", "blocked") == nil {
		t.Fatal("previous global pre-commit no longer runs")
	}
	if err := Uninstall(ctx, r.opts); err != nil {
		t.Fatal(err)
	}
	if got := testgit.Git(t, r.repo, "config", "--global", "core.hooksPath"); got != prev {
		t.Errorf("hooksPath after uninstall = %q, want %q", got, prev)
	}
	if _, err := os.Stat(r.opts.Dir); !os.IsNotExist(err) {
		t.Error("shim dir left behind")
	}
}

func TestUninstallUnsetsWhenNoPrevious(t *testing.T) {
	r := setup(t)
	r.install(t)
	if err := Uninstall(ctx, r.opts); err != nil {
		t.Fatal(err)
	}
	if gitErr(r.repo, "config", "--global", "core.hooksPath") == nil {
		t.Error("core.hooksPath still set")
	}
}

func TestShimsAreValidPOSIX(t *testing.T) {
	shells := []string{"sh"}
	for _, s := range []string{"dash", "bash"} {
		if _, err := exec.LookPath(s); err == nil {
			shells = append(shells, s)
		}
	}
	for _, name := range Names {
		src := Shim(name, "/opt/it's/ledger", "/prev dir")
		for _, sh := range shells {
			cmd := exec.Command(sh, "-n")
			cmd.Stdin = strings.NewReader(string(src))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s -n %s: %v\n%s", sh, name, err, out)
			}
		}
	}
	for _, skip := range []string{"push-to-checkout", "proc-receive", "fsmonitor-watchman", "p4-pre-submit"} {
		for _, n := range Names {
			if n == skip {
				t.Errorf("%s must not get a shim", skip)
			}
		}
	}
}

func TestHandleWritesRedactedEvent(t *testing.T) {
	testgit.Env(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "cc-1")
	dir := filepath.Join(t.TempDir(), "spool")
	Handle("pre-push", []string{"origin", "https://x-access-token:SECRET@github.com/a/b.git"},
		strings.NewReader("refs/heads/main aaa refs/heads/main bbb\n"), dir, time.Unix(100, 0))
	Handle("reference-transaction", []string{"prepared"}, strings.NewReader("a b refs/heads/x\n"), dir, time.Unix(101, 0))
	b, err := spool.Drain(dir)
	if err != nil || len(b.Events) != 1 {
		t.Fatalf("got %+v %v", b, err)
	}
	ev := b.Events[0]
	if ev.Src != "git-hook" || ev.Hook != "pre-push" || ev.Args[1] != "https://github.com/a/b.git" || ev.ClaudeID != "cc-1" {
		t.Errorf("event %+v", ev)
	}
	if len(ev.Stdin) != 1 || ev.Stdin[0][3] != "bbb" {
		t.Errorf("stdin %v", ev.Stdin)
	}
}

func TestMainNeverFails(t *testing.T) {
	testgit.Env(t)
	t.Setenv("LEDGER_HOME", filepath.Join(t.TempDir(), "missing", "deeper"))
	if Main(nil, strings.NewReader("")) != 0 || Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	if _, err := os.Stat(config.SpoolDir()); err == nil {
		t.Error("uninitialized machine got a spool")
	}
	// Initialized: records.
	os.MkdirAll(filepath.Dir(config.Path()), 0o700)
	os.WriteFile(config.Path(), []byte("machine = \"m\"\n"), 0o600)
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	b, err := spool.Drain(config.SpoolDir())
	if err != nil || len(b.Events) != 1 {
		t.Fatalf("initialized machine: %+v %v", b, err)
	}
	b.Close()
	t.Setenv("LEDGER_INTERNAL", "1")
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
}
