package hooks

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/spool"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

var ctx = context.Background()

type rig struct {
	opts Options
	log  string // what the stub "casebook" received
	repo string
}

func setup(t *testing.T) rig {
	t.Helper()
	testgit.Env(t)
	base := t.TempDir()
	log := filepath.Join(base, "stub.log")
	stub := filepath.Join(base, "casebook-stub")
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
	_ = os.WriteFile(filepath.Join(r.repo, "f"), []byte("x"), 0o644)
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

// adoptRepo sets a local core.hooksPath of .husky/_ (relative) with a
// pre-commit and a post-commit, and returns the hooks dir path.
func huskyRepo(t *testing.T, repo, precommit string) string {
	t.Helper()
	hooksDir := filepath.Join(repo, ".husky", "_")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte("#!/bin/sh\n"+precommit+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Git(t, repo, "config", "--local", "core.hooksPath", ".husky/_")
	return hooksDir
}

func TestAdoptChainsLocalHooksPath(t *testing.T) {
	r := setup(t)
	marker := filepath.Join(t.TempDir(), "post-commit-ran")
	hooksDir := huskyRepo(t, r.repo, "exit 1")
	_ = os.WriteFile(filepath.Join(hooksDir, "post-commit"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o755)
	r.install(t)
	res, err := Adopt(ctx, r.opts, r.repo)
	if err != nil || res.Already {
		t.Fatalf("Adopt = %+v %v", res, err)
	}
	_ = os.WriteFile(filepath.Join(r.repo, "f"), []byte("x"), 0o644)
	testgit.Git(t, r.repo, "add", "f")
	if gitErr(r.repo, "commit", "-q", "-m", "blocked") == nil {
		t.Fatal("adopted repo's pre-commit (exit 1) did not block the commit")
	}
	_ = os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	testgit.Git(t, r.repo, "commit", "-q", "-m", "ok")
	if _, err := os.Stat(marker); err != nil {
		t.Error("repo post-commit did not run after adopt")
	}
	if !strings.Contains(r.stubLog(t), "ARGS hook post-commit") {
		t.Errorf("post-commit not recorded:\n%s", r.stubLog(t))
	}
	if got := testgit.Git(t, r.repo, "config", "--local", "core.hooksPath"); got != r.opts.Dir {
		t.Errorf("local core.hooksPath = %q, want %q", got, r.opts.Dir)
	}
	if got := testgit.Git(t, r.repo, "config", "--local", "casebook.prevHooksPath"); got != ".husky/_" {
		t.Errorf("casebook.prevHooksPath = %q, want .husky/_", got)
	}
}

func TestAdoptIdempotentAndRelease(t *testing.T) {
	r := setup(t)
	huskyRepo(t, r.repo, "exit 0")
	r.install(t)
	if _, err := Adopt(ctx, r.opts, r.repo); err != nil {
		t.Fatal(err)
	}
	res, err := Adopt(ctx, r.opts, r.repo)
	if err != nil || !res.Already {
		t.Fatalf("second Adopt = %+v %v, want Already", res, err)
	}
	st, _ := GetStatus(ctx, r.opts)
	if len(st.Adopted) != 1 {
		t.Fatalf("Adopted = %v, want one entry", st.Adopted)
	}
	if err := Release(ctx, r.opts, r.repo); err != nil {
		t.Fatal(err)
	}
	if got := testgit.Git(t, r.repo, "config", "--local", "core.hooksPath"); got != ".husky/_" {
		t.Errorf("core.hooksPath after release = %q, want .husky/_", got)
	}
	if gitErr(r.repo, "config", "--local", "casebook.prevHooksPath") == nil {
		t.Error("casebook.prevHooksPath still set after release")
	}
	st, _ = GetStatus(ctx, r.opts)
	if len(st.Adopted) != 0 {
		t.Errorf("Adopted after release = %v, want empty", st.Adopted)
	}
}

func TestAdoptRefusals(t *testing.T) {
	r := setup(t)
	huskyRepo(t, r.repo, "exit 0")
	if _, err := Adopt(ctx, r.opts, r.repo); err == nil || !strings.Contains(err.Error(), "install the global hooks first") {
		t.Fatalf("Adopt without install = %v", err)
	}
	r2 := setup(t)
	r2.install(t)
	if _, err := Adopt(ctx, r2.opts, r2.repo); err == nil || !strings.Contains(err.Error(), "no local core.hooksPath") {
		t.Fatalf("Adopt with no local hooksPath = %v", err)
	}
}

func TestUninstallReleasesAdopted(t *testing.T) {
	r := setup(t)
	huskyRepo(t, r.repo, "exit 0")
	r.install(t)
	if _, err := Adopt(ctx, r.opts, r.repo); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(ctx, r.opts); err != nil {
		t.Fatal(err)
	}
	if got := testgit.Git(t, r.repo, "config", "--local", "core.hooksPath"); got != ".husky/_" {
		t.Errorf("core.hooksPath after uninstall = %q, want .husky/_", got)
	}
	if gitErr(r.repo, "config", "--global", "core.hooksPath") == nil {
		t.Error("global core.hooksPath still set after uninstall")
	}
}

func TestReleaseLeavesRepointedRepo(t *testing.T) {
	r := setup(t)
	huskyRepo(t, r.repo, "exit 0")
	r.install(t)
	if _, err := Adopt(ctx, r.opts, r.repo); err != nil {
		t.Fatal(err)
	}
	testgit.Git(t, r.repo, "config", "--local", "core.hooksPath", "other/")
	if err := Release(ctx, r.opts, r.repo); err != nil {
		t.Fatal(err)
	}
	if got := testgit.Git(t, r.repo, "config", "--local", "core.hooksPath"); got != "other/" {
		t.Errorf("core.hooksPath after release of repointed repo = %q, want other/", got)
	}
	if gitErr(r.repo, "config", "--local", "casebook.prevHooksPath") == nil {
		t.Error("casebook.prevHooksPath still set after release")
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
		t.Errorf("casebook pre-push record:\n%s", log)
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
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("shim exit = %v, want 7", err)
	}
	// No repo hook at all: exit 0.
	_ = os.Remove(filepath.Join(r.repo, ".git", "hooks", "pre-push"))
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

func TestCasebookDisableSkipsRecordingOnly(t *testing.T) {
	r := setup(t)
	r.install(t)
	marker := filepath.Join(t.TempDir(), "ran")
	repoHook(t, r.repo, "post-commit", "touch '"+marker+"'")
	t.Setenv("CASEBOOK_DISABLE", "1")
	testgit.Commit(t, r.repo, "h", "z")
	if r.stubLog(t) != "" {
		t.Errorf("recorded despite CASEBOOK_DISABLE:\n%s", r.stubLog(t))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("repo hook skipped under CASEBOOK_DISABLE")
	}
}

func TestShimChainsPreviousHooksPath(t *testing.T) {
	r := setup(t)
	prev := t.TempDir()
	_ = os.WriteFile(filepath.Join(prev, "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	testgit.Git(t, r.repo, "config", "--global", "core.hooksPath", prev)
	r.install(t)
	r.install(t) // idempotent: must not record itself as the previous path
	st, err := GetStatus(ctx, r.opts)
	if err != nil || !st.Installed || st.Prev != prev || len(st.Missing) != 0 || !st.BinaryOK {
		t.Fatalf("status %+v %v", st, err)
	}
	_ = os.WriteFile(filepath.Join(r.repo, "f"), []byte("x"), 0o644)
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
		src := Shim(name, "/opt/it's/casebook", "/prev dir")
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
		strings.NewReader("refs/heads/main aaa refs/heads/main bbb\n"), dir, time.Unix(100, 0), nil)
	Handle("reference-transaction", []string{"prepared"}, strings.NewReader("a b refs/heads/x\n"), dir, time.Unix(101, 0), nil)
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
	t.Setenv("CASEBOOK_HOME", filepath.Join(t.TempDir(), "missing", "deeper"))
	if Main(nil, strings.NewReader("")) != 0 || Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	if _, err := os.Stat(config.SpoolDir()); err == nil {
		t.Error("uninitialized machine got a spool")
	}
	// Initialized: records.
	_ = os.MkdirAll(filepath.Dir(config.Path()), 0o700)
	_ = os.WriteFile(config.Path(), []byte("machine = \"m\"\n"), 0o600)
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	b, err := spool.Drain(config.SpoolDir())
	if err != nil || len(b.Events) != 1 {
		t.Fatalf("initialized machine: %+v %v", b, err)
	}
	b.Close()
	t.Setenv("CASEBOOK_INTERNAL", "1")
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
}

// writeConfig initializes casebook in a fresh CASEBOOK_HOME with the given
// scan roots and returns nothing; the spool is config.SpoolDir().
func writeConfig(t *testing.T, roots ...string) {
	t.Helper()
	t.Setenv("CASEBOOK_HOME", filepath.Join(t.TempDir(), "home"))
	_ = os.MkdirAll(filepath.Dir(config.Path()), 0o700)
	toml := "machine = \"m\"\nroots = ["
	for i, r := range roots {
		if i > 0 {
			toml += ", "
		}
		toml += "\"" + r + "\""
	}
	if err := os.WriteFile(config.Path(), []byte(toml+"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func spooled(t *testing.T) int {
	t.Helper()
	b, err := spool.Drain(config.SpoolDir())
	if err != nil {
		t.Fatal(err)
	}
	n := len(b.Events)
	if err := b.Done(); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMainSkipsTempFolders(t *testing.T) {
	testgit.Env(t)
	pkg, _ := filepath.Abs(".") // a real (non-temp) directory: this package's source
	realRoot, _ := filepath.EvalSymlinks(t.TempDir())
	writeConfig(t, filepath.Join(realRoot, "GitHub"))
	scratch := t.TempDir() // under os.TempDir(), outside the configured root
	// As written and through the /private realpath: both are temp.
	for _, dir := range []string{scratch, func() string { p, _ := filepath.EvalSymlinks(scratch); return p }()} {
		t.Chdir(dir)
		if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
			t.Fatal("Main returned non-zero")
		}
		if n := spooled(t); n != 0 {
			t.Errorf("hook in temp folder %s spooled %d event(s)", dir, n)
		}
	}
	// A real working directory with a temp GIT_DIR (a probe's git) is temp too.
	t.Chdir(pkg)
	t.Setenv("GIT_DIR", filepath.Join(scratch, "repo", ".git"))
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	if n := spooled(t); n != 0 {
		t.Errorf("hook with a temp GIT_DIR spooled %d event(s)", n)
	}
}

func TestMainRecordsInsideAConfiguredRootInTemp(t *testing.T) {
	testgit.Env(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	writeConfig(t, root)
	clone := filepath.Join(root, "hail")
	_ = os.MkdirAll(clone, 0o755)
	t.Chdir(clone)
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	if n := spooled(t); n != 1 {
		t.Errorf("hook inside a configured root spooled %d event(s), want 1", n)
	}
}

// TestMainRecordsRealWorkInTempFolders: real work in a temp folder is
// journalled. A linked worktree of a clone in a configured root (git runs
// its hooks there without GIT_DIR for some hooks, with it for others), and
// a scratch clone's push to GitHub, are recorded; the same scratch clone's
// push to a local bare repo is not.
func TestMainRecordsRealWorkInTempFolders(t *testing.T) {
	testgit.Env(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	writeConfig(t, root)
	clone := filepath.Join(root, "hail")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Git(t, clone, "init", "-q", "-b", "main")
	testgit.Commit(t, clone, "a", "1")
	scratch, _ := filepath.EvalSymlinks(t.TempDir()) // temp, outside the root
	wt := filepath.Join(scratch, "wt")
	testgit.Git(t, clone, "worktree", "add", "-q", "-b", "feat", wt)

	t.Chdir(wt)
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	if n := spooled(t); n != 1 {
		t.Errorf("hook in a worktree of a rooted clone, no GIT_DIR: spooled %d, want 1", n)
	}
	t.Setenv("GIT_DIR", testgit.Git(t, wt, "rev-parse", "--absolute-git-dir"))
	if Main([]string{"post-commit"}, strings.NewReader("")) != 0 {
		t.Fatal("Main returned non-zero")
	}
	if n := spooled(t); n != 1 {
		t.Errorf("hook in a worktree of a rooted clone, GIT_DIR set: spooled %d, want 1", n)
	}
	_ = os.Unsetenv("GIT_DIR")

	t.Chdir(scratch)
	for url, want := range map[string]int{"https://github.com/schuettc/hail.git": 1, "git@github.com:schuettc/hail.git": 1, "../remote.git": 0, filepath.Join(scratch, "remote.git"): 0} {
		if Main([]string{"pre-push", "origin", url}, strings.NewReader("")) != 0 {
			t.Fatal("Main returned non-zero")
		}
		if n := spooled(t); n != want {
			t.Errorf("pre-push from a scratch clone to %s: spooled %d, want %d", url, n, want)
		}
	}
}
