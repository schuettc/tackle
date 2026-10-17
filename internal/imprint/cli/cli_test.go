package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/subaud-tools/tackle/skills"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(""), &out, &errw)
	return code, out.String(), errw.String()
}

func TestVersion(t *testing.T) {
	code, out, errw := run(t, "version")
	if code != 0 || !strings.Contains(out, "imprint") {
		t.Fatalf("code %d: %s %s", code, out, errw)
	}
}

// The command index lists the framework's built-ins plus imprint's own.
func TestCommandsJSON(t *testing.T) {
	code, out, errw := run(t, "commands", "--json")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errw)
	}
	var cmds []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &cmds); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cmds {
		got = append(got, c.Name)
	}
	sort.Strings(got)
	for _, want := range []string{"channel", "skills"} {
		if i := sort.SearchStrings(got, want); i == len(got) || got[i] != want {
			t.Errorf("commands %v lack %s", got, want)
		}
	}
}

func TestSkillsInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
	want, err := skills.FS.ReadFile("imprint/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if code, out, errw := run(t, "skills", "install", "--agent", "claude-code"); code != 0 {
		t.Fatalf("code %d: %s %s", code, out, errw)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".claude/skills/imprint/SKILL.md")); err != nil || string(b) != string(want) {
		t.Errorf("not installed: %v", err)
	}
	if code, _, _ := run(t, "skills", "list"); code == 0 {
		t.Error("skills list accepted")
	}
}
