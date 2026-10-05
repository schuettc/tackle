package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/profile"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The defaults are the spec's: 8 / 6 / 10 KB budgets, a 7-day weekly window
// and 3 usage days before a new model triggers a round.
func TestDefaultsMatchTheSpec(t *testing.T) {
	d := Default()
	if d.Budgets != (Budgets{Global: 8000, Repo: 6000, Skill: 10000}) {
		t.Errorf("budgets %+v", d.Budgets)
	}
	if d.Windows != (Windows{WeeklyDays: 7, UsageDays: 3, StaleDays: 30}) {
		t.Errorf("windows %+v", d.Windows)
	}
	if len(d.Negative.Patterns) == 0 || len(d.Stale.Phrases) == 0 || len(d.Stale.Waits) == 0 {
		t.Error("no default patterns")
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFillsDefaultsAndExpandsRoots(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	p := write(t, `
profiles = ["pi"]

[[root]]
path = "~/src"
base = "main"
private = "~/src/private"

[budgets]
repo = 4000
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Budgets != (Budgets{Global: 8000, Repo: 4000, Skill: 10000}) {
		t.Errorf("budgets %+v", c.Budgets)
	}
	want := []Root{{Path: "/home/u/src", Base: "main", Private: "/home/u/src/private"}}
	if !reflect.DeepEqual(c.Roots, want) {
		t.Errorf("roots %+v", c.Roots)
	}
	ps, err := c.Enabled()
	if err != nil || len(ps) != 1 || ps[0].Name != "pi" {
		t.Errorf("enabled %+v %v", ps, err)
	}
}

func TestUnknownKeysAreAnError(t *testing.T) {
	_, err := Load(write(t, "profiles = [\"pi\"]\nbudget = 3\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown keys: budget") {
		t.Fatalf("err %v", err)
	}
	_, err = Load(write(t, "[budgets]\nglobals = 3\n"))
	if err == nil || !strings.Contains(err.Error(), "budgets.globals") {
		t.Fatalf("nested err %v", err)
	}
}

func TestMissingIsErrMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("err %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown profile":  `profiles = ["nope"]`,
		"relative root":    "[[root]]\npath = \"src\"",
		"bad pattern":      "[negative]\npatterns = [\"(\"]",
		"zero budget":      "[budgets]\nglobal = 0",
		"bad custom":       "profiles = [\"x\"]\n[[profile]]\nname = \"x\"",
		"shadowed builtin": "[[profile]]\nname = \"pi\"\nhome = \"~/.p\"\nrepo_files = [\"P.md\"]",
		"empty retired":    "[[retired]]\nname = \"store\"",
		"wait without ref": "[stale]\nwaits = [\"until it lands\"]",
		"bad wait":         "[stale]\nwaits = [\"({ref}\"]",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// A config profile sits beside the builtins and is enabled by name.
func TestCustomProfile(t *testing.T) {
	c, err := Load(write(t, `
profiles = ["claude-code", "other"]
[[profile]]
name = "other"
home = "~/.other"
repo_files = ["RULES.md"]
`))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := c.Enabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[1].Name != "other" || !reflect.DeepEqual(ps[1].RepoFiles, []string{"RULES.md"}) {
		t.Fatalf("%+v", ps)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	c := Default()
	c.Profiles = []string{"codex", "pi"}
	c.Roots = []Root{{Path: "/w"}, {Path: "/r", Base: "dev", Exclude: []string{"fixtures/**"}}}
	c.Retired = []Retired{{Name: "old-store", Patterns: []string{"memory_search", "~/.old/memory"}}}
	c.Custom = []profile.Profile{{Name: "x", Home: "~/.x", RepoFiles: []string{"X.md"}}}
	p := filepath.Join(t.TempDir(), "sift", "config.toml")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, c)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
}

// An enabled builtin carries its harness's own settings (Codex's
// config.toml); a config profile is taken as written.
func TestEnabledAppliesTheHarnessConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("project_doc_max_bytes = 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps, err := Config{Profiles: []string{"codex"}}.Enabled()
	if err != nil || len(ps) != 1 || ps[0].LoadLimit != 1000 {
		t.Fatalf("%+v %v", ps, err)
	}
}
