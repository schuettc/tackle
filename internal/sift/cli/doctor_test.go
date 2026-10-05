package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
)

func TestDoctorWithoutConfig(t *testing.T) {
	siftEnv(t)
	code, out, _ := run(t, "", "doctor")
	if code != 1 || !strings.Contains(out, "sift init") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestDoctorReportsEachItem(t *testing.T) {
	home := siftEnv(t)
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "AGENTS.md"), []byte("# g\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	c := config.Default()
	c.Profiles = []string{"pi", "codex"}
	c.Roots = []config.Root{{Path: root}}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "", "doctor")
	// codex is on but not installed, gh is absent: notes, not failures.
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, w := range []string{"config", "pi", "AGENTS.md", "codex", "not found", "root", root, "state", "gh", "note"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}

	// An unreadable root is required.
	c.Roots = []config.Root{{Path: filepath.Join(root, "gone")}}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := run(t, "", "doctor"); code != 1 || !strings.Contains(out, "gone") {
		t.Fatalf("code %d\n%s", code, out)
	}
}
