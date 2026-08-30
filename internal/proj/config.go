package proj

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Layout describes the sidebar pane arrangement.
// In config.toml this lives under [sidebar_layout] (not [sidebar.layout])
// because BurntSushi/toml does not decode a dotted struct-tag key against a
// top-level bool field with the same prefix ("sidebar").
type Layout struct {
	Panes []string       `toml:"panes"`
	Sizes map[string]int `toml:"sizes"`
}

// ProjectOverride holds optional per-project overrides.
type ProjectOverride struct {
	DefaultAgent *string `toml:"default_agent"`
	Sidebar      *bool   `toml:"sidebar"`
}

// Config is the in-memory representation of config.toml.
type Config struct {
	DefaultAgent  string                     `toml:"default_agent"`
	Sidebar       bool                       `toml:"sidebar"`
	SidebarLayout Layout                     `toml:"sidebar_layout"`
	Projects      map[string]ProjectOverride `toml:"project"`
}

func configPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "proj", "config.toml")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "proj", "config.toml")
}

func defaults() Config {
	return Config{
		DefaultAgent:  "pi",
		Sidebar:       true,
		SidebarLayout: Layout{Panes: []string{"scratch", "yazi", "shell"}},
		Projects:      map[string]ProjectOverride{},
	}
}

// LoadConfig reads ~/.config/proj/config.toml (or $XDG_CONFIG_HOME/proj/config.toml).
// A missing or malformed file silently returns built-in defaults.
func LoadConfig() Config {
	fallback := defaults()
	b, err := os.ReadFile(configPath())
	if err != nil {
		return fallback
	}
	// Pre-seed with defaults so absent keys keep their default values.
	seeded := defaults()
	if _, err := toml.Decode(string(b), &seeded); err != nil {
		return fallback
	}
	if seeded.Projects == nil {
		seeded.Projects = map[string]ProjectOverride{}
	}
	if len(seeded.SidebarLayout.Panes) == 0 {
		seeded.SidebarLayout.Panes = defaults().SidebarLayout.Panes
	}
	return seeded
}

// AgentFor returns the effective agent name for the given project.
func (c Config) AgentFor(project string) string {
	if o, ok := c.Projects[project]; ok && o.DefaultAgent != nil {
		return *o.DefaultAgent
	}
	return c.DefaultAgent
}

// SidebarFor returns the effective sidebar visibility for the given project.
func (c Config) SidebarFor(project string) bool {
	if o, ok := c.Projects[project]; ok && o.Sidebar != nil {
		return *o.Sidebar
	}
	return c.Sidebar
}
