// Package check implements `cull check`: discover test files (whole suite or
// a git diff), extract tests, group near-duplicates, judge tests and groups
// with Jev, apply policy, and report — writing .cull/last.json.
package check

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

// Config is .cull.toml at the project root.
type Config struct {
	Egress          bool     `toml:"egress"`
	Model           string   `toml:"model"`
	MaxContextBytes int      `toml:"max_context_bytes"`
	Concurrency     int      `toml:"concurrency"`
	Exclude         []string `toml:"exclude"`
	TestRubric      string   `toml:"test_rubric"`
	GroupRubric     string   `toml:"group_rubric"`
	TestCommand     string   `toml:"test_command"` // unused until apply
}

// defaultConfig is Config before .cull.toml is applied.
func defaultConfig() Config {
	return Config{
		Model:           "jev-latest",
		MaxContextBytes: cases.DefaultMaxContextBytes,
		Concurrency:     6,
		TestRubric:      rubric.DefaultTest,
		GroupRubric:     rubric.DefaultGroup,
	}
}

// LoadConfig reads <root>/.cull.toml. A missing file returns the defaults
// and found=false (not an error); a present file that fails to parse, or
// that sets an unknown key, is an error.
func LoadConfig(root string) (Config, bool, error) {
	cfg := defaultConfig()
	path := filepath.Join(root, ".cull.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return cfg, false, err
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return cfg, true, fmt.Errorf(".cull.toml: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return cfg, true, fmt.Errorf(".cull.toml: unknown keys: %s", strings.Join(keys, ", "))
	}
	return cfg, true, nil
}
