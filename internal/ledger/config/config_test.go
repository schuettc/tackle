package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func home(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("LEDGER_HOME", filepath.Join(h, "ledger-home"))
	return h
}

func TestLoadMissingIsNotInitialized(t *testing.T) {
	home(t)
	if _, err := Load(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("got %v", err)
	}
}

func TestSaveLoadRoundTripAndDefaults(t *testing.T) {
	h := home(t)
	if err := os.MkdirAll(filepath.Join(h, "dotfiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(Config{User: "schuettc", LedgerRemote: "git@github.com:schuettc/ledger-data.git", Roots: []string{"~/code"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode: %v %v", fi, err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "schuettc" || c.Machine == "" || c.SyncInterval != "30m" {
		t.Errorf("defaults: %+v", c)
	}
	if c.Roots[0] != filepath.Join(h, "code") {
		t.Errorf("~ not expanded: %v", c.Roots)
	}
	if !strings.HasPrefix(c.LedgerRepo, filepath.Join(h, "ledger-home")) {
		t.Errorf("ledger repo %q not under LEDGER_HOME", c.LedgerRepo)
	}
	var d Config
	d.Defaults()
	if len(d.Roots) != 2 || d.Roots[0] != filepath.Join(h, "GitHub") || d.Roots[1] != filepath.Join(h, "dotfiles") {
		t.Errorf("default roots %v", d.Roots)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	home(t)
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("machine = \"m\"\nmachnie = \"typo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "machnie") {
		t.Fatalf("got %v", err)
	}
}

func TestDirsLiveUnderToolHome(t *testing.T) {
	h := home(t)
	base := filepath.Join(h, "ledger-home")
	for _, p := range []string{Path(), SpoolDir(), CachePath(), SeenPath(), HooksDir()} {
		if !strings.HasPrefix(p, base) {
			t.Errorf("%s not under %s", p, base)
		}
	}
}

func TestDefaultMachineIsShortLower(t *testing.T) {
	m := DefaultMachine()
	if m == "" || strings.Contains(m, ".") || m != strings.ToLower(m) {
		t.Fatalf("machine %q", m)
	}
}
