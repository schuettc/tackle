// Package store is cull's machine-wide database: SQLite at
// StateDir("cull")/cull.db. It holds each check run's uncertain items and
// Court's answers to them, so serve (one process) and any number of check
// runs share one place with real transactions. last.json stays the report;
// this database sits beside it.
//
// It is a thin layer over github.com/schuettc/tools-common/sqlitedb.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	tools "github.com/schuettc/tools-common"
	"github.com/schuettc/tools-common/sqlitedb"
)

// SchemaVersion is the schema version this binary targets.
const SchemaVersion = 1

// keepRuns is how many runs per project are kept.
const keepRuns = 5

const schemaV1 = `
CREATE TABLE projects (
  id   INTEGER PRIMARY KEY AUTOINCREMENT,
  root TEXT NOT NULL UNIQUE
);
CREATE TABLE runs (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id     INTEGER NOT NULL REFERENCES projects(id),
  mode           TEXT NOT NULL DEFAULT '',
  base           TEXT NOT NULL DEFAULT '',
  questions_hash TEXT NOT NULL DEFAULT '',
  at             INTEGER NOT NULL,
  total          INTEGER NOT NULL DEFAULT 0,
  summary        TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX runs_project ON runs(project_id, id);
CREATE TABLE items (
  run_id  INTEGER NOT NULL REFERENCES runs(id),
  item_id TEXT NOT NULL,
  kind    TEXT NOT NULL,
  hash    TEXT NOT NULL,
  file    TEXT NOT NULL DEFAULT '',
  name    TEXT NOT NULL DEFAULT '',
  verdict TEXT NOT NULL DEFAULT '',
  rule    TEXT NOT NULL DEFAULT '',
  state   TEXT NOT NULL DEFAULT '',
  jev     TEXT NOT NULL DEFAULT '',
  model   TEXT NOT NULL DEFAULT '',
  rows    TEXT NOT NULL DEFAULT '',
  members TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (run_id, item_id)
);
CREATE TABLE answers (
  project_id     INTEGER NOT NULL REFERENCES projects(id),
  item_id        TEXT NOT NULL,
  hash           TEXT NOT NULL,
  kind           TEXT NOT NULL,
  value          TEXT NOT NULL,
  note           TEXT NOT NULL DEFAULT '',
  via            TEXT NOT NULL,
  blind          INTEGER NOT NULL DEFAULT 0,
  run_id         INTEGER NOT NULL,
  jev            TEXT NOT NULL DEFAULT '',
  model          TEXT NOT NULL DEFAULT '',
  questions_hash TEXT NOT NULL DEFAULT '',
  answered_at    INTEGER NOT NULL,
  sent_at        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, item_id, hash)
);
`

// Migrations is the append-only list of schema steps.
var Migrations = []sqlitedb.Step{
	sqlitedb.SQL(schemaV1),
}

// ErrStale is returned when an answer names an item (or hash) that is not in
// the run it was given against.
var ErrStale = errors.New("store: item not in that run")

// Store is an open cull database.
type Store struct{ db *sqlitedb.DB }

// Path is where the database lives: tools.StateDir("cull")/cull.db.
func Path() string { return filepath.Join(tools.StateDir("cull"), "cull.db") }

// Open opens (creating if needed) the database at path and migrates it.
func Open(ctx context.Context, path string) (*Store, error) {
	d, err := sqlitedb.Open(ctx, path, sqlitedb.Options{Migrations: Migrations})
	if err != nil {
		return nil, err
	}
	return &Store{db: d}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Project is a registered project root.
type Project struct {
	ID   int64
	Root string
}

// Project returns the project for an absolute root, creating it if new.
func (s *Store) Project(ctx context.Context, root string) (Project, error) {
	p := Project{Root: root}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO projects(root) VALUES (?)`, root); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE root = ?`, root).Scan(&p.ID)
	})
	return p, err
}

