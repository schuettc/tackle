// Package db is casebook serve's working-state database: SQLite at
// StateDir/casebook.db, written only by casebook serve. It holds sessions,
// threads, messages, deliveries, batches, proposals, evidence, progress and
// the live event log. Durable intent (decisions, rules) lives in the casebook
// repo, never here.
//
// Family convention: modernc.org/sqlite, WAL, a 5 s busy timeout, foreign
// keys, one connection, files 0600, and schema changes as numbered
// PRAGMA user_version steps, one transaction each. A newer database than this
// binary knows is refused.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DB is an open working-state database.
type DB struct {
	*sql.DB
	Path string
}

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	// 1: sessions, threads, messages, deliveries, batches, proposals,
	// evidence, progress, serve's own notes (meta) and the event log.
	`
CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  harness    TEXT NOT NULL DEFAULT '',
  label      TEXT NOT NULL DEFAULT '',
  cwd        TEXT NOT NULL DEFAULT '',
  pid        INTEGER NOT NULL DEFAULT 0,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  looked_at  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE threads (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  name       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE batches (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  thread_id  INTEGER NOT NULL REFERENCES threads(id),
  state      TEXT NOT NULL DEFAULT 'draft',
  created_at INTEGER NOT NULL
);
CREATE TABLE deliveries (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id  TEXT NOT NULL REFERENCES sessions(id),
  state       TEXT NOT NULL,
  sent_at     INTEGER NOT NULL,
  touched_at  INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE messages (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  thread_id   INTEGER NOT NULL REFERENCES threads(id),
  author      TEXT NOT NULL,
  body        TEXT NOT NULL,
  attached    TEXT NOT NULL DEFAULT '',
  batch_id    INTEGER REFERENCES batches(id),
  batch_pos   INTEGER NOT NULL DEFAULT 0,
  delivery_id INTEGER REFERENCES deliveries(id),
  reply_to    INTEGER REFERENCES messages(id),
  state       TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  queued_at   INTEGER NOT NULL DEFAULT 0,
  settled_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX messages_state ON messages(state);
CREATE TABLE proposals (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  key           TEXT NOT NULL,
  disposition   TEXT NOT NULL,
  until         TEXT NOT NULL DEFAULT '',
  note          TEXT NOT NULL DEFAULT '',
  source        TEXT NOT NULL,
  state         TEXT NOT NULL DEFAULT 'pending',
  reason        TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  settled_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX proposals_key ON proposals(key, state);
CREATE TABLE evidence (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  key        TEXT NOT NULL,
  text       TEXT NOT NULL,
  author     TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX evidence_key ON evidence(key);
CREATE TABLE progress (
  session_id TEXT PRIMARY KEY REFERENCES sessions(id),
  text       TEXT NOT NULL,
  n          INTEGER NOT NULL DEFAULT 0,
  total      INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE events (
  cursor     INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
`,
}

// Version is the schema version this binary writes.
func Version() int { return len(migrations) }

// Open opens (creating if needed) the database at path and migrates it.
func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(1)
	d := &DB{DB: sdb, Path: path}
	if err := d.migrate(context.Background()); err != nil {
		sdb.Close()
		return nil, err
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0o600)
		}
	}
	return d, nil
}

func (d *DB) migrate(ctx context.Context) error {
	var v int
	if err := d.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("%s: schema version %d is newer than this casebook (%d); update casebook", d.Path, v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Tx runs fn in one transaction, committing on nil and rolling back on error.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
