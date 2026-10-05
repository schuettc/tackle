package profile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// The shipped profiles are product data: each path and name was checked
// against a current install of its harness. A change here is a change to
// what sift reads, so it should be deliberate.
func TestBuiltinsArePinned(t *testing.T) {
	want := []Profile{
		{
			Name: "claude-code", Label: "Claude Code",
			Home: "~/.claude", HomeEnv: "CLAUDE_CONFIG_DIR",
			Global:      []string{"CLAUDE.md"},
			RepoFiles:   []string{"CLAUDE.md"},
			Skills:      []string{"skills"},
			Vendor:      []string{"skills/synced"},
			Transcripts: []string{"projects/*/*.jsonl"},
			Memory:      "projects/*/memory",
		},
		{
			Name: "codex", Label: "Codex",
			Home: "~/.codex", HomeEnv: "CODEX_HOME",
			Global:      []string{"AGENTS.override.md", "AGENTS.md"},
			RepoFiles:   []string{"AGENTS.override.md", "AGENTS.md"},
			FirstOnly:   true,
			Skills:      []string{"skills"},
			Transcripts: []string{"sessions/**/*.jsonl"},
			LoadLimit:   32 * 1024,
		},
		{
			Name: "pi", Label: "pi",
			Home: "~/.pi/agent", HomeEnv: "PI_CODING_AGENT_DIR",
			Global:      []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"},
			RepoFiles:   []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"},
			FirstOnly:   true,
			Skills:      []string{"skills"},
			Transcripts: []string{"sessions/**/*.jsonl"},
			Memory:      "memory",
		},
	}
	if got := Builtins(); !reflect.DeepEqual(got, want) {
		t.Fatalf("builtins changed:\n got %+v\nwant %+v", got, want)
	}
	for _, p := range want {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
	}
}

// A profile from the config is the same type and survives a TOML round trip.
func TestConfigProfileRoundTrips(t *testing.T) {
	in := Profile{
		Name: "other", Label: "Other agent", Home: "~/.other",
		Global: []string{"RULES.md"}, RepoFiles: []string{"RULES.md"},
		Skills: []string{"skills"}, Transcripts: []string{"log/*.jsonl"}, LoadLimit: 1000,
	}
	b, err := toml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Profile
	if _, err := toml.Decode(string(b), &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip:\n in %+v\nout %+v\n%s", in, out, b)
	}
}

func TestValidateNeedsANameAndFiles(t *testing.T) {
	for _, p := range []Profile{
		{Home: "~/.x", RepoFiles: []string{"X.md"}},
		{Name: "x", Home: "~/.x"},
		{Name: "x", RepoFiles: []string{"X.md"}},
		{Name: "X y", Home: "~/.x", RepoFiles: []string{"X.md"}},
		{Name: "x", Home: "~/.x", RepoFiles: []string{"a/X.md"}},
	} {
		if p.Validate() == nil {
			t.Errorf("%+v: want an error", p)
		}
	}
}

func TestHomeDirHonoursItsEnvironmentVariable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	p, _ := Builtin("codex")
	if got := p.HomeDir(); got != filepath.Join(home, ".codex") {
		t.Errorf("default home %q", got)
	}
	t.Setenv("CODEX_HOME", "/elsewhere/codex")
	if got := p.HomeDir(); got != "/elsewhere/codex" {
		t.Errorf("env home %q", got)
	}
	t.Setenv("CODEX_HOME", "relative/codex") // relative values are ignored
	if got := p.HomeDir(); got != filepath.Join(home, ".codex") {
		t.Errorf("relative env home %q", got)
	}
}

func TestRepoFileOrder(t *testing.T) {
	p, _ := Builtin("codex")
	if !p.IsRepoFile("AGENTS.md") || p.IsRepoFile("CLAUDE.md") {
		t.Fatal("codex repo files")
	}
	// Codex reads the override before AGENTS.md, one file per directory.
	if got := p.Pick([]string{"AGENTS.md", "AGENTS.override.md"}); !reflect.DeepEqual(got, []string{"AGENTS.override.md"}) {
		t.Errorf("pick %v", got)
	}
	c, _ := Builtin("claude-code")
	if got := c.Pick([]string{"CLAUDE.md", "AGENTS.md"}); !reflect.DeepEqual(got, []string{"CLAUDE.md"}) {
		t.Errorf("claude pick %v", got)
	}
	if _, ok := Builtin("nope"); ok {
		t.Error("unknown builtin found")
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("HOME", "/h")
	for in, want := range map[string]string{"~": "/h", "~/a/b": "/h/a/b", "/abs": "/abs", "rel": "rel"} {
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

// codexHome points CODEX_HOME at a temp dir holding config (none: no file).
func codexHome(t *testing.T, config string) Profile {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := Builtin("codex")
	return p
}

// Codex's own config adds fallback file names after AGENTS.md, in its order,
// and may lower or raise the load limit; unset, the defaults stand.
func TestCodexConfig(t *testing.T) {
	p, err := codexHome(t, "model = \"x\"\nproject_doc_fallback_filenames = [\"TEAM.md\", \"CLAUDE.md\"]\nproject_doc_max_bytes = 16384\n[profiles.fast]\nmodel = \"y\"\n").Configured()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"AGENTS.override.md", "AGENTS.md", "TEAM.md", "CLAUDE.md"}; !reflect.DeepEqual(p.RepoFiles, want) {
		t.Errorf("repo files %v", p.RepoFiles)
	}
	for present, want := range map[string]string{"CLAUDE.md TEAM.md": "TEAM.md", "AGENTS.md TEAM.md": "AGENTS.md", "CLAUDE.md": "CLAUDE.md"} {
		if got := p.Pick(strings.Fields(present)); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("pick %s: %v", present, got)
		}
	}
	if p.LoadLimit != 16384 || !reflect.DeepEqual(p.Global, []string{"AGENTS.override.md", "AGENTS.md"}) {
		t.Errorf("limit %d global %v", p.LoadLimit, p.Global)
	}

	if p, err = codexHome(t, "project_doc_max_bytes = 65536\n").Configured(); err != nil || p.LoadLimit != 65536 || len(p.RepoFiles) != 2 {
		t.Errorf("larger limit: %+v %v", p, err)
	}
	def, _ := Builtin("codex")
	for _, cfg := range []string{"", "model = \"x\"\n"} {
		if p, err = codexHome(t, cfg).Configured(); err != nil || !reflect.DeepEqual(p, def) {
			t.Errorf("config %q: %+v %v", cfg, p, err)
		}
	}
	if _, err = codexHome(t, "project_doc_max_bytes = \"big\"\n").Configured(); err == nil {
		t.Error("a broken config.toml is not an error")
	}
	// Other harnesses are as shipped.
	c, _ := Builtin("claude-code")
	if got, err := c.Configured(); err != nil || !reflect.DeepEqual(got, c) {
		t.Errorf("%+v %v", got, err)
	}
}
