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
	run := func(a ...string) { exec.Command("git", append([]string{"-C", dir}, a...)...).Run() }
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	run("add", ".")
	run("commit", "-m", "one")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y"), 0o644) // untracked ⇒ dirty

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
