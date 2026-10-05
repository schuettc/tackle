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
	"slices"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/sift/rec"
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
	sqlitedb.SQL(schemaV4),
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
// is not stored and its id is 0. An audit round's files go in with
// RecordAudit.
func (s *Store) RecordRound(ctx context.Context, r Round, rows []row.Row) (int64, error) {
	return s.record(ctx, r, rows, nil)
}

func (s *Store) record(ctx context.Context, r Round, rows []row.Row, files []rec.File) (int64, error) {
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
			rw.Decision, rw.Fingerprint = nil, ""
			body, err := json.Marshal(rw)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO rows(round_id, seq, row_id, check_, body) VALUES (?,?,?,?,?)`,
				id, i, rw.ID, rw.Check, string(body)); err != nil {
				return fmt.Errorf("row %s: %w", rw.ID, err)
			}
		}
		for i, f := range files {
			f.Content = "" // never stored: it is read back at its source
			body, err := json.Marshal(f)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO files(round_id, seq, key, body) VALUES (?,?,?,?)`, id, i, f.Key, string(body)); err != nil {
				return fmt.Errorf("file %s: %w", f.Source.File, err)
			}
		}
		const old = `SELECT id FROM rounds ORDER BY id DESC LIMIT -1 OFFSET ?`
		for _, q := range []string{
			`DELETE FROM rows WHERE round_id IN (` + old + `)`,
			`DELETE FROM files WHERE round_id IN (` + old + `)`,
			`DELETE FROM recs WHERE round_id IN (` + old + `)`,
		} {
			if _, err := tx.ExecContext(ctx, q, keepRounds); err != nil {
				return err
			}
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
	rows, err := rowsOf(ctx, s.db, r.ID)
	if err != nil {
		return r, nil, err
	}
	return r, rows, nil
}

