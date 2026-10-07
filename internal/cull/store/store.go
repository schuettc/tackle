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
	"sort"
	"time"

	tools "github.com/schuettc/tools-common"
	"github.com/schuettc/tools-common/sqlitedb"
)

// SchemaVersion is the schema version this binary targets.
const SchemaVersion = 2

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

// schemaV2 adds the project's review owner (the agent session that last opened
// its review) and the sends: one row per press of Send, kept until delivered.
const schemaV2 = `
ALTER TABLE projects ADD COLUMN owner_session TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN owner_label   TEXT NOT NULL DEFAULT '';
CREATE TABLE sends (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id   INTEGER NOT NULL REFERENCES projects(id),
  created_at   INTEGER NOT NULL,
  counts       TEXT NOT NULL,
  notes        TEXT NOT NULL,
  owner        TEXT NOT NULL DEFAULT '',
  delivered_to TEXT,
  delivered_at INTEGER
);
CREATE INDEX sends_project ON sends(project_id, id);
`

// Migrations is the append-only list of schema steps.
var Migrations = []sqlitedb.Step{
	sqlitedb.SQL(schemaV1),
	sqlitedb.SQL(schemaV2),
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
	// OwnerSession and OwnerLabel name the agent session that last opened the
	// project's review ("" when none has).
	OwnerSession, OwnerLabel string
}

// Project returns the project for an absolute root, creating it if new.
func (s *Store) Project(ctx context.Context, root string) (Project, error) {
	p := Project{Root: root}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO projects(root) VALUES (?)`, root); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT id, owner_session, owner_label FROM projects WHERE root = ?`, root).
			Scan(&p.ID, &p.OwnerSession, &p.OwnerLabel)
	})
	return p, err
}

