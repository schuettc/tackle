// Package config is the per-machine ledger configuration, stored at
// tools.ConfigDir("ledger")/config.toml. Settings shared by every machine
// (policy) live in the ledger repo instead.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	tools "github.com/schuettc/tools-common"
)

// Tool is the family tool name used for the ledger's directories.
const Tool = "ledger"

// Config is one machine's settings.
type Config struct {
	Machine      string   `toml:"machine"`
	User         string   `toml:"user"`
	LedgerRepo   string   `toml:"ledger_repo"`
	LedgerRemote string   `toml:"ledger_remote"`
	Roots        []string `toml:"roots"`
	Owners       []string `toml:"owners,omitempty"`
	SyncInterval string   `toml:"sync_interval"`
}

// ErrNotInitialized means this machine has no ledger config yet.
var ErrNotInitialized = errors.New("ledger is not initialized on this machine")

// Path is the config file.
func Path() string { return filepath.Join(tools.ConfigDir(Tool), "config.toml") }

// StateDir is the ledger's machine-local state directory.
func StateDir() string { return tools.StateDir(Tool) }

// SpoolDir is the machine-local queue of journal events awaiting sync.
func SpoolDir() string { return filepath.Join(tools.StateDir(Tool), "spool") }

// CachePath is the machine-local GitHub observation cache.
func CachePath() string { return filepath.Join(tools.StateDir(Tool), "github.json") }

// SeenPath records decisions observed satisfied (for drift detection).
func SeenPath() string { return filepath.Join(tools.StateDir(Tool), "seen.json") }

// HooksDir is where `ledger hooks install` writes the global hook shims.
func HooksDir() string { return filepath.Join(tools.ConfigDir(Tool), "hooks") }

// Load reads the config and fills defaults; ErrNotInitialized if absent.
func Load() (Config, error) {
	b, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, ErrNotInitialized
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	md, err := toml.Decode(string(b), &c)
	if err != nil {
		return c, fmt.Errorf("%s: %w", Path(), err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return c, fmt.Errorf("%s: unknown key %q", Path(), und[0].String())
	}
	c.Defaults()
	return c, nil
}

// Save writes c with mode 0600.
func Save(c Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	return tools.WriteFileAtomic(Path(), buf.Bytes(), 0o600)
}

// Defaults fills unset fields (machine = short host name, ledger repo under
// the data dir, roots = ~/GitHub plus ~/dotfiles when present, sync every 30m)
// and expands a leading ~ in paths.
func (c *Config) Defaults() {
	if c.Machine == "" {
		c.Machine = DefaultMachine()
	}
	if c.LedgerRepo == "" {
		c.LedgerRepo = filepath.Join(tools.DataDir(Tool), "repo")
	}
	if len(c.Roots) == 0 {
		home, _ := os.UserHomeDir()
		c.Roots = []string{filepath.Join(home, "GitHub")}
		if fi, err := os.Stat(filepath.Join(home, "dotfiles")); err == nil && fi.IsDir() {
			c.Roots = append(c.Roots, filepath.Join(home, "dotfiles"))
		}
	}
	if c.SyncInterval == "" {
		c.SyncInterval = "30m"
	}
	c.LedgerRepo = Expand(c.LedgerRepo)
	for i := range c.Roots {
		c.Roots[i] = Expand(c.Roots[i])
	}
}

// DefaultMachine is the short, lower-cased host name.
func DefaultMachine() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "machine"
	}
	h, _, _ = strings.Cut(h, ".")
	return strings.ToLower(h)
}

// Expand replaces a leading ~ with the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
