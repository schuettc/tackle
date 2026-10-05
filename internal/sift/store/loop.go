package store

// The review loop's half of the store: the agent's proposals merged into a
// round, the review's owner, Send and its delivery, and what apply did.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/schuettc/tackle/internal/sift/row"
)

// AddResult is what AddRows did.
type AddResult struct {
	Updated int // proposals merged into the round's rows
	Added   int // intake rows new to the round
	Cleared int // decisions dropped because the proposal they answered changed
}

// AddRows merges the agent's rows into a round, all or nothing. A row the
// round has gets its proposal (verdict, title, destination, text, reason)
// replaced; everything a check found stays as it was. An intake row the
// round lacks is added after the others, never certain. Anything else is an
// error: no id, an unknown verdict, an id not in the round, a decision
// (decisions are the user's), an intake row with no source. A decision on a
// row whose proposal changed is dropped: it answered another proposal.
func (s *Store) AddRows(ctx context.Context, roundID int64, in []row.Row) (AddResult, error) {
	var res AddResult
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res = AddResult{}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM rounds WHERE id = ?`, roundID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no round %d", roundID)
		}
		var seq int
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(seq), -1) + 1 FROM rows WHERE round_id = ?`, roundID).Scan(&seq); err != nil {
			return err
		}
		for i, r := range in {
			where := fmt.Sprintf("row %d", i+1)
			if r.ID != "" {
				where += " (" + r.ID + ")"
			}
			switch {
			case r.ID == "":
				return fmt.Errorf("%s: no id", where)
			case r.Decision != nil:
				return fmt.Errorf("%s: carries a decision; decisions are made on the review page", where)
			case r.Verdict != "" && !row.ValidVerdict(r.Verdict):
				return fmt.Errorf("%s: %q is not a verdict", where, r.Verdict)
			}
			var body string
			err := tx.QueryRowContext(ctx, `SELECT body FROM rows WHERE round_id = ? AND row_id = ?`, roundID, r.ID).Scan(&body)
			if errors.Is(err, sql.ErrNoRows) {
				if r.Check != "intake" {
					return fmt.Errorf("%s: not in round %d (only intake rows are new)", where, roundID)
				}
				if r.Source.Entry == "" && r.Source.File == "" {
					return fmt.Errorf("%s: an intake row needs a source (entry or file)", where)
				}
				r.Certain = false
				if err := putRow(ctx, tx, roundID, seq, r, true); err != nil {
					return err
				}
				seq++
				res.Added++
				continue
			}
			if err != nil {
				return err
			}
			var cur row.Row
			if err := json.Unmarshal([]byte(body), &cur); err != nil {
				return err
			}
			changed := cur.Verdict != r.Verdict || cur.Title != r.Title || cur.Destination != r.Destination || cur.Text != r.Text
			if r.Check == "intake" && cur.Check == "intake" {
				r.Certain = false
				cur = r // an intake row is the agent's own: all of it is replaced
			}
			cur.Verdict, cur.Title, cur.Destination, cur.Text, cur.Reason = r.Verdict, r.Title, r.Destination, r.Text, r.Reason
			if err := putRow(ctx, tx, roundID, 0, cur, false); err != nil {
				return err
			}
			res.Updated++
			if changed {
				d, err := tx.ExecContext(ctx, `DELETE FROM decisions WHERE round_id = ? AND row_id = ?`, roundID, r.ID)
				if err != nil {
					return err
				}
				if k, _ := d.RowsAffected(); k > 0 {
					res.Cleared++
				}
			}
		}
		return bump(ctx, tx, roundID)
	})
	if err != nil {
		return AddResult{}, err
	}
	return res, nil
}

func putRow(ctx context.Context, tx *sql.Tx, roundID int64, seq int, r row.Row, insert bool) error {
	r.Decision = nil
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if insert {
		_, err = tx.ExecContext(ctx, `INSERT INTO rows(round_id, seq, row_id, check_, body) VALUES (?,?,?,?,?)`, roundID, seq, r.ID, r.Check, string(b))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE rows SET body = ? WHERE round_id = ? AND row_id = ?`, string(b), roundID, r.ID)
	}
	return err
}

// SetOwner makes session (shown as label) the one the round's review goes to.
func (s *Store) SetOwner(ctx context.Context, roundID int64, session, label string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE rounds SET owner_session = ?, owner_label = ? WHERE id = ?`, session, label, roundID)
	return err
}

