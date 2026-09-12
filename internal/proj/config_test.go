package proj

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "proj")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "config.toml"), []byte(body), 0o644)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func TestConfigDefaultsAndOverride(t *testing.T) {
	writeConfig(t, `
default_agent = "pi"
sidebar = true
[project."bettor-help"]
default_agent = "claude"
sidebar = false
`)
	c := LoadConfig()
	if c.AgentFor("anything") != "pi" {
		t.Fatal("global agent")
	}
	if c.AgentFor("bettor-help") != "claude" {
		t.Fatal("override agent")
	}
	if c.SidebarFor("anything") != true || c.SidebarFor("bettor-help") != false {
		t.Fatal("sidebar override")
	}
}

func TestConfigMissingFileDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "none"))
	c := LoadConfig()
	if c.AgentFor("x") != "pi" || c.SidebarFor("x") != true {
		t.Fatal("missing file must yield built-in defaults")
	}
}

func TestConfigSidebarLayoutDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "none"))
	c := LoadConfig()
	panes := c.SidebarLayout.Panes
	if len(panes) != 3 || panes[0] != "scratch" || panes[1] != "yazi" || panes[2] != "shell" {
		t.Fatalf("expected default panes [scratch yazi shell], got %v", panes)
	}
}

func TestConfigDefaultSizes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "none"))
	c := LoadConfig()
	if got := c.SidebarLayout.Sizes["scratch"]; got != 12 {
		t.Fatalf("expected Sizes[scratch]==12, got %d", got)
	}
	if got := c.SidebarLayout.Sizes["shell"]; got != 10 {
		t.Fatalf("expected Sizes[shell]==10, got %d", got)
	}
}

func TestConfigUserSizesPreserved(t *testing.T) {
	writeConfig(t, `
[sidebar_layout]
sizes = {scratch = 20}
`)
	c := LoadConfig()
	// User-specified scratch must be preserved.
	if got := c.SidebarLayout.Sizes["scratch"]; got != 20 {
		t.Fatalf("expected user Sizes[scratch]==20, got %d", got)
	}
	// Shell not specified by user — should be seeded with default.
	if got := c.SidebarLayout.Sizes["shell"]; got != 10 {
		t.Fatalf("expected default Sizes[shell]==10, got %d", got)
	}
}

func TestConfigMalformedFile(t *testing.T) {
	writeConfig(t, `this is not valid toml ===`)
	c := LoadConfig()
	if c.AgentFor("x") != "pi" || c.SidebarFor("x") != true {
		t.Fatal("malformed file must yield built-in defaults")
	}
}

func TestConfigDefaultModelDecoded(t *testing.T) {
	writeConfig(t, `
default_agent = "pi"
default_model = "claude-bridge/claude-opus-4-8"
`)
	c := LoadConfig()
	if c.DefaultModel != "claude-bridge/claude-opus-4-8" {
		t.Fatalf("DefaultModel = %q", c.DefaultModel)
	}
}

func TestSaveDefaultModelCreatesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := SaveDefaultModel("claude-bridge/claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	if got := LoadConfig().DefaultModel; got != "claude-bridge/claude-opus-4-8" {
		t.Fatalf("round-trip DefaultModel = %q", got)
	}
}

func TestSaveDefaultModelReplacesPreservingComments(t *testing.T) {
	writeConfig(t, `# top comment
default_agent = "pi"
default_model = "claude-bridge/claude-fable-5"

# providers explained
[model_providers]
pi = ["claude-bridge"]
`)
	if err := SaveDefaultModel("claude-bridge/claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(configPath())
	body := string(raw)
	if !strings.Contains(body, `default_model = "claude-bridge/claude-opus-4-8"`) {
		t.Fatalf("value not updated:\n%s", body)
	}
	if strings.Contains(body, "claude-fable-5") {
		t.Fatalf("old value not replaced:\n%s", body)
	}
	// exactly one default_model line
	if n := strings.Count(body, "default_model"); n != 1 {
		t.Fatalf("want 1 default_model line, got %d:\n%s", n, body)
	}
	for _, want := range []string{"# top comment", "# providers explained", "[model_providers]", `pi = ["claude-bridge"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("lost %q:\n%s", want, body)
		}
	}
}

func TestSaveDefaultModelInsertsBeforeTables(t *testing.T) {
	writeConfig(t, `default_agent = "pi"

# providers
[model_providers]
pi = ["claude-bridge"]

[project."bettor-help"]
default_agent = "claude"
`)
	if err := SaveDefaultModel("claude-bridge/claude-opus-4-8"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(configPath())
	body := string(raw)
	dmIdx := strings.Index(body, "default_model")
	tblIdx := strings.Index(body, "[model_providers]")
	if dmIdx < 0 || tblIdx < 0 || dmIdx > tblIdx {
		t.Fatalf("default_model must land in the top-level section before tables:\n%s", body)
	}
	// tables and their contents preserved
	for _, want := range []string{`[project."bettor-help"]`, `default_agent = "claude"`, `pi = ["claude-bridge"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("lost %q:\n%s", want, body)
		}
	}
	if got := LoadConfig().DefaultModel; got != "claude-bridge/claude-opus-4-8" {
		t.Fatalf("round-trip DefaultModel = %q", got)
	}
}
