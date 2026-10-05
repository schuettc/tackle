// Package store is sift's database: SQLite at tools.StateDir("sift")/sift.db.
// It holds each round's rows, the answers to them and the mutes ("keep, stop
// flagging"). It is a thin layer over github.com/schuettc/tools-common/sqlitedb.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/sift/row"
	tools "github.com/schuettc/tools-common"
	"github.com/schuettc/tools-common/sqlitedb"
)

// keepRounds is how many rounds are kept (decisions and mutes never are
// pruned).
const keepRounds = 20

const schemaV1 = `
CREATE TABLE rounds (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  kind    TEXT NOT NULL,
  at      INTEGER NOT NULL,
  summary TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE rows (
  round_id INTEGER NOT NULL REFERENCES rounds(id),
  seq      INTEGER NOT NULL,
  row_id   TEXT NOT NULL,
  check_   TEXT NOT NULL,
  body     TEXT NOT NULL,
  PRIMARY KEY (round_id, row_id)
);
CREATE TABLE decisions (
  round_id   INTEGER NOT NULL,
  row_id     TEXT NOT NULL,
  action     TEXT NOT NULL,
  verdict    TEXT NOT NULL DEFAULT '',
  title      TEXT NOT NULL DEFAULT '',
  text       TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  decided_at INTEGER NOT NULL,
  PRIMARY KEY (round_id, row_id)
);
CREATE TABLE mutes (
  row_id   TEXT PRIMARY KEY,
  muted_at INTEGER NOT NULL
);
`

// schemaV2 is the review loop: each round's revision (bumped by every change
// the page must see), the agent session the round's review goes to, when a
// decision was sent, one row per press of Send (kept until delivered), and
// what apply did per repo.
const schemaV2 = `
ALTER TABLE rounds ADD COLUMN rev INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rounds ADD COLUMN owner_session TEXT NOT NULL DEFAULT '';
ALTER TABLE rounds ADD COLUMN owner_label TEXT NOT NULL DEFAULT '';
ALTER TABLE decisions ADD COLUMN sent_at INTEGER NOT NULL DEFAULT 0;
CREATE TABLE sends (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  round_id     INTEGER NOT NULL,
  created_at   INTEGER NOT NULL,
  counts       TEXT NOT NULL,
  notes        TEXT NOT NULL,
  owner        TEXT NOT NULL DEFAULT '',
  delivered_to TEXT,
  delivered_at INTEGER
);
CREATE TABLE applies (
  round_id INTEGER NOT NULL,
  repo     TEXT NOT NULL,
  base     TEXT NOT NULL DEFAULT '',
  branch   TEXT NOT NULL DEFAULT '',
  pr       TEXT NOT NULL DEFAULT '',
  state    TEXT NOT NULL,
  detail   TEXT NOT NULL DEFAULT '',
  rows     TEXT NOT NULL DEFAULT '[]',
  at       INTEGER NOT NULL,
  PRIMARY KEY (round_id, repo)
);
`

// schemaV3: the fields an edit cleared (an edit can empty the title or
// text), and apply's latest attempt kept apart from a success, which is
// never overwritten.
const schemaV3 = `
ALTER TABLE decisions ADD COLUMN cleared TEXT NOT NULL DEFAULT '';
ALTER TABLE applies ADD COLUMN last_state TEXT NOT NULL DEFAULT '';
ALTER TABLE applies ADD COLUMN last_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE applies ADD COLUMN last_at INTEGER NOT NULL DEFAULT 0;
`

// Migrations is the append-only list of schema steps.
var Migrations = []sqlitedb.Step{
	sqlitedb.SQL(schemaV1),
	sqlitedb.SQL(schemaV2),
	sqlitedb.SQL(schemaV3),
}

// ErrStale is returned when a decision names a row that is not in the round
// it was given against.
var ErrStale = errors.New("store: row not in that round")

// Store is an open sift database.
type Store struct{ db *sqlitedb.DB }

