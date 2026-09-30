package proj

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(a ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "one")
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y"), 0o644) // untracked ⇒ dirty

	g := GitStatus(dir)
	if !g.Repo || g.Branch != "main" {
		t.Fatalf("git=%+v", g)
	}
	if g.Dirty != 1 {
		t.Fatalf("dirty=%d want 1", g.Dirty)
	}

	if GitStatus(t.TempDir()).Repo {
		t.Fatal("non-repo must report Repo=false")
	}
}