// ProjectByID returns a registered project; sql.ErrNoRows if unknown.
// Projects lists every project, oldest first.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, root, owner_session, owner_label FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Root, &p.OwnerSession, &p.OwnerLabel); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProjectByID(ctx context.Context, id int64) (Project, error) {
	p := Project{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT root, owner_session, owner_label FROM projects WHERE id = ?`, id).
		Scan(&p.Root, &p.OwnerSession, &p.OwnerLabel)
	return p, err
}

// SetOwner makes session (shown as label) the owner of the project's review.
func (s *Store) SetOwner(ctx context.Context, projectID int64, session, label string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET owner_session = ?, owner_label = ? WHERE id = ?`, session, label, projectID)
	return err
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
	AnsweredAt, SentAt                   time.Time // SentAt is zero until Send
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
// first AnsweredAt. A "group" answer never replaces an existing "item" answer
// (that one is left as is, without error).
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
				  questions_hash = excluded.questions_hash, sent_at = 0
				WHERE excluded.via = 'item' OR answers.via = 'group'`,
				a.Kind, a.Value, a.Note, a.Via, blind, rawText(a.Jev), a.Model, a.QuestionsHash, now,
				runID, projectID, a.ItemID, a.Hash)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 0 {
				if a.Via == "group" {
					// Skipped (a single answer stands) or stale: only a missing item is stale.
					var one int
					err := tx.QueryRowContext(ctx, `SELECT 1 FROM items i JOIN runs r ON r.id = i.run_id
						WHERE i.run_id = ? AND r.project_id = ? AND i.item_id = ? AND i.hash = ?`,
						runID, projectID, a.ItemID, a.Hash).Scan(&one)
					if err == nil {
						continue
					}
					if !errors.Is(err, sql.ErrNoRows) {
						return err
					}
				}
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

// Counts is how many answers of each value one Send carried.
type Counts struct {
	Cut      int `json:"cut"`
	Keep     int `json:"keep"`
	Merge    int `json:"merge"`
	Separate int `json:"separate"`
}

// Total is every answer counted.
func (c Counts) Total() int { return c.Cut + c.Keep + c.Merge + c.Separate }

// Note is Court's note on one answer, with the test (or group) it is about.
type Note struct {
	Name string `json:"name"`
	Note string `json:"note"`
}

// Send is one press of Send: what was sent and where it went. ID is 0 when
// nothing was unsent (no row is recorded then).
type Send struct {
	ID, ProjectID int64
	CreatedAt     time.Time
	Counts        Counts
	Notes         []Note
	Owner         string // the review owner's session at send time ("" if none)
	DeliveredTo   string // "" until delivered
	DeliveredAt   time.Time
}

// Send marks the project's unsent answers sent and records one send row for
// them, in one transaction. owner is the review owner's session at this
// moment. With nothing unsent it returns a zero Send (ID 0) and records
// nothing.
func (s *Store) Send(ctx context.Context, projectID int64, owner string) (Send, error) {
	now := time.Now()
	sd := Send{ProjectID: projectID, CreatedAt: now, Owner: owner}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		// The first statement is a write (the main connection begins deferred).
		rs, err := tx.QueryContext(ctx, `UPDATE answers SET sent_at = ? WHERE project_id = ? AND sent_at = 0
			RETURNING item_id, run_id, value, note`, now.UnixMilli(), projectID)
		if err != nil {
			return err
		}
		type row struct {
			id, value, note string
			run             int64
		}
		var got []row
		for rs.Next() {
			var r row
			if err := rs.Scan(&r.id, &r.run, &r.value, &r.note); err != nil {
				_ = rs.Close()
				return err
			}
			got = append(got, r)
		}
		if err := rs.Err(); err != nil {
			_ = rs.Close()
			return err
		}
		if err := rs.Close(); err != nil {
			return err
		}
		if len(got) == 0 {
			return nil
		}
		sort.Slice(got, func(i, j int) bool { return got[i].id < got[j].id })
		for _, r := range got {
			switch r.value {
			case "cut":
				sd.Counts.Cut++
			case "keep":
				sd.Counts.Keep++
			case "merge":
				sd.Counts.Merge++
			case "separate":
				sd.Counts.Separate++
			}
			if r.note == "" {
				continue
			}
			name := r.id
			var n string
			if err := tx.QueryRowContext(ctx, `SELECT name FROM items WHERE run_id = ? AND item_id = ?`, r.run, r.id).Scan(&n); err == nil && n != "" {
				name = n
			}
			sd.Notes = append(sd.Notes, Note{Name: name, Note: r.note})
		}
		counts, _ := json.Marshal(sd.Counts)
		notes, _ := json.Marshal(append([]Note{}, sd.Notes...))
		res, err := tx.ExecContext(ctx, `INSERT INTO sends(project_id, created_at, counts, notes, owner) VALUES (?,?,?,?,?)`,
			projectID, now.UnixMilli(), string(counts), string(notes), owner)
		if err != nil {
			return err
		}
		sd.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Send{}, err
	}
	return sd, nil
}

const sendCols = `id, project_id, created_at, counts, notes, owner, delivered_to, delivered_at`

type scanner interface{ Scan(...any) error }

func scanSend(r scanner) (Send, error) {
	var (
		sd           Send
		at           int64
		counts, note string
		to           sql.NullString
		dat          sql.NullInt64
	)
	if err := r.Scan(&sd.ID, &sd.ProjectID, &at, &counts, &note, &sd.Owner, &to, &dat); err != nil {
		return Send{}, err
	}
	sd.CreatedAt, sd.DeliveredTo, sd.DeliveredAt = fromMS(at), to.String, fromMS(dat.Int64)
	if err := json.Unmarshal([]byte(counts), &sd.Counts); err != nil {
		return Send{}, err
	}
	if err := json.Unmarshal([]byte(note), &sd.Notes); err != nil {
		return Send{}, err
	}
	return sd, nil
}

// ClaimSend delivers to session the oldest undelivered send of the project
// that it may take, marking it delivered in the same statement, so concurrent
// claimers never both get one. It does not look at the send's owner: serve
// decides who may call (the project's current owner when present, else any
// present session of the project). ok is false when nothing is undelivered.
func (s *Store) ClaimSend(ctx context.Context, projectID int64, session string) (Send, bool, error) {
	var sd Send
	found := true
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		sd, err = scanSend(tx.QueryRowContext(ctx, `UPDATE sends SET delivered_to = ?, delivered_at = ?
			WHERE id = (SELECT id FROM sends WHERE project_id = ? AND delivered_to IS NULL
			            ORDER BY id LIMIT 1)
			AND delivered_to IS NULL RETURNING `+sendCols, session, time.Now().UnixMilli(), projectID))
		if errors.Is(err, sql.ErrNoRows) {
			found = false
			return nil
		}
		return err
	})
	if err != nil {
		return Send{}, false, err
	}
	return sd, found, nil
}

// Undelivered lists the project's sends not yet delivered, oldest first.
func (s *Store) Undelivered(ctx context.Context, projectID int64) ([]Send, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT `+sendCols+` FROM sends WHERE project_id = ? AND delivered_to IS NULL ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []Send
	for rs.Next() {
		sd, err := scanSend(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, sd)
	}
	return out, rs.Err()
}

// LatestRunIDs returns the newest run id of every project that has a run.
func (s *Store) LatestRunIDs(ctx context.Context) (map[int64]int64, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT project_id, MAX(id) FROM runs GROUP BY project_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := map[int64]int64{}
	for rs.Next() {
		var p, id int64
		if err := rs.Scan(&p, &id); err != nil {
			return nil, err
		}
		out[p] = id
	}
	return out, rs.Err()
}
