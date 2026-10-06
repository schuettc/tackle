package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
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

// gh on PATH but not logged in looks up nothing: doctor says so.
func TestDoctorGhNotLoggedIn(t *testing.T) {
	siftEnv(t)
	c := config.Default()
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	oldLook, oldAuth := lookPath, ghAuth
	lookPath = func(string) (string, error) { return "/bin/gh", nil }
	ghAuth = func() error { return errors.New("exit status 1") }
	t.Cleanup(func() { lookPath, ghAuth = oldLook, oldAuth })
	code, out, _ := run(t, "", "doctor")
	if code != 0 || !strings.Contains(out, "gh auth login") {
		t.Fatalf("code %d\n%s", code, out)
	}
	ghAuth = func() error { return nil }
	if _, out, _ := run(t, "", "doctor"); !regexp.MustCompile(`gh\s+ok`).MatchString(out) {
		t.Fatalf("%s", out)
	}
}

// A harness config sift cannot read (Codex's config.toml) is a failure, not
// a quiet "no profiles".
func TestDoctorBrokenHarnessConfig(t *testing.T) {
	home := siftEnv(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("project_doc_max_bytes = \"big\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Profiles = []string{"codex"}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "", "doctor")
	if code != 1 || !regexp.MustCompile(`config\s+ok`).MatchString(out) || !regexp.MustCompile(`profiles\s+missing: codex config .*config.toml`).MatchString(out) {
		t.Fatalf("code %d\n%s", code, out)
	}
}

// The page server and each enabled harness's channel registration are
// notes: sift works without them, but the user's decisions then reach a
// session only through sift wait.
func TestDoctorPageServerAndChannels(t *testing.T) {
	home := siftEnv(t)
	for _, d := range []string{".claude", ".codex", ".pi/agent"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := config.Default()
	c.Profiles = []string{"claude-code", "codex", "pi"}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "", "doctor")
	for _, re := range []string{
		`serve\s+note: sift serve is not running`,
		`claude-code channel\s+note: sift is not in .*\.claude\.json mcpServers`,
		`codex channel\s+note: sift is not in .*config\.toml \[mcp_servers\]`,
		`pi channel\s+note: sift is not in .*channels\.json`,
	} {
		if !regexp.MustCompile(re).MatchString(out) {
			t.Errorf("want %s:\n%s", re, out)
		}
	}
	if code != 0 {
		t.Fatalf("notes failed doctor: %d", code)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"sift":{"command":"sift","args":["channel"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("[mcp_servers.sift]\ncommand = \"sift\"\nargs = [\"channel\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "channels.json"), []byte(`{"channelServers":{"sift":{"command":"sift","args":["channel"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, _ = run(t, "", "doctor")
	for _, re := range []string{`claude-code channel\s+ok`, `codex channel\s+ok`, `pi channel\s+ok`} {
		if !regexp.MustCompile(re).MatchString(out) {
			t.Errorf("want %s:\n%s", re, out)
		}
	}
}
