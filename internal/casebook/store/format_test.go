package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/testgit"
)

func TestInitWritesCurrentFormat(t *testing.T) {
	r, _ := newStore(t)
	b, _ := r.ReadFile("casebook.toml")
	if r.Version != FormatVersion || !strings.Contains(string(b), "format_version = 2") {
		t.Fatalf("version %d, casebook.toml %q", r.Version, b)
	}
}

func TestUpgradeFromFormat1(t *testing.T) {
	r, _ := newStore(t)
	if err := os.WriteFile(filepath.Join(r.Dir, "casebook.toml"), []byte("format_version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, "pretend format 1"); err != nil {
		t.Fatal(err)
	}
	old, err := Open(r.Dir)
	if err != nil || old.Version != 1 {
		t.Fatalf("open v1: %+v %v", old, err)
	}
	up, err := old.Upgrade(ctx)
	if err != nil || !up || old.Version != 2 {
		t.Fatalf("upgrade: %v %v %d", up, err, old.Version)
	}
	if got := testgit.Git(t, r.Dir, "log", "-1", "--format=%s"); got != "upgrade casebook repo to format 2" {
		t.Errorf("commit %q", got)
	}
	again, _ := Open(r.Dir)
	if up, err := again.Upgrade(ctx); up || err != nil {
		t.Errorf("second upgrade: %v %v", up, err)
	}
}
