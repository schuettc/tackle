// Package profile describes what sift reads for each agent harness: its
// global instruction file, the instruction files it loads in a repo, its skill
// directories, its transcripts, its memory store and its load limit. The
// shipped profiles are one table (Builtins); a harness sift does not ship is
// described in the config with the same type.
package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// SkillFile is the file name of a skill, whatever the harness.
const SkillFile = "SKILL.md"

// Profile is one harness. Global, Skills, Transcripts and Memory are relative
// to the harness home unless they are absolute or start with ~.
type Profile struct {
	Name  string `toml:"name" json:"name"`
	Label string `toml:"label,omitempty" json:"label,omitempty"`
	// Home is the harness's own directory; HomeEnv, when set and absolute,
	// overrides it (CODEX_HOME and the like).
	Home    string `toml:"home" json:"home"`
	HomeEnv string `toml:"home_env,omitempty" json:"home_env,omitempty"`
	// Global lists the global file's candidates in the harness's order.
	Global []string `toml:"global,omitempty" json:"global,omitempty"`
	// RepoFiles are the instruction file names loaded per directory in a repo,
	// in precedence order. With FirstOnly the harness loads only the first
	// one present in each directory (and only the first global candidate).
	RepoFiles   []string `toml:"repo_files" json:"repo_files"`
	FirstOnly   bool     `toml:"first_only,omitempty" json:"first_only,omitempty"`
	Skills      []string `toml:"skills,omitempty" json:"skills,omitempty"`
	Transcripts []string `toml:"transcripts,omitempty" json:"transcripts,omitempty"`
	Memory      string   `toml:"memory,omitempty" json:"memory,omitempty"`
	// LoadLimit is the combined size in bytes past which the harness stops
	// adding instruction files (0: no limit).
	LoadLimit int `toml:"load_limit,omitempty" json:"load_limit,omitempty"`
}

// Builtins returns the shipped profiles, in display order. Each was checked
// against a current install: Codex's discovery order and 32 KiB
// project_doc_max_bytes come from its AGENTS.md guide (its config.toml can
// change both; see Configured); pi's candidate list from its resource loader.
func Builtins() []Profile {
	piFiles := []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}
	return []Profile{
		{
			Name: "claude-code", Label: "Claude Code",
			Home: "~/.claude", HomeEnv: "CLAUDE_CONFIG_DIR",
			Global:      []string{"CLAUDE.md"},
			RepoFiles:   []string{"CLAUDE.md"},
			Skills:      []string{"skills"},
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
			Global:      append([]string(nil), piFiles...),
			RepoFiles:   append([]string(nil), piFiles...),
			FirstOnly:   true,
			Skills:      []string{"skills"},
			Transcripts: []string{"sessions/**/*.jsonl"},
			Memory:      "memory",
		},
	}
}

// codexConfig is what sift reads of Codex's config.toml.
type codexConfig struct {
	Fallbacks []string `toml:"project_doc_fallback_filenames"`
	MaxBytes  *int     `toml:"project_doc_max_bytes"`
}

// Configured returns the profile with its harness's own settings applied.
// For Codex those are project_doc_fallback_filenames (more names to look for
// in each directory, after AGENTS.override.md and AGENTS.md, in that order)
// and project_doc_max_bytes (the load limit), from config.toml in its home;
// unset, or no file, leaves the shipped values. Other profiles are returned
// as they are.
func (p Profile) Configured() (Profile, error) {
	if p.Name != "codex" {
		return p, nil
	}
	path := filepath.Join(p.HomeDir(), "config.toml")
	var c codexConfig
	if _, err := toml.DecodeFile(path, &c); errors.Is(err, fs.ErrNotExist) {
		return p, nil
	} else if err != nil {
		return p, fmt.Errorf("codex config %s: %w", path, err)
	}
	if len(c.Fallbacks) > 0 {
		files := append([]string(nil), p.RepoFiles...)
		for _, f := range c.Fallbacks {
			if f != "" && !strings.ContainsAny(f, `/\`) && !slices.Contains(files, f) {
				files = append(files, f)
			}
		}
		p.RepoFiles = files
	}
	if c.MaxBytes != nil && *c.MaxBytes > 0 {
		p.LoadLimit = *c.MaxBytes
	}
	return p, nil
}

// Builtin returns the shipped profile with that name.
func Builtin(name string) (Profile, bool) {
	for _, p := range Builtins() {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Validate reports the first thing wrong with a profile.
func (p Profile) Validate() error {
	switch {
	case !nameRE.MatchString(p.Name):
		return fmt.Errorf("profile %q: name must be lower-case letters, digits and hyphens", p.Name)
	case p.Home == "":
		return fmt.Errorf("profile %s: home is required", p.Name)
	case len(p.RepoFiles) == 0:
		return fmt.Errorf("profile %s: repo_files is required", p.Name)
	}
	for _, f := range append(append([]string(nil), p.RepoFiles...), p.Global...) {
		if f == "" || strings.ContainsAny(f, `/\`) {
			return fmt.Errorf("profile %s: %q is not a file name", p.Name, f)
		}
	}
	return nil
}

// HomeDir is the harness home: $HomeEnv when it is set and absolute, else
// Home with ~ expanded.
func (p Profile) HomeDir() string {
	if p.HomeEnv != "" {
		if v := os.Getenv(p.HomeEnv); filepath.IsAbs(v) {
			return filepath.Clean(v)
		}
	}
	return Expand(p.Home)
}

// Path resolves one of the profile's home-relative paths.
func (p Profile) Path(rel string) string {
	if rel == "~" || strings.HasPrefix(rel, "~/") || filepath.IsAbs(rel) {
		return Expand(rel)
	}
	return filepath.Join(p.HomeDir(), rel)
}

// IsRepoFile reports whether name is one of the profile's repo file names.
func (p Profile) IsRepoFile(name string) bool {
	for _, f := range p.RepoFiles {
		if f == name {
			return true
		}
	}
	return false
}

// Pick returns, of the file names present in one directory, those the
// harness loads, in its order: the first match with FirstOnly, else all.
func (p Profile) Pick(present []string) []string {
	has := map[string]bool{}
	for _, n := range present {
		has[n] = true
	}
	var out []string
	for _, f := range p.RepoFiles {
		if has[f] {
			out = append(out, f)
			if p.FirstOnly {
				break
			}
		}
	}
	return out
}

// Expand replaces a leading ~ with the home directory.
func Expand(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}
