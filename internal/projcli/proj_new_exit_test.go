package projcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/proj"
)

// A --command against an already-existing session is refused: exit 3, with the
// JSON still printed (created:false) so hail can read it and back off.
func TestRunNewCommandExit3OnReuse(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	cfg := t.TempDir()
	projDir := filepath.Join(t.TempDir(), "projexit3")
	mustMkdir(t, projDir)
	mustMkdir(t, filepath.Join(cfg, "proj"))
	if err := os.WriteFile(filepath.Join(cfg, "proj", "roots"), []byte("project:"+projDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", cfg)

	const projName = "projexit3"
	sock := proj.SocketFor(projName)
	defer proj.Run(sock, "kill-server")

	out1, code1 := captureStdout(t, []string{"new", projName + "/w", "--agent", "none", "--json", "--command", "sleep", "30"})
	if code1 != 0 {
		t.Fatalf("first new: exit %d, want 0; out=%s", code1, out1)
	}
	if !strings.Contains(out1, `"created":true`) {
		t.Fatalf("first new: expected created:true, got %s", out1)
	}

	out2, code2 := captureStdout(t, []string{"new", projName + "/w", "--json", "--command", "sleep", "30"})
	if code2 != 3 {
		t.Fatalf("reuse with --command: exit %d, want 3; out=%s", code2, out2)
	}
	if !strings.Contains(out2, `"created":false`) {
		t.Fatalf("reuse: expected created:false in stdout, got %s", out2)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
