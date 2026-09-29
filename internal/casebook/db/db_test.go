package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/tools-common/sqlitedb"
)

var ctx = context.Background()

func TestFreshDatabaseMigratesToVersion2(t *testing.T) {
	d, err := Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	v, err := d.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != SchemaVersion {
		t.Fatalf("user_version %d, want %d", v, SchemaVersion)
	}
	if v != 2 {
		t.Fatalf("expected version 2, got %d", v)
	}
	// jobs table exists; insert a row
	now := int64(1000000)
	res, err := d.ExecContext(ctx, `INSERT INTO jobs(plan_json, machine, state, created_at) VALUES ('{}', 'test', 'pending', ?)`, now)
	if err != nil {
		t.Fatalf("INSERT job: %v", err)
	}
	jobID, _ := res.LastInsertId()
	// steps table exists with FK to jobs
	_, err = d.ExecContext(ctx, `INSERT INTO steps(job_id, pos, key, action, lane, command, state, updated_at) VALUES (?, 0, 'k', 'act', 'l', 'cmd', 'pending', ?)`, jobID, now)
	if err != nil {
		t.Fatalf("INSERT step: %v", err)
	}
	// FK enforced: step with bad job_id should fail
	_, err = d.ExecContext(ctx, `INSERT INTO steps(job_id, pos, key, action, lane, command, state, updated_at) VALUES (99999, 0, 'k', 'act', 'l', 'cmd', 'pending', ?)`, now)
	if err == nil {
		t.Fatal("expected FK violation, got nil")
	}
}

func TestAdoptsAP1aVersion1Database(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "casebook.db")
	// Create a db with only schemaV1 applied and user_version=1 (P1a shape).
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	sdb.SetMaxOpenConns(1)
	tx, err := sdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = sdb.Close()
	_ = os.Chmod(path, 0o600)

	// Now Open() it: no error, migrates 1->2, existing sessions table intact.
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open after P1a: %v", err)
	}
	defer func() { _ = d.Close() }()
	v, err := d.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Fatalf("version %d, want 2", v)
	}
	// sessions table from schemaV1 still intact
	var n int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&n); err != nil {
		t.Fatalf("sessions table gone: %v", err)
	}
	// jobs table now present
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM jobs").Scan(&n); err != nil {
		t.Fatalf("jobs table missing: %v", err)
	}
}

func TestNewerDatabaseIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "casebook.db")
	// Create a db with user_version=3 (future version).
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	sdb.SetMaxOpenConns(1)
	_, _ = sdb.ExecContext(ctx, "PRAGMA user_version = 3")
	_ = sdb.Close()

	_, err = Open(ctx, path)
	if err == nil {
		t.Fatal("expected error for newer database, got nil")
	}
	if ne, ok := Newer(err); !ok || ne == nil {
		t.Fatalf("Newer(%v) = %v, %v; want ok=true", err, ne, ok)
	}
}

func TestSecondInflightDeliveryIsRefusedByTheDatabase(t *testing.T) {
	d, err := Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	// Seed a session
	if _, err := d.ExecContext(ctx, `INSERT INTO sessions(id, first_seen, last_seen) VALUES ('sess1', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	now := int64(1000000)
	// First inflight delivery should succeed
	if _, err := d.ExecContext(ctx, `INSERT INTO deliveries(session_id, state, sent_at, touched_at) VALUES ('sess1', 'inflight', ?, ?)`, now, now); err != nil {
		t.Fatalf("first inflight: %v", err)
	}
	// Second inflight delivery for same session should fail (unique index)
	_, err = d.ExecContext(ctx, `INSERT INTO deliveries(session_id, state, sent_at, touched_at) VALUES ('sess1', 'inflight', ?, ?)`, now, now)
	if err == nil {
		t.Fatal("expected unique index violation for second inflight delivery, got nil")
	}
}

func TestUnversionedDatabaseWithTablesIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "casebook.db")
	// Create tables but leave user_version=0
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	sdb.SetMaxOpenConns(1)
	_, _ = sdb.ExecContext(ctx, "CREATE TABLE foo (id INTEGER PRIMARY KEY)")
	_ = sdb.Close()

	_, err = Open(ctx, path)
	if err == nil {
		t.Fatal("expected error for unversioned db with tables, got nil")
	}
	if !errors.Is(err, sqlitedb.ErrUnversioned) {
		t.Fatalf("expected ErrUnversioned, got %v", err)
	}
}

// Legacy tests preserved — they now use the new signature.

func TestOpenMigratesAndReopens(t *testing.T) {
	p := filepath.Join(t.TempDir(), "casebook.db")
	d, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := d.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != SchemaVersion {
		t.Fatalf("user_version %d, want %d", v, SchemaVersion)
	}
	for _, table := range []string{"sessions", "threads", "batches", "deliveries", "messages", "proposals", "evidence", "progress", "meta", "events", "jobs", "steps"} {
		var n int
		if err := d.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
	var mode string
	_ = d.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode %q", mode)
	}
	_ = d.Close()
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	d2, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	_ = d2.Close()
}

func TestRefusesNewerSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "casebook.db")
	d, _ := Open(ctx, p)
	_, _ = d.ExecContext(ctx, "PRAGMA user_version = 99")
	_ = d.Close()
	_, err := Open(ctx, p)
	if err == nil {
		t.Fatal("expected error for newer schema")
	}
	if ne, ok := Newer(err); !ok || ne == nil {
		t.Fatalf("Newer(%v): want ok=true", err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	d, _ := Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	defer func() { _ = d.Close() }()
	if _, err := d.ExecContext(ctx, "INSERT INTO threads(session_id, name, created_at) VALUES ('nope', 'x', 1)"); err == nil {
		t.Fatal("foreign key not enforced")
	}
}

func TestNewerHelper(t *testing.T) {
	ne := &sqlitedb.NewerError{Path: "x", Have: 5, Known: 2}
	wrapped := errors.New("wrap: " + ne.Error())
	// errors.As needs the actual NewerError wrapped
	wrapped2 := fmt.Errorf("wrap: %w", ne)
	if _, ok := Newer(wrapped); ok {
		t.Fatal("plain error should not match Newer")
	}
	if n, ok := Newer(wrapped2); !ok || n == nil {
		t.Fatal("wrapped NewerError should match")
	}
}
