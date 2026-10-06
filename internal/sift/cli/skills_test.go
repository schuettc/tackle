package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/skills"
)

// sift skills install writes the skill the binary was built with into each
// harness found (its skills directory may be a symlink, as a dotfiles
// install makes it), again and again, and never over a skill of another
// name.
func TestSkillsInstall(t *testing.T) {
	home := siftEnv(t)
	want, err := skills.FS.ReadFile("sift/SKILL.md")
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
		code, out, errw := run(t, "", "skills", "install")
		if code != 0 {
			t.Fatalf("code %d: %s %s", code, out, errw)
		}
		for _, p := range []string{filepath.Join(home, ".claude/skills/sift/SKILL.md"), filepath.Join(dots, "sift/SKILL.md")} {
			if b, err := os.ReadFile(p); err != nil || string(b) != string(want) {
				t.Errorf("%s: %v", p, err)
			}
			if !strings.Contains(out, filepath.Dir(p)) && !strings.Contains(out, "sift/SKILL.md") {
				t.Errorf("output lacks %s:\n%s", p, out)
			}
		}
		if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
			t.Error("installed for a harness that is not there")
		}
	}
	foreign := filepath.Join(home, ".claude/skills/sift/SKILL.md")
	if err := os.WriteFile(foreign, []byte("---\nname: other\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errw := run(t, "", "skills", "install", "--agent", "claude-code"); code == 0 || !strings.Contains(errw, "not sift's") {
		t.Fatalf("code %d %s", code, errw)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "---\nname: other\n---\n" {
		t.Error("a foreign skill was overwritten")
	}
}

// A symlink or a hard link waiting at the old fixed temporary name beside
// SKILL.md is never written through: install writes a fresh file.
func TestSkillsInstallWritesThroughAFreshFile(t *testing.T) {
	home := siftEnv(t)
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
		if code, out, errw := run(t, "", "skills", "install", "--agent", "claude-code"); code != 0 {
			t.Fatalf("%s: code %d: %s %s", how, code, out, errw)
		}
		if b, _ := os.ReadFile(target); string(b) != "keep me\n" {
			t.Errorf("%s: the target was written: %q", how, b)
		}
	}
}
