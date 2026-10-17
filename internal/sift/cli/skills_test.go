package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/tackle/skills"
)

// sift skills install hands its --agent flags to the shared installer
// (skills.Install, tested in the skills package) for the sift skill.
func TestSkillsInstall(t *testing.T) {
	home := siftEnv(t)
	want, err := skills.FS.ReadFile("sift/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if code, out, errw := run(t, "", "skills", "install", "--agent", "claude-code"); code != 0 {
		t.Fatalf("code %d: %s %s", code, out, errw)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".claude/skills/sift/SKILL.md")); err != nil || string(b) != string(want) {
		t.Errorf("not installed: %v", err)
	}
	if code, _, _ := run(t, "", "skills", "install", "--agent", "nope"); code == 0 {
		t.Error("an unknown agent was accepted")
	}
}