// Path is where the database lives.
func Path() string { return filepath.Join(tools.StateDir("sift"), "sift.db") }

// Open opens (creating if needed) the database at path and migrates it.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := tools.EnsureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	d, err := sqlitedb.Open(ctx, path, sqlitedb.Options{Migrations: Migrations})
	if err != nil {
		return nil, err
	}
	return &Store{db: d}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Round is one audit (or intake) round.
type Round struct {
	ID int64
	// Kind is what started it: on-demand, weekly, new-model, intake.
	Kind    string
	At      time.Time
	Summary map[string]int // rows per check
	// Rev counts the changes to the round since it was recorded: proposals
	// merged, decisions, applies.
	Rev int64
	// OwnerSession and OwnerLabel name the agent session the review goes to
	// (the last one that opened it).
	OwnerSession, OwnerLabel string
}

// RecordRound stores a round and its rows in one transaction, then prunes
// to the latest keepRounds rounds. When the transaction rolls back the round
// is not stored and its id is 0.
func (s *Store) RecordRound(ctx context.Context, r Round, rows []row.Row) (int64, error) {
	if r.At.IsZero() {
		r.At = time.Now()
	}
	sum := []byte("{}")
	if r.Summary != nil {
		var err error
		if sum, err = json.Marshal(r.Summary); err != nil {
			return 0, err
		}
	}
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO rounds(kind, at, summary) VALUES (?,?,?)`, r.Kind, r.At.UnixMilli(), string(sum))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		for i, rw := range rows {
			if rw.Source.FromDisk() {
				rw.Source.Canon = row.Resolve(rw.Source.File)
			}
			if rw.Certain && rw.Fix == "" {
				// The check's own fix, kept apart from any later proposal.
				rw.Fix = rw.Verdict
				if rw.Fix == "" {
					rw.Fix = rw.CertainFix()
				}
			}
			body, err := json.Marshal(rw)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO rows(round_id, seq, row_id, check_, body) VALUES (?,?,?,?,?)`,
				id, i, rw.ID, rw.Check, string(body)); err != nil {
				return fmt.Errorf("row %s: %w", rw.ID, err)
			}
		}
		old := `SELECT id FROM rounds ORDER BY id DESC LIMIT -1 OFFSET ?`
		if _, err := tx.ExecContext(ctx, `DELETE FROM rows WHERE round_id IN (`+old+`)`, keepRounds); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM rounds WHERE id IN (`+old+`)`, keepRounds)
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// LatestRound returns the newest round and its rows in the order they were
// recorded, each with its decision; sql.ErrNoRows when there is none.
func (s *Store) LatestRound(ctx context.Context) (Round, []row.Row, error) {
	id, _, err := s.Latest(ctx)
	if err != nil {
		return Round{}, nil, err
	}
	return s.Round(ctx, id)
}

// Latest returns the newest round's id and revision; sql.ErrNoRows when
// there is none.
func (s *Store) Latest(ctx context.Context) (id, rev int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT id, rev FROM rounds ORDER BY id DESC LIMIT 1`).Scan(&id, &rev)
	return id, rev, err
}

