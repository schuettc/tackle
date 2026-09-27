package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenMigratesAndReopens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "casebook.db")
	d, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	d.QueryRow("PRAGMA user_version").Scan(&v)
	if v != Version() {
		t.Fatalf("user_version %d, want %d", v, Version())
	}
	for _, table := range []string{"sessions", "threads", "batches", "deliveries", "messages", "proposals", "evidence", "progress", "meta", "events"} {
		var n int
		if err := d.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
	var mode string
	d.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode %q", mode)
	}
	d.Close()
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	d2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

func TestRefusesNewerSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "casebook.db")
	d, _ := Open(p)
	d.Exec("PRAGMA user_version = 99")
	d.Close()
	if _, err := Open(p); err == nil || !strings.Contains(err.Error(), "newer than this casebook") {
		t.Fatalf("got %v", err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	d, _ := Open(filepath.Join(t.TempDir(), "casebook.db"))
	defer d.Close()
	if _, err := d.Exec("INSERT INTO threads(session_id, name, created_at) VALUES ('nope', 'x', 1)"); err == nil {
		t.Fatal("foreign key not enforced")
	}
}
