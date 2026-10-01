package key

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeKey = "ts-fake-0000"

func home(t *testing.T) {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
}

func TestLoadOrder(t *testing.T) {
	home(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	if _, _, err := Load(); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
	if err := Save(fakeKey); err != nil {
		t.Fatal(err)
	}
	k, src, err := Load()
	if err != nil || k != fakeKey || src != Path() {
		t.Fatalf("file: %q %q %v", k, src, err)
	}
	t.Setenv("TYPESAFE_API_KEY", "ts-fake-env")
	k, src, err = Load()
	if err != nil || k != "ts-fake-env" || src != "environment" {
		t.Fatalf("env: %q %q %v", k, src, err)
	}
}

func TestSaveModes(t *testing.T) {
	home(t)
	if err := Save(fakeKey); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", fi, err)
	}
	di, _ := os.Stat(filepath.Dir(Path()))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
}

func TestSaveValidation(t *testing.T) {
	home(t)
	for _, bad := range []string{"", "has space", "tab\tkey", "nl\nkey", "uni✓code", strings.Repeat("a", 4097)} {
		err := Save(bad)
		if err == nil {
			t.Errorf("Save(%q) accepted", bad)
			continue
		}
		if bad != "" && strings.Contains(err.Error(), bad) {
			t.Errorf("error leaks the key: %v", err)
		}
	}
	if _, err := os.Stat(Path()); err == nil {
		t.Error("a rejected key was written")
	}
	if err := Save(strings.Repeat("a", 4096)); err != nil {
		t.Errorf("4096 should be accepted: %v", err)
	}
}

func TestLoadTrimsFileNewline(t *testing.T) {
	home(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(fakeKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, _, err := Load(); err != nil || k != fakeKey {
		t.Fatalf("%q %v", k, err)
	}
}
