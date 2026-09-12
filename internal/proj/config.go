package proj

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	DefaultModel *string `toml:"default_model"`
	Sidebar      *bool   `toml:"sidebar"`
}

// Config is the in-memory representation of config.toml.
type Config struct {
	DefaultAgent  string                     `toml:"default_agent"`
	DefaultModel  string                     `toml:"default_model"`
	Sidebar       bool                       `toml:"sidebar"`
	SidebarLayout Layout                     `toml:"sidebar_layout"`
	Projects      map[string]ProjectOverride `toml:"project"`
	// ModelProviders maps a proj agent to the ordered pi providers whose models
	// feed that agent's model picker. Only "pi" is meaningful today; claude's
	// models are built in (see claudeModels). List order is picker order.
	ModelProviders map[string][]string `toml:"model_providers"`
}

func configPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "proj", "config.toml")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "proj", "config.toml")
}

func defaults() Config {
	return Config{
		DefaultAgent: "pi",
		Sidebar:      true,
		SidebarLayout: Layout{
			Panes: []string{"scratch", "yazi", "shell"},
			Sizes: map[string]int{"scratch": 12, "shell": 10},
		},
		Projects: map[string]ProjectOverride{},
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
	// Seed default Sizes only where the user has not specified them explicitly.
	if seeded.SidebarLayout.Sizes == nil {
		seeded.SidebarLayout.Sizes = defaults().SidebarLayout.Sizes
	} else {
		for k, v := range defaults().SidebarLayout.Sizes {
			if _, ok := seeded.SidebarLayout.Sizes[k]; !ok {
				seeded.SidebarLayout.Sizes[k] = v
			}
		}
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

// ModelProvidersFor returns the ordered pi providers configured to feed agent's
// model picker, or nil when none are configured.
func (c Config) ModelProvidersFor(agent string) []string {
	return c.ModelProviders[agent]
}

// ModelFor returns the per-project default model pin, or "" when unset. It is
// agent-agnostic: the caller applies it only when it matches the effective
// agent's available models.
func (c Config) ModelFor(project string) string {
	if o, ok := c.Projects[project]; ok && o.DefaultModel != nil {
		return *o.DefaultModel
	}
	return ""
}

// SaveDefaultModel upserts the top-level `default_model` key in config.toml,
// preserving the rest of the file (comments, tables, and other keys) rather
// than re-marshaling the whole config. A missing file is created with just the
// key. When the key is absent, it is inserted at the end of the top-level
// section (before the first table header) so it stays grouped with the other
// top-level keys like default_agent.
func SaveDefaultModel(model string) error {
	p := configPath()
	line := fmt.Sprintf("default_model = %q", model)

	b, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(line+"\n"), 0o644)
	}

	lines := strings.Split(string(b), "\n")
	firstTable := -1 // index of the first table header line
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") {
			firstTable = i
			break
		}
	}
	topEnd := len(lines)
	if firstTable >= 0 {
		topEnd = firstTable
	}
	// Replace an existing top-level key in place.
	for i := 0; i < topEnd; i++ {
		if isTopKey(lines[i], "default_model") {
			lines[i] = line
			return os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	// Insert at the end of the top-level section. Trim trailing blank lines in
	// that section so the new key sits directly after the last top-level content.
	ins := topEnd
	for ins > 0 && strings.TrimSpace(lines[ins-1]) == "" {
		ins--
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[:ins]...)
	out = append(out, line)
	if firstTable >= 0 {
		out = append(out, "") // blank line before the first table
	}
	out = append(out, lines[ins:]...)
	return os.WriteFile(p, []byte(strings.Join(out, "\n")), 0o644)
}

// isTopKey reports whether line is an assignment of the given bare key
// (`key = ...`), ignoring surrounding whitespace. Used to find a top-level key
// for in-place replacement without a TOML round-trip.
func isTopKey(line, key string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, key) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(t[len(key):]), "=")
}

// SidebarFor returns the effective sidebar visibility for the given project.
func (c Config) SidebarFor(project string) bool {
	if o, ok := c.Projects[project]; ok && o.Sidebar != nil {
		return *o.Sidebar
	}
	return c.Sidebar
}