// querier is a database or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// rowsOf is a round's rows in the order they were recorded, each with its
// decision and its fingerprint.
func rowsOf(ctx context.Context, db querier, roundID int64) ([]row.Row, error) {
	q, err := db.QueryContext(ctx, `SELECT r.body, d.action, d.verdict, d.title, d.text, d.cleared, d.note, d.sent_at FROM rows r
		LEFT JOIN decisions d ON d.round_id = r.round_id AND d.row_id = r.row_id
		WHERE r.round_id = ? ORDER BY r.seq`, roundID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = q.Close() }()
	var rows []row.Row
	for q.Next() {
		var body string
		var action, verdict, title, text, cleared, note sql.NullString
		var sent sql.NullInt64
		if err := q.Scan(&body, &action, &verdict, &title, &text, &cleared, &note, &sent); err != nil {
			return nil, err
		}
		var rw row.Row
		if err := json.Unmarshal([]byte(body), &rw); err != nil {
			return nil, err
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
	if err := q.Err(); err != nil {
		return nil, err
	}
	byID := make(map[string]*row.Row, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	for i := range rows {
		rows[i].Fingerprint = rows[i].Print(byID[row.MergeTarget(rows[i].Verdict)])
	}
	return rows, nil
}

// ErrChanged is returned when a decision answers a fingerprint the row no
// longer has: the proposal changed since the page showed it.
var ErrChanged = errors.New("store: the row changed since it was shown")

// Answer is one decision on a row, given against the row's fingerprint as
// the page showed it. An edit to merge:C also carries C's fingerprint as the
// page showed it (TargetFingerprint).
type Answer struct {
	Row               string
	Fingerprint       string
	TargetFingerprint string
	Decision          row.Decision
}

// Decide records the answer to one row of a round (accept, edit or reject
// its proposal), replacing an earlier one, whatever the row holds now. The
// review page's decisions go through Answer, which checks the fingerprint.
// Both refuse to approve a merge into a row the round lacks (ErrChanged).
func (s *Store) Decide(ctx context.Context, roundID int64, rowID string, d row.Decision) error {
	_, err := s.answer(ctx, roundID, []Answer{{Row: rowID, Decision: d}}, false)
	return err
}

// Answer records decisions on a round's rows, all or nothing, each only if
// the row's fingerprint (row.Print, with its merge target) is still the
// one given, and, for an edit to merge:C, C's is still TargetFingerprint:
// ErrChanged when either is not, ErrStale when the row is not in the
// round, ErrNotReady while a round decided per item still waits for the
// agent's verdicts. The check and the write are one transaction, which
// also reads back the answered rows: the snapshot the page shows next, each
// with its decision and fingerprint.
func (s *Store) Answer(ctx context.Context, roundID int64, as []Answer) ([]row.Row, error) {
	return s.answer(ctx, roundID, as, true)
}

func (s *Store) answer(ctx context.Context, roundID int64, as []Answer, check bool) ([]row.Row, error) {
	for _, a := range as {
		if err := a.Decision.Validate(); err != nil {
			return nil, err
		}
	}
	var after []row.Row
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		// The first statement is a write (the main connection begins deferred).
		if err := bump(ctx, tx, roundID); err != nil {
			return err
		}
		// A round decided per item waits for the agent's verdict on every
		// item, as an audit round waits for every file's recommendation.
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM rounds WHERE id = ?`, roundID).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		} else if err != nil {
			return err
		}
		if PerItem(kind) {
			p, err := stateIn(ctx, tx, roundID)
			if err != nil {
				return err
			}
			if p.State == Recommending {
				return ErrNotReady
			}
		}
		for _, a := range as {
			cur, err := rowIn(ctx, tx, roundID, a.Row)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrStale
			}
			if err != nil {
				return err
			}
			if check {
				p, err := printIn(ctx, tx, roundID, cur)
				if err != nil {
					return err
				}
				if a.Fingerprint == "" || p != a.Fingerprint {
					return fmt.Errorf("%w: %s", ErrChanged, a.Row)
				}
			}
			if id := row.MergeTarget(chosen(cur, a.Decision)); id != "" {
				target, err := rowIn(ctx, tx, roundID, id)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: %s merges into %s, which is not in the round", ErrChanged, a.Row, id)
				}
				if err != nil {
					return err
				}
				if check && a.Decision.Action == "edit" && a.Decision.Verdict != "" {
					p, err := printIn(ctx, tx, roundID, target)
					if err != nil {
						return err
					}
					if a.TargetFingerprint == "" || p != a.TargetFingerprint {
						return fmt.Errorf("%w: %s (its merge target %s)", ErrChanged, a.Row, id)
					}
				}
			}
			d := a.Decision
			if _, err := tx.ExecContext(ctx, `INSERT INTO decisions(round_id, row_id, action, verdict, title, text, cleared, note, decided_at, sent_at)
				VALUES (?,?,?,?,?,?,?,?,?,0)
				ON CONFLICT(round_id, row_id) DO UPDATE SET action = excluded.action, verdict = excluded.verdict,
				  title = excluded.title, text = excluded.text, cleared = excluded.cleared, note = excluded.note,
				  decided_at = excluded.decided_at, sent_at = 0`,
				roundID, a.Row, d.Action, d.Verdict, d.Title, d.Text, strings.Join(d.Cleared, ","), d.Note, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		ids := make([]string, len(as))
		for i, a := range as {
			ids[i] = a.Row
		}
		var err error
		after, err = rowsNow(ctx, tx, roundID, ids)
		return err
	})
	if err != nil {
		return nil, err
	}
	return after, nil
}

// rowsNow is the round's rows named by ids as they are now, in the round's
// order.
func rowsNow(ctx context.Context, tx *sql.Tx, roundID int64, ids []string) ([]row.Row, error) {
	rows, err := rowsOf(ctx, tx, roundID)
	if err != nil {
		return nil, err
	}
	var out []row.Row
	for _, r := range rows {
		if slices.Contains(ids, r.ID) {
			out = append(out, r)
		}
	}
	return out, nil
}

// chosen is the verdict a decision on cur approves: the proposal's for an
// accept, the edit's own when it names one, none for a reject.
func chosen(cur row.Row, d row.Decision) string {
	switch {
	case d.Action == "reject":
		return ""
	case d.Action == "edit" && d.Verdict != "":
		return d.Verdict
	}
	return cur.Verdict
}

// printIn is r's fingerprint as the page shows it (row.Print, with r's
// merge target as the round holds it).
func printIn(ctx context.Context, tx *sql.Tx, roundID int64, r row.Row) (string, error) {
	var target *row.Row
	if id := row.MergeTarget(r.Verdict); id != "" {
		t, err := rowIn(ctx, tx, roundID, id)
		if err == nil {
			target = &t
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return r.Print(target), nil
}

// rowIn reads one row of a round as stored (no decision); sql.ErrNoRows
// when the round has no such row.
func rowIn(ctx context.Context, tx *sql.Tx, roundID int64, rowID string) (row.Row, error) {
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT body FROM rows WHERE round_id = ? AND row_id = ?`, roundID, rowID).Scan(&body); err != nil {
		return row.Row{}, err
	}
	var r row.Row
	err := json.Unmarshal([]byte(body), &r)
	return r, err
}

// Undecide removes the answer to one row (for a certain row: redoes it),
// if the row's fingerprint is still the one the page showed: ErrChanged
// when it is not, ErrStale when the row is not in the round. It returns
// the row as it is after, read in the same transaction.
func (s *Store) Undecide(ctx context.Context, roundID int64, rowID, fingerprint string) (row.Row, error) {
	var after row.Row
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := bump(ctx, tx, roundID); err != nil {
			return err
		}
		cur, err := rowIn(ctx, tx, roundID, rowID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		} else if err != nil {
			return err
		}
		p, err := printIn(ctx, tx, roundID, cur)
		if err != nil {
			return err
		}
		if fingerprint == "" || p != fingerprint {
			return fmt.Errorf("%w: %s", ErrChanged, rowID)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM decisions WHERE round_id = ? AND row_id = ?`, roundID, rowID); err != nil {
			return err
		}
		rows, err := rowsNow(ctx, tx, roundID, []string{rowID})
		if err != nil {
			return err
		}
		after = rows[0]
		return nil
	})
	return after, err
}

// Note sets the note on a row's answer, and nothing else: it approves
// nothing, so it carries no fingerprint. The answer is unsent again.
// ErrChanged when the row has no answer (cleared since the page showed it),
// ErrStale when it is not in the round.
func (s *Store) Note(ctx context.Context, roundID int64, rowID, note string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := bump(ctx, tx, roundID); err != nil {
			return err
		}
		if _, err := rowIn(ctx, tx, roundID, rowID); errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		} else if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE decisions SET note = ?, sent_at = 0 WHERE round_id = ? AND row_id = ?`, note, roundID, rowID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s has no decision", ErrChanged, rowID)
		}
		return nil
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
