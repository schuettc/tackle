package skills_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	st "github.com/subaud-tools/tackle/internal/sift/sifttest"
	"github.com/subaud-tools/tackle/skills"
	tools "github.com/subaud-tools/tools-common"
)

func install(name string, agents ...string) (string, string, error) {
	var out, errw bytes.Buffer
	err := skills.Install(name, agents, &out, &errw)
	return out.String(), errw.String(), err
}

// Install writes the named skill the binary was built with into each
// harness found (its skills directory may be a symlink, as a dotfiles
// install makes it), again and again, and never over a skill of another
// name.
func TestInstall(t *testing.T) {
	for _, name := range []string{"sift", "imprint"} {
		t.Run(name, func(t *testing.T) {
			home := st.Home(t)
			want, err := skills.FS.ReadFile(name + "/SKILL.md")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			dots := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, ".pi/agent"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dots, filepath.Join(home, ".pi/agent/skills")); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				out, errw, err := install(name)
				if err != nil {
					t.Fatalf("%v: %s %s", err, out, errw)
				}
				for _, p := range []string{filepath.Join(home, ".claude/skills", name, "SKILL.md"), filepath.Join(dots, name, "SKILL.md")} {
					if b, err := os.ReadFile(p); err != nil || string(b) != string(want) {
						t.Errorf("%s: %v", p, err)
					}
					if !strings.Contains(out, filepath.Dir(p)) && !strings.Contains(out, name+"/SKILL.md") {
						t.Errorf("output lacks %s:\n%s", p, out)
					}
				}
				if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
					t.Error("installed for a harness that is not there")
				}
			}
			foreign := filepath.Join(home, ".claude/skills", name, "SKILL.md")
			if err := os.WriteFile(foreign, []byte("---\nname: other\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			_, errw, err := install(name, "claude-code")
			var ee *tools.ExitError
			if !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(errw, "not "+name+"'s") {
				t.Fatalf("%v %s", err, errw)
			}
			if b, _ := os.ReadFile(foreign); string(b) != "---\nname: other\n---\n" {
				t.Error("a foreign skill was overwritten")
			}
		})
	}
}

// With no harness home and none named, nothing is installed and the exit
// code is 2.
func TestInstallWithNoHarness(t *testing.T) {
	st.Home(t)
	_, _, err := install("imprint")
	var ee *tools.ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("want exit 2, got %v", err)
	}
}

// A symlink or a hard link waiting at the old fixed temporary name beside
// SKILL.md is never written through: install writes a fresh file.
func TestInstallWritesThroughAFreshFile(t *testing.T) {
	home := st.Home(t)
	dir := filepath.Join(home, ".claude/skills/sift")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, how := range []string{"symlink", "hard link"} {
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte("keep me\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		trap := filepath.Join(dir, "SKILL.md.sift-new")
		_ = os.Remove(trap)
		link := os.Symlink
		if how == "hard link" {
			link = os.Link
		}
		if err := link(target, trap); err != nil {
			t.Fatal(err)
		}
		if out, errw, err := install("sift", "claude-code"); err != nil {
			t.Fatalf("%s: %v: %s %s", how, err, out, errw)
		}
		if b, _ := os.ReadFile(target); string(b) != "keep me\n" {
			t.Errorf("%s: the target was written: %q", how, b)
		}
	}
}