// Counts is what one Send carried.
type Counts struct {
	Accept int `json:"accept"`
	Edit   int `json:"edit"`
	Reject int `json:"reject"`
	// Undone counts certain rows the user undid; Applied the certain rows
	// left to apply (first send of a round only).
	Undone  int `json:"undone"`
	Applied int `json:"applied"`
}

// Total is every answer counted.
func (c Counts) Total() int { return c.Accept + c.Edit + c.Reject + c.Undone + c.Applied }

// Note is the user's note on one row, with where the row is.
type Note struct {
	Row  string `json:"row"` // file:line · check
	Note string `json:"note"`
}

// Send is one press of Send. ID is 0 when nothing was new (nothing is
// recorded then).
type Send struct {
	ID          int64
	Round       int64
	CreatedAt   time.Time
	Counts      Counts
	Notes       []Note
	Owner       string // the review's owner session at send time ("" if none)
	DeliveredTo string // "" until delivered
	DeliveredAt time.Time
}

// Send marks the round's unsent decisions sent and records one send for
// them, in one transaction. The round's first send also carries its certain
// rows nobody undid, so a round with nothing to judge can still be sent.
// With nothing new it returns a zero Send and records nothing.
func (s *Store) Send(ctx context.Context, roundID int64, owner string) (Send, error) {
	now := time.Now()
	sd := Send{Round: roundID, CreatedAt: now, Owner: owner}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var sends int
		// The first statement is a write (the main connection begins deferred).
		if _, err := tx.ExecContext(ctx, `UPDATE rounds SET rev = rev WHERE id = ?`, roundID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sends WHERE round_id = ?`, roundID).Scan(&sends); err != nil {
			return err
		}
		q, err := tx.QueryContext(ctx, `SELECT r.body, d.action, d.note, d.sent_at FROM rows r
			LEFT JOIN decisions d ON d.round_id = r.round_id AND d.row_id = r.row_id
			WHERE r.round_id = ? ORDER BY r.seq`, roundID)
		if err != nil {
			return err
		}
		var unsent []string
		for q.Next() {
			var body string
			var action, note sql.NullString
			var sent sql.NullInt64
			if err := q.Scan(&body, &action, &note, &sent); err != nil {
				_ = q.Close()
				return err
			}
			var r row.Row
			if err := json.Unmarshal([]byte(body), &r); err != nil {
				_ = q.Close()
				return err
			}
			if !action.Valid {
				if r.Certain && sends == 0 {
					sd.Counts.Applied++
				}
				continue
			}
			if sent.Int64 != 0 {
				continue
			}
			unsent = append(unsent, r.ID)
			switch {
			case r.Certain && action.String == "reject":
				sd.Counts.Undone++
			case action.String == "accept":
				sd.Counts.Accept++
			case action.String == "edit":
				sd.Counts.Edit++
			case action.String == "reject":
				sd.Counts.Reject++
			}
			if note.String != "" {
				sd.Notes = append(sd.Notes, Note{Row: where(r), Note: note.String})
			}
		}
		if err := q.Close(); err != nil {
			return err
		}
		if len(unsent) == 0 && sd.Counts.Applied == 0 {
			sd.Counts = Counts{}
			return nil
		}
		for _, id := range unsent {
			if _, err := tx.ExecContext(ctx, `UPDATE decisions SET sent_at = ? WHERE round_id = ? AND row_id = ?`, now.UnixMilli(), roundID, id); err != nil {
				return err
			}
		}
		counts, _ := json.Marshal(sd.Counts)
		notes, _ := json.Marshal(append([]Note{}, sd.Notes...))
		res, err := tx.ExecContext(ctx, `INSERT INTO sends(round_id, created_at, counts, notes, owner) VALUES (?,?,?,?,?)`,
			roundID, now.UnixMilli(), string(counts), string(notes), owner)
		if err != nil {
			return err
		}
		if sd.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return bump(ctx, tx, roundID)
	})
	if err != nil {
		return Send{}, err
	}
	return sd, nil
}

// where names a row for a note: file:line · check.
func where(r row.Row) string {
	at := r.Source.File
	if at == "" {
		at = r.Source.Entry
	}
	if r.Source.Start > 0 {
		at = fmt.Sprintf("%s:%d", at, r.Source.Start)
	}
	return at + " · " + r.Check
}

const sendCols = `id, round_id, created_at, counts, notes, owner, delivered_to, delivered_at`

type scanner interface{ Scan(...any) error }

func scanSend(r scanner) (Send, error) {
	var (
		sd           Send
		at           int64
		counts, note string
		to           sql.NullString
		dat          sql.NullInt64
	)
	if err := r.Scan(&sd.ID, &sd.Round, &at, &counts, &note, &sd.Owner, &to, &dat); err != nil {
		return Send{}, err
	}
	sd.CreatedAt, sd.DeliveredTo = time.UnixMilli(at), to.String
	if dat.Valid {
		sd.DeliveredAt = time.UnixMilli(dat.Int64)
	}
	if err := json.Unmarshal([]byte(counts), &sd.Counts); err != nil {
		return Send{}, err
	}
	if err := json.Unmarshal([]byte(note), &sd.Notes); err != nil {
		return Send{}, err
	}
	return sd, nil
}

// ClaimSend delivers to session the oldest undelivered send, marking it
// delivered in the same statement, so two claimers never both get it. serve
// decides who may claim. ok is false when nothing is undelivered.
func (s *Store) ClaimSend(ctx context.Context, session string) (Send, bool, error) {
	var sd Send
	found := true
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		sd, err = scanSend(tx.QueryRowContext(ctx, `UPDATE sends SET delivered_to = ?, delivered_at = ?
			WHERE id = (SELECT id FROM sends WHERE delivered_to IS NULL ORDER BY id LIMIT 1)
			AND delivered_to IS NULL RETURNING `+sendCols, session, time.Now().UnixMilli()))
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

// Undelivered lists the sends not yet delivered, oldest first.
func (s *Store) Undelivered(ctx context.Context) ([]Send, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT `+sendCols+` FROM sends WHERE delivered_to IS NULL ORDER BY id`)
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

// Apply is what sift apply did in one repo for a round.
type Apply struct {
	Round  int64
	Repo   string // the primary clone
	Base   string // the ref the branch was cut from
	Branch string
	PR     string // the pull request's URL, when one was opened
	// State is pr (branch pushed, PR opened), branch (committed, left
	// local), held (not touched; Detail says why) or failed.
	State  string
	Detail string
	Rows   []string // the row ids applied
	At     time.Time
}

// RecordApply stores (or replaces) what apply did in one repo.
func (s *Store) RecordApply(ctx context.Context, a Apply) error {
	if a.At.IsZero() {
		a.At = time.Now()
	}
	ids, _ := json.Marshal(append([]string{}, a.Rows...))
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO applies(round_id, repo, base, branch, pr, state, detail, rows, at) VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(round_id, repo) DO UPDATE SET base = excluded.base, branch = excluded.branch, pr = excluded.pr,
			  state = excluded.state, detail = excluded.detail, rows = excluded.rows, at = excluded.at`,
			a.Round, a.Repo, a.Base, a.Branch, a.PR, a.State, a.Detail, string(ids), a.At.UnixMilli()); err != nil {
			return err
		}
		return bump(ctx, tx, a.Round)
	})
}

// Applies lists what apply did for a round, by repo.
func (s *Store) Applies(ctx context.Context, roundID int64) ([]Apply, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT round_id, repo, base, branch, pr, state, detail, rows, at FROM applies WHERE round_id = ? ORDER BY repo`, roundID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []Apply
	for rs.Next() {
		var a Apply
		var ids string
		var at int64
		if err := rs.Scan(&a.Round, &a.Repo, &a.Base, &a.Branch, &a.PR, &a.State, &a.Detail, &ids, &at); err != nil {
			return nil, err
		}
		a.At = time.UnixMilli(at)
		if err := json.Unmarshal([]byte(ids), &a.Rows); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rs.Err()
}
