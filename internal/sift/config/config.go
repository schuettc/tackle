// Package config is sift's configuration: TOML at
// tools.ConfigDir("sift")/config.toml. It names the profiles that are on, the
// roots to audit, the budgets and windows, the patterns the text checks use,
// and the memory stores that have been retired.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/tackle/internal/sift/host"
	"github.com/schuettc/tackle/internal/sift/profile"
	tools "github.com/schuettc/tools-common"
)

// Config is config.toml.
type Config struct {
	// Profiles names the harness profiles that are on: shipped ones or ones
	// defined under [[profile]].
	Profiles []string          `toml:"profiles"`
	Custom   []profile.Profile `toml:"profile,omitempty"`
	Roots    []Root            `toml:"root,omitempty"`
	// Archive is where intake copies a memory store before it is retired.
	Archive  string    `toml:"archive,omitempty"`
	Budgets  Budgets   `toml:"budgets"`
	Windows  Windows   `toml:"windows"`
	Negative Negative  `toml:"negative"`
	Stale    Stale     `toml:"stale"`
	Retired  []Retired `toml:"retired,omitempty"`
}

// Root is a directory to audit: one repo, or a directory holding repos at
// any depth.
type Root struct {
	Path string `toml:"path"`
	// Base is the branch repos are read at (origin/<base>, else <base>);
	// empty: each repo's origin/HEAD, else its upstream, else HEAD.
	Base string `toml:"base,omitempty"`
	// Private is where a rule about your own setup goes instead of a shared
	// file: an untracked local file or a private companion repo.
	Private string `toml:"private,omitempty"`
	// Exclude lists repo-relative globs (** allowed) never audited.
	Exclude []string `toml:"exclude,omitempty"`
}

// Budgets are the size budgets in bytes per file class.
type Budgets struct {
	Global int `toml:"global"`
	Repo   int `toml:"repo"`
	Skill  int `toml:"skill"`
}

// Windows are the time windows, in days.
type Windows struct {
	// WeeklyDays is the time since the last completed round before one is due.
	WeeklyDays int `toml:"weekly_days"`
	// UsageDays is how many days a new model is used before it triggers a round.
	UsageDays int `toml:"usage_days"`
	// StaleDays is the age past which a date in a file reads as stale status.
	StaleDays int `toml:"stale_days"`
}

// Negative holds the negative-rule patterns (case-insensitive regexps).
// Unset, the shipped defaults apply.
type Negative struct {
	Patterns []string `toml:"patterns,omitempty"`
}

// Stale holds the stale-status phrases (case-insensitive regexps). Unset,
// the shipped defaults apply. Waits are the phrases that wait on a pull
// request, with {ref} where the reference goes: a line matching one about a
// PR the host reports done is certain.
type Stale struct {
	Phrases []string `toml:"phrases,omitempty"`
	Waits   []string `toml:"waits,omitempty"`
}

// RefPlaceholder marks where a wait phrase's reference goes.
const RefPlaceholder = "{ref}"

// RefGroup is the named group RefPlaceholder becomes; a wait phrase may not
// name a group of its own with it.
const RefGroup = "ref"

// Wait compiles a wait phrase as it runs: case-insensitive, with its one
// RefPlaceholder replaced by the group RefGroup matching a reference.
func Wait(w string) (*regexp.Regexp, error) {
	if n := strings.Count(w, RefPlaceholder); n != 1 {
		return nil, fmt.Errorf("needs %s once where the reference goes, has it %d times", RefPlaceholder, n)
	}
	re, err := regexp.Compile("(?i)" + strings.Replace(w, RefPlaceholder, "(?P<"+RefGroup+">"+host.RefPattern+")", 1))
	if err != nil {
		return nil, err
	}
	n := 0
	for _, name := range re.SubexpNames() {
		if name == RefGroup {
			n++
		}
	}
	if n != 1 {
		return nil, fmt.Errorf("the group %s must be %s's alone, and %s must not be quoted", RefGroup, RefPlaceholder, RefPlaceholder)
	}
	return re, nil
}

// Retired is a memory store that has been migrated: any pointer to it in an
// instruction file is now wrong.
type Retired struct {
	Name     string   `toml:"name"`
	Patterns []string `toml:"patterns"` // literal text: paths, file names, tool names
}

// ErrMissing is returned by Load when there is no config file.
var ErrMissing = errors.New("no sift config: run sift init")

// Path is where the config lives.
func Path() string { return filepath.Join(tools.ConfigDir("sift"), "config.toml") }