// ProjectByID returns a registered project; sql.ErrNoRows if unknown.
func (s *Store) ProjectByID(ctx context.Context, id int64) (Project, error) {
	p := Project{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT root FROM projects WHERE id = ?`, id).Scan(&p.Root)
	return p, err
}

// Item is one uncertain item of a run.
type Item struct {
	ID, Kind, Hash, File, Name string // Kind "test" | "group"
	Verdict, Rule              string // after answers were applied
	State                      json.RawMessage
	Jev                        json.RawMessage
	Model                      string
	Rows                       [][]string // groups only
	Members                    []string   // groups only
}

// Run is one check run.
type Run struct {
	ID, ProjectID             int64
	Mode, Base, QuestionsHash string
	At                        time.Time
	Total                     int // tests judged in the run
	Summary                   map[string]int
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func rawText(r json.RawMessage) string { return string(r) }

func rawFrom(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

// RecordRun stores a run and its items in one transaction, then prunes the
// project to its latest five runs (answers are never pruned).
func (s *Store) RecordRun(ctx context.Context, r Run, items []Item) (int64, error) {
	if r.At.IsZero() {
		r.At = time.Now()
	}
	sum, err := json.Marshal(r.Summary)
	if err != nil {
		return 0, err
	}
	if r.Summary == nil {
		sum = []byte("{}")
	}
	var id int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO runs(project_id, mode, base, questions_hash, at, total, summary)
			VALUES (?,?,?,?,?,?,?)`, r.ProjectID, r.Mode, r.Base, r.QuestionsHash, ms(r.At), r.Total, string(sum))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		for _, it := range items {
			var rows, members []byte
			if len(it.Rows) > 0 {
				if rows, err = json.Marshal(it.Rows); err != nil {
					return err
				}
			}
			if len(it.Members) > 0 {
				if members, err = json.Marshal(it.Members); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO items(run_id, item_id, kind, hash, file, name, verdict, rule, state, jev, model, rows, members)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, it.ID, it.Kind, it.Hash, it.File, it.Name, it.Verdict, it.Rule,
				rawText(it.State), rawText(it.Jev), it.Model, string(rows), string(members)); err != nil {
				return err
			}
		}
		old := `SELECT id FROM runs WHERE project_id = ? ORDER BY id DESC LIMIT -1 OFFSET ?`
		if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE run_id IN (`+old+`)`, r.ProjectID, keepRuns); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM runs WHERE id IN (`+old+`)`, r.ProjectID, keepRuns)
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// LatestRun returns the project's newest run and its items in insertion
// order. A project with no run yields an error wrapping sql.ErrNoRows.
func (s *Store) LatestRun(ctx context.Context, projectID int64) (Run, []Item, error) {
	var (
		r   Run
		at  int64
		sum string
		its []Item
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, project_id, mode, base, questions_hash, at, total, summary
		FROM runs WHERE project_id = ? ORDER BY id DESC LIMIT 1`, projectID).
		Scan(&r.ID, &r.ProjectID, &r.Mode, &r.Base, &r.QuestionsHash, &at, &r.Total, &sum)
	if err != nil {
		return Run{}, nil, fmt.Errorf("latest run: %w", err)
	}
	r.At = fromMS(at)
	if err := json.Unmarshal([]byte(sum), &r.Summary); err != nil {
		return Run{}, nil, fmt.Errorf("run summary: %w", err)
	}
	rs, err := s.db.QueryContext(ctx, `SELECT item_id, kind, hash, file, name, verdict, rule, state, jev, model, rows, members
		FROM items WHERE run_id = ? ORDER BY rowid`, r.ID)
	if err != nil {
		return Run{}, nil, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var it Item
		var state, jv, rows, members string
		if err := rs.Scan(&it.ID, &it.Kind, &it.Hash, &it.File, &it.Name, &it.Verdict, &it.Rule,
			&state, &jv, &it.Model, &rows, &members); err != nil {
			return Run{}, nil, err
		}
		it.State, it.Jev = rawFrom(state), rawFrom(jv)
		if rows != "" {
			if err := json.Unmarshal([]byte(rows), &it.Rows); err != nil {
				return Run{}, nil, err
			}
		}
		if members != "" {
			if err := json.Unmarshal([]byte(members), &it.Members); err != nil {
				return Run{}, nil, err
			}
		}
		its = append(its, it)
	}
	return r, its, rs.Err()
}

// Key identifies an answer: the item and the content hash it was given for.
type Key struct{ ItemID, Hash string }

// Answer is Court's answer to one item.
type Answer struct {
	ItemID, Hash, Kind, Value, Note, Via string // Value cut|keep|merge|separate; Via item|group
	Blind                                bool
	RunID                                int64
	Jev                                  json.RawMessage
	Model, QuestionsHash                 string
	AnsweredAt, SentAt                   time.Time // SentAt is zero until MarkSent
}

// Answers returns every answer of the project, sent or not.
func (s *Store) Answers(ctx context.Context, projectID int64) (map[Key]Answer, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT item_id, hash, kind, value, note, via, blind, run_id, jev, model, questions_hash, answered_at, sent_at
		FROM answers WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := map[Key]Answer{}
	for rs.Next() {
		var a Answer
		var blind, ans, sent int64
		var jv string
		if err := rs.Scan(&a.ItemID, &a.Hash, &a.Kind, &a.Value, &a.Note, &a.Via, &blind, &a.RunID, &jv, &a.Model, &a.QuestionsHash, &ans, &sent); err != nil {
			return nil, err
		}
		a.Blind, a.Jev, a.AnsweredAt, a.SentAt = blind != 0, rawFrom(jv), fromMS(ans), fromMS(sent)
		out[Key{a.ItemID, a.Hash}] = a
	}
	return out, rs.Err()
}

func validAnswer(a Answer) error {
	switch a.Value {
	case "cut", "keep", "merge", "separate":
	default:
		return fmt.Errorf("store: invalid answer value %q", a.Value)
	}
	switch a.Via {
	case "item", "group":
	default:
		return fmt.Errorf("store: invalid answer via %q", a.Via)
	}
	return nil
}

// SaveAnswers upserts answers against run runID. Every (ItemID, Hash) must be
// an item of that run (of that project), else ErrStale and nothing is stored.
// An upsert replaces the earlier answer, clears its sent mark and keeps the
// first AnsweredAt.
func (s *Store) SaveAnswers(ctx context.Context, projectID, runID int64, as []Answer) error {
	for _, a := range as {
		if err := validAnswer(a); err != nil {
			return err
		}
	}
	now := time.Now().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		// Each statement is a write (insert-select), so the transaction takes
		// the write lock at once instead of upgrading a read snapshot.
		for _, a := range as {
			blind := 0
			if a.Blind {
				blind = 1
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO answers(project_id, item_id, hash, kind, value, note, via, blind, run_id, jev, model, questions_hash, answered_at, sent_at)
				SELECT r.project_id, i.item_id, i.hash, ?, ?, ?, ?, ?, r.id, ?, ?, ?, ?, 0
				FROM items i JOIN runs r ON r.id = i.run_id
				WHERE i.run_id = ? AND r.project_id = ? AND i.item_id = ? AND i.hash = ?
				ON CONFLICT(project_id, item_id, hash) DO UPDATE SET
				  kind = excluded.kind, value = excluded.value, note = excluded.note, via = excluded.via,
				  blind = excluded.blind, run_id = excluded.run_id, jev = excluded.jev, model = excluded.model,
				  questions_hash = excluded.questions_hash, sent_at = 0`,
				a.Kind, a.Value, a.Note, a.Via, blind, rawText(a.Jev), a.Model, a.QuestionsHash, now,
				runID, projectID, a.ItemID, a.Hash)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 0 {
				return fmt.Errorf("%w: %s", ErrStale, a.ItemID)
			}
		}
		return nil
	})
}

// DeleteAnswer removes one answer (a no-op if absent).
func (s *Store) DeleteAnswer(ctx context.Context, projectID int64, k Key) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM answers WHERE project_id = ? AND item_id = ? AND hash = ?`, projectID, k.ItemID, k.Hash)
	return err
}

// MarkSent marks the project's unsent answers sent and returns how many.
func (s *Store) MarkSent(ctx context.Context, projectID int64) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE answers SET sent_at = ? WHERE project_id = ? AND sent_at = 0`, time.Now().UnixMilli(), projectID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
