// Package db is casebook serve's working-state database: SQLite at
// StateDir/casebook.db, written only by casebook serve. It holds sessions,
// threads, messages, deliveries, batches, proposals, evidence, progress,
// the live event log, and (from schema v2) jobs and steps. Durable intent
// (decisions, rules) lives in the casebook repo, never here.
//
// This package is a thin wrapper over github.com/schuettc/tools-common/sqlitedb,
// which implements the family conventions: modernc.org/sqlite, WAL, 5 s busy
// timeout, foreign keys, one connection, files 0600, append-only migrations.
package db

import (
	"context"
	"errors"

	"github.com/schuettc/tools-common/sqlitedb"
)

// DB is an open working-state database.
type DB = sqlitedb.DB

// SchemaVersion is the schema version this binary targets.
const SchemaVersion = 2

// schemaV1 is P1a's migration-1 SQL verbatim.
const schemaV1 = `
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
  finished_at INTEGER NOT NULL DEFAULT 0,
  shown_at    INTEGER NOT NULL DEFAULT 0
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
`

// schemaV2 adds jobs, steps, and needs_you tables, plus the unique index
// that enforces one delivery in flight per session at the database level.
const schemaV2 = `
CREATE TABLE jobs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  plan_json   TEXT NOT NULL,
  machine     TEXT NOT NULL,
  session     TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL,
  paused      INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  approved_at INTEGER NOT NULL DEFAULT 0,
  finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE steps (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id       INTEGER NOT NULL REFERENCES jobs(id),
  pos          INTEGER NOT NULL,
  key          TEXT NOT NULL,
  action       TEXT NOT NULL,
  lane         TEXT NOT NULL,
  command      TEXT NOT NULL,
  precondition TEXT NOT NULL DEFAULT '',
  posts        INTEGER NOT NULL DEFAULT 0,
  text         TEXT NOT NULL DEFAULT '',
  restore      TEXT NOT NULL DEFAULT '',
  expected_tip TEXT NOT NULL DEFAULT '',
  state        TEXT NOT NULL,
  detail       TEXT NOT NULL DEFAULT '',
  updated_at   INTEGER NOT NULL,
  verified_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX steps_job ON steps(job_id, pos);
CREATE TABLE needs_you (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id     INTEGER NOT NULL REFERENCES jobs(id),
  step_id    INTEGER REFERENCES steps(id),
  kind       TEXT NOT NULL,
  question   TEXT NOT NULL DEFAULT '',
  text       TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT 'open',
  answer     TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  answered_at INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX deliveries_one_inflight ON deliveries(session_id) WHERE state = 'inflight';
`

// Migrations is the ordered list of schema steps for this binary.
// Migrations[i] moves the database from user_version i to i+1.
var Migrations = []sqlitedb.Step{
	sqlitedb.SQL(schemaV1),
	sqlitedb.SQL(schemaV2),
}

// Open opens (creating if needed) the database at path and migrates it to
// SchemaVersion. A database at user_version 0 with tables is refused
// (AdoptUnversioned is not set: schemaV1 is not idempotent). A database
// newer than this binary knows is refused.
func Open(ctx context.Context, path string) (*DB, error) {
	return sqlitedb.Open(ctx, path, sqlitedb.Options{
		Migrations: Migrations,
	})
}

// Newer reports whether err wraps a *sqlitedb.NewerError. It is a convenience
// helper so serve can detect and report a database that is newer than this
// binary without importing the sqlitedb package directly.
func Newer(err error) (*sqlitedb.NewerError, bool) {
	var ne *sqlitedb.NewerError
	if errors.As(err, &ne) {
		return ne, true
	}
	return nil, false
}