// Default is the config before config.toml is applied.
func Default() Config {
	return Config{
		Budgets: Budgets{Global: 8000, Repo: 6000, Skill: 10000},
		Windows: Windows{WeeklyDays: 7, UsageDays: 3, StaleDays: 30},
		Negative: Negative{Patterns: []string{
			`\bnever\b`,
			`\b(?:don't|do not|must not|mustn't|should not|shouldn't)\b`,
			`\bavoid\b`,
			`^\s*(?:[-*+]|\d+\.)\s+(?:\*\*)?no\s`,
		}},
		Stale: Stale{Phrases: []string{
			`\bwaiting (?:on|for)\b`,
			`\bin[ -]flight\b`,
			`\bin progress\b`,
			`\bblocked (?:on|by)\b`,
			`\b(?:until|once) [^.;:!?]{1,80}? (?:lands|merges|ships|is merged|is released)\b`,
			`\bnot yet (?:merged|released|shipped|landed)\b`,
		}, Waits: []string{
			`\b(?:until|once|after) (?:[^.;:!?]{0,40}? )?{ref} (?:lands|merges|is merged|ships)\b`,
			`\bwaiting (?:on|for) (?:[^.;:!?]{0,40}? )?{ref}`,
			`\bblocked (?:on|by) (?:[^.;:!?]{0,40}? )?{ref}`,
		}},
	}
}

// Load reads the config at path over the defaults. A missing file is
// ErrMissing; a file that fails to parse, sets an unknown key or does not
// validate is an error.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, ErrMissing
	}
	if err != nil {
		return c, err
	}
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return c, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	for i := range c.Roots {
		c.Roots[i].Path = profile.Expand(c.Roots[i].Path)
		c.Roots[i].Private = profile.Expand(c.Roots[i].Private)
	}
	c.Archive = profile.Expand(c.Archive)
	if err := c.Validate(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the config to path (0600), creating its directory.
func Save(path string, c Config) error {
	if err := tools.EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return tools.WriteFileAtomic(path, b.Bytes(), 0o600)
}

// Validate reports the first thing wrong with the config.
func (c Config) Validate() error {
	for _, p := range c.Custom {
		if err := p.Validate(); err != nil {
			return err
		}
		if _, ok := profile.Builtin(p.Name); ok {
			return fmt.Errorf("profile %s: a shipped profile has that name", p.Name)
		}
	}
	for _, name := range c.Profiles {
		if !c.known(name) {
			return fmt.Errorf("unknown profile %q", name)
		}
	}
	for _, r := range c.Roots {
		if !filepath.IsAbs(r.Path) {
			return fmt.Errorf("root %q: path must be absolute (or start with ~)", r.Path)
		}
	}
	if c.Budgets.Global <= 0 || c.Budgets.Repo <= 0 || c.Budgets.Skill <= 0 {
		return errors.New("budgets must be positive")
	}
	if c.Windows.WeeklyDays <= 0 || c.Windows.UsageDays <= 0 || c.Windows.StaleDays <= 0 {
		return errors.New("windows must be positive")
	}
	for _, p := range append(append([]string(nil), c.Negative.Patterns...), c.Stale.Phrases...) {
		if _, err := regexp.Compile("(?i)" + p); err != nil {
			return fmt.Errorf("pattern %q: %w", p, err)
		}
	}
	for _, w := range c.Stale.Waits {
		if _, err := Wait(w); err != nil {
			return fmt.Errorf("wait phrase %q: %w", w, err)
		}
	}
	for _, r := range c.Retired {
		if r.Name == "" || len(r.Patterns) == 0 {
			return fmt.Errorf("retired store %q: needs a name and patterns", r.Name)
		}
	}
	return nil
}

// Enabled returns the profiles that are on, in config order.
func (c Config) Enabled() ([]profile.Profile, error) {
	var out []profile.Profile
	for _, name := range c.Profiles {
		p, err := c.lookup(name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (c Config) known(name string) bool {
	for _, p := range c.Custom {
		if p.Name == name {
			return true
		}
	}
	_, ok := profile.Builtin(name)
	return ok
}

// lookup returns a config profile as written, or a builtin with its
// harness's own settings applied.
func (c Config) lookup(name string) (profile.Profile, error) {
	for _, p := range c.Custom {
		if p.Name == name {
			return p, nil
		}
	}
	p, ok := profile.Builtin(name)
	if !ok {
		return p, fmt.Errorf("unknown profile %q", name)
	}
	return p.Configured()
}