// Round returns a round and its rows in the order they were recorded, each
// with its decision; sql.ErrNoRows when there is no such round.
func (s *Store) Round(ctx context.Context, id int64) (Round, []row.Row, error) {
	var r Round
	var at int64
	var sum string
	err := s.db.QueryRowContext(ctx, `SELECT id, kind, at, summary, rev, owner_session, owner_label FROM rounds WHERE id = ?`, id).
		Scan(&r.ID, &r.Kind, &at, &sum, &r.Rev, &r.OwnerSession, &r.OwnerLabel)
	if err != nil {
		return r, nil, err
	}
	r.At = time.UnixMilli(at)
	if err := json.Unmarshal([]byte(sum), &r.Summary); err != nil {
		return r, nil, err
	}
	q, err := s.db.QueryContext(ctx, `SELECT r.body, d.action, d.verdict, d.title, d.text, d.cleared, d.note, d.sent_at FROM rows r
		LEFT JOIN decisions d ON d.round_id = r.round_id AND d.row_id = r.row_id
		WHERE r.round_id = ? ORDER BY r.seq`, r.ID)
	if err != nil {
		return r, nil, err
	}
	defer func() { _ = q.Close() }()
	var rows []row.Row
	for q.Next() {
		var body string
		var action, verdict, title, text, cleared, note sql.NullString
		var sent sql.NullInt64
		if err := q.Scan(&body, &action, &verdict, &title, &text, &cleared, &note, &sent); err != nil {
			return r, nil, err
		}
		var rw row.Row
		if err := json.Unmarshal([]byte(body), &rw); err != nil {
			return r, nil, err
		}
		rw.Decision = nil
		if action.Valid {
			rw.Decision = &row.Decision{Action: action.String, Verdict: verdict.String, Title: title.String, Text: text.String,
				Note: note.String, Sent: sent.Int64 != 0}
			if cleared.String != "" {
				rw.Decision.Cleared = strings.Split(cleared.String, ",")
			}
		}
		rows = append(rows, rw)
	}
	return r, rows, q.Err()
}

// Decide records the answer to one row of a round (accept, edit or reject
// its proposal), replacing an earlier one.
func (s *Store) Decide(ctx context.Context, roundID int64, rowID string, d row.Decision) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM rows WHERE round_id = ? AND row_id = ?`, roundID, rowID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrStale
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO decisions(round_id, row_id, action, verdict, title, text, cleared, note, decided_at, sent_at)
			VALUES (?,?,?,?,?,?,?,?,?,0)
			ON CONFLICT(round_id, row_id) DO UPDATE SET action = excluded.action, verdict = excluded.verdict,
			  title = excluded.title, text = excluded.text, cleared = excluded.cleared, note = excluded.note,
			  decided_at = excluded.decided_at, sent_at = 0`,
			roundID, rowID, d.Action, d.Verdict, d.Title, d.Text, strings.Join(d.Cleared, ","), d.Note, time.Now().UnixMilli()); err != nil {
			return err
		}
		return bump(ctx, tx, roundID)
	})
}

// Undecide removes the answer to one row (for a certain row: redoes it).
func (s *Store) Undecide(ctx context.Context, roundID int64, rowID string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM rows WHERE round_id = ? AND row_id = ?`, roundID, rowID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrStale
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM decisions WHERE round_id = ? AND row_id = ?`, roundID, rowID); err != nil {
			return err
		}
		return bump(ctx, tx, roundID)
	})
}

// bump counts one change to the round, so a page watching it reloads.
func bump(ctx context.Context, tx *sql.Tx, roundID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE rounds SET rev = rev + 1 WHERE id = ?`, roundID)
	return err
}

// Mute stops flagging a row id ("keep, stop flagging"). The id hashes the
// passage, so the mute lapses when the passage changes.
func (s *Store) Mute(ctx context.Context, rowID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO mutes(row_id, muted_at) VALUES (?,?)`, rowID, time.Now().UnixMilli())
	return err
}

// Unmuted returns the rows that are not muted, in order, and how many were.
func (s *Store) Unmuted(ctx context.Context, rows []row.Row) ([]row.Row, int, error) {
	q, err := s.db.QueryContext(ctx, `SELECT row_id FROM mutes`)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = q.Close() }()
	muted := map[string]bool{}
	for q.Next() {
		var id string
		if err := q.Scan(&id); err != nil {
			return nil, 0, err
		}
		muted[id] = true
	}
	if err := q.Err(); err != nil {
		return nil, 0, err
	}
	kept := make([]row.Row, 0, len(rows))
	n := 0
	for _, r := range rows {
		if muted[r.ID] {
			n++
			continue
		}
		kept = append(kept, r)
	}
	return kept, n, nil
}
