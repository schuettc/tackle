package store

// The audit round's files: each audited file with its content at the audit,
// the agent's recommendation for it, and the user's decision on that. An
// audit round is decided one file at a time; a backlog or intake round one
// row at a time (PerItem).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
)

// schemaV4: the audit round's files (content at the audit), the agent's
// recommendation per file, and the user's decision per file.
const schemaV4 = `
CREATE TABLE files (
  round_id INTEGER NOT NULL,
  seq      INTEGER NOT NULL,
  key      TEXT NOT NULL,
  body     TEXT NOT NULL,
  PRIMARY KEY (round_id, key)
);
CREATE TABLE recs (
  round_id INTEGER NOT NULL,
  key      TEXT NOT NULL,
  body     TEXT NOT NULL,
  at       INTEGER NOT NULL,
  PRIMARY KEY (round_id, key)
);
CREATE TABLE file_decisions (
  round_id   INTEGER NOT NULL,
  key        TEXT NOT NULL,
  action     TEXT NOT NULL,
  content    TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  decided_at INTEGER NOT NULL,
  sent_at    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (round_id, key)
);
`

// State is where a round is: recommending until every file with findings
// has a recommendation (the page and its notifications wait for ready),
// then ready, sent once the user pressed Send, applied once apply wrote a
// branch. Checking is a round's state while sift check runs: the round is
// recorded in one transaction when the checks are done, so a stored round
// is never in it.
type State string

// The states, in order.
const (
	Checking     State = "checking"
	Recommending State = "recommending"
	Ready        State = "ready"
	Sent         State = "sent"
	Applied      State = "applied"
)

// ErrNotReady is returned for a decision on a round the agent is still
// recommending.
var ErrNotReady = errors.New("store: the round is still being recommended")

// PerItem reports whether a round of this kind is decided one row at a time
// (backlog and intake) rather than one file at a time (an audit).
func PerItem(kind string) bool { return kind == "backlog" || kind == "intake" }

// FileItem is one of a round's files with its recommendation and decision.
type FileItem struct {
	rec.File
	Rec      *rec.Rec
	Decision *rec.Decision
	// Fingerprint is rec.Print of the recommendation with its linked ones
	// ("" with no recommendation); a decision answers it.
	Fingerprint string
	// Group is the files decided with this one (rec.Group), itself
	// included.
	Group []string
}

// Progress is a round's state and how far the recommending got.
type Progress struct {
	State State `json:"state"`
	// Files counts the files that need a recommendation (those with
	// findings), Recommended those that have one.
	Files       int `json:"files"`
	Recommended int `json:"recommended"`
}

// RecordAudit stores an audit round: its rows and its files (each file with
// findings, and any other audited file a recommendation may move text to).
func (s *Store) RecordAudit(ctx context.Context, r Round, rows []row.Row, files []rec.File) (int64, error) {
	return s.record(ctx, r, rows, files)
}

// Files returns the round's files in the order they were recorded.
func (s *Store) Files(ctx context.Context, roundID int64) ([]FileItem, error) {
	var out []FileItem
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = filesIn(ctx, tx, roundID)
		return err
	})
	return out, err
}

func filesIn(ctx context.Context, tx *sql.Tx, roundID int64) ([]FileItem, error) {
	q, err := tx.QueryContext(ctx, `SELECT f.body, r.body, d.action, d.content, d.note, d.sent_at FROM files f
		LEFT JOIN recs r ON r.round_id = f.round_id AND r.key = f.key
		LEFT JOIN file_decisions d ON d.round_id = f.round_id AND d.key = f.key
		WHERE f.round_id = ? ORDER BY f.seq`, roundID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = q.Close() }()
	var out []FileItem
	for q.Next() {
		var body string
		var rb, action, content, note sql.NullString
		var sent sql.NullInt64
		if err := q.Scan(&body, &rb, &action, &content, &note, &sent); err != nil {
			return nil, err
		}
		var it FileItem
		if err := json.Unmarshal([]byte(body), &it.File); err != nil {
			return nil, err
		}
		if rb.Valid {
			var r rec.Rec
			if err := json.Unmarshal([]byte(rb.String), &r); err != nil {
				return nil, err
			}
			it.Rec = &r
		}
		if action.Valid {
			it.Decision = &rec.Decision{Action: action.String, Content: content.String, Note: note.String, Sent: sent.Int64 != 0}
		}
		out = append(out, it)
	}
	if err := q.Err(); err != nil {
		return nil, err
	}
	recs := recsOf(out)
	for i := range out {
		out[i].Group = rec.Group(recs, out[i].Key)
		if out[i].Rec != nil {
			out[i].Fingerprint = printOf(recs, *out[i].Rec)
		}
	}
	return out, nil
}

func recsOf(items []FileItem) map[string]rec.Rec {
	m := map[string]rec.Rec{}
	for _, it := range items {
		if it.Rec != nil {
			m[it.Key] = *it.Rec
		}
	}
	return m
}

// printOf is r's fingerprint with its linked recommendations as recs holds
// them.
func printOf(recs map[string]rec.Rec, r rec.Rec) string {
	var linked []rec.Rec
	for _, l := range r.Links {
		if o, ok := recs[l]; ok {
			linked = append(linked, o)
		}
	}
	return rec.Print(r, linked)
}

// State says where the round is.
func (s *Store) State(ctx context.Context, roundID int64) (Progress, error) {
	var p Progress
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = stateIn(ctx, tx, roundID)
		return err
	})
	return p, err
}

func stateIn(ctx context.Context, tx *sql.Tx, roundID int64) (Progress, error) {
	var p Progress
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM rounds WHERE id = ?`, roundID).Scan(&kind); err != nil {
		return p, err
	}
	var applied, sends int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM applies WHERE round_id = ? AND state IN ('pr', 'branch')`, roundID).Scan(&applied); err != nil {
		return p, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sends WHERE round_id = ?`, roundID).Scan(&sends); err != nil {
		return p, err
	}
	if PerItem(kind) {
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM rows WHERE round_id = ? AND coalesce(json_extract(body, '$.verdict'), '') = ''`, roundID).Scan(&open); err != nil {
			return p, err
		}
		p.State = Ready
		if open > 0 {
			p.State = Recommending
		}
	} else {
		items, err := filesIn(ctx, tx, roundID)
		if err != nil {
			return p, err
		}
		for _, it := range items {
			if len(it.Rows) > 0 {
				p.Files++
				if it.Rec != nil {
					p.Recommended++
				}
			}
		}
		p.State = Ready
		if p.Recommended < p.Files {
			p.State = Recommending
		}
	}
	switch {
	case p.State == Recommending:
	case applied > 0:
		p.State = Applied
	case sends > 0:
		p.State = Sent
	}
	return p, nil
}

// Next is the first file with findings that has no recommendation; nil
// when every one has one.
func (s *Store) Next(ctx context.Context, roundID int64) (*FileItem, error) {
	items, err := s.Files(ctx, roundID)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if len(it.Rows) > 0 && it.Rec == nil {
			return &it, nil
		}
	}
	return nil, nil
}

// ProposeResult is what Propose did.
type ProposeResult struct {
	Stored  int // recommendations stored
	Cleared int // decisions dropped because a recommendation they answered changed
	Left    int // files with findings still without a recommendation
}

// Propose stores a batch of recommendations, all or nothing, once
// rec.Check passes against the round's files and rows and the
// recommendations already stored. A file or link may be named by its key
// or its path. A file's earlier recommendation is replaced, and the
// decisions on its group (before and after) are dropped, sent or not: they
// answered another recommendation.
func (s *Store) Propose(ctx context.Context, roundID int64, batch []rec.Rec) (ProposeResult, error) {
	var res ProposeResult
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res = ProposeResult{}
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM rounds WHERE id = ?`, roundID).Scan(&kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("no round %d", roundID)
			}
			return err
		}
		if PerItem(kind) {
			return fmt.Errorf("round %d is a %s round, decided per item: add rows with sift rows add", roundID, kind)
		}
		items, err := filesIn(ctx, tx, roundID)
		if err != nil {
			return err
		}
		files := map[string]rec.File{}
		byPath := map[string]string{}
		for _, it := range items {
			files[it.Key] = it.File
			byPath[it.Source.File] = it.Key
		}
		resolve := func(k string) string {
			if _, ok := files[k]; ok {
				return k
			}
			if key, ok := byPath[k]; ok {
				return key
			}
			return k
		}
		in := make([]rec.Rec, len(batch))
		for i, r := range batch {
			r.File = resolve(r.File)
			r.Links = append([]string(nil), r.Links...)
			for j, l := range r.Links {
				r.Links[j] = resolve(l)
			}
			r.Findings = append([]rec.Account(nil), r.Findings...)
			in[i] = r
		}
		rows, err := rowsIn(ctx, tx, roundID)
		if err != nil {
			return err
		}
		stored := recsOf(items)
		if err := rec.Check(files, rows, stored, in); err != nil {
			return err
		}
		after := map[string]rec.Rec{}
		for k, r := range stored {
			after[k] = r
		}
		for _, r := range in {
			after[r.File] = r
		}
		drop := map[string]bool{}
		for _, r := range in {
			for _, k := range rec.Group(stored, r.File) {
				drop[k] = true
			}
			for _, k := range rec.Group(after, r.File) {
				drop[k] = true
			}
		}
		for k := range drop {
			d, err := tx.ExecContext(ctx, `DELETE FROM file_decisions WHERE round_id = ? AND key = ?`, roundID, k)
			if err != nil {
				return err
			}
			if n, _ := d.RowsAffected(); n > 0 {
				res.Cleared++
			}
		}
		now := time.Now().UnixMilli()
		for _, r := range in {
			b, err := json.Marshal(r)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO recs(round_id, key, body, at) VALUES (?,?,?,?)
				ON CONFLICT(round_id, key) DO UPDATE SET body = excluded.body, at = excluded.at`, roundID, r.File, string(b), now); err != nil {
				return err
			}
			res.Stored++
		}
		for _, it := range items {
			if _, ok := after[it.Key]; len(it.Rows) > 0 && !ok {
				res.Left++
			}
		}
		return bump(ctx, tx, roundID)
	})
	if err != nil {
		return ProposeResult{}, err
	}
	return res, nil
}

// rowsIn is the round's rows by id, as stored.
func rowsIn(ctx context.Context, tx *sql.Tx, roundID int64) (map[string]row.Row, error) {
	q, err := tx.QueryContext(ctx, `SELECT body FROM rows WHERE round_id = ?`, roundID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = q.Close() }()
	out := map[string]row.Row{}
	for q.Next() {
		var body string
		if err := q.Scan(&body); err != nil {
			return nil, err
		}
		var r row.Row
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, q.Err()
}

// DecideFile records the user's decision on a file and the files linked to
// it, which are decided together: accept or reject sets each of them (an
// accept leaves an edit on another file as it is); an edit is this file's,
// and accepts the others not edited. The note is this file's alone. prints
// holds each group file's fingerprint as the page showed it: ErrChanged
// when one is missing or differs, ErrStale when the file is not in the
// round, ErrNotReady while the round is recommending. All in one
// transaction.
func (s *Store) DecideFile(ctx context.Context, roundID int64, key string, d rec.Decision, prints map[string]string) error {
	if err := d.Validate(); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := bump(ctx, tx, roundID); err != nil {
			return err
		}
		group, items, err := groupFor(ctx, tx, roundID, key)
		if err != nil {
			return err
		}
		recs := recsOf(items)
		by := map[string]FileItem{}
		for _, it := range items {
			by[it.Key] = it
		}
		for _, k := range group {
			r, ok := recs[k]
			if !ok {
				return fmt.Errorf("%w: %s has no recommendation", ErrChanged, k)
			}
			if p := prints[k]; p == "" || p != printOf(recs, r) {
				return fmt.Errorf("%w: %s", ErrChanged, by[k].Source.File)
			}
		}
		now := time.Now().UnixMilli()
		put := func(k string, dd rec.Decision) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO file_decisions(round_id, key, action, content, note, decided_at, sent_at)
				VALUES (?,?,?,?,?,?,0)
				ON CONFLICT(round_id, key) DO UPDATE SET action = excluded.action, content = excluded.content,
				  note = excluded.note, decided_at = excluded.decided_at, sent_at = 0`,
				roundID, k, dd.Action, dd.Content, dd.Note, now)
			return err
		}
		for _, k := range group {
			cur := by[k].Decision
			switch {
			case k == key:
				if err := put(k, d); err != nil {
					return err
				}
			case d.Action == "reject":
				if err := put(k, rec.Decision{Action: "reject", Note: noteOf(cur)}); err != nil {
					return err
				}
			case cur != nil && (cur.Action == "edit" || cur.Action == "accept"):
				// An accept or edit elsewhere in the group keeps this one.
			default:
				if err := put(k, rec.Decision{Action: "accept", Note: noteOf(cur)}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func noteOf(d *rec.Decision) string {
	if d == nil {
		return ""
	}
	return d.Note
}

// groupFor checks the round is an audit round that is ready, and returns
// key's group and the round's files.
func groupFor(ctx context.Context, tx *sql.Tx, roundID int64, key string) ([]string, []FileItem, error) {
	p, err := stateIn(ctx, tx, roundID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrStale
	}
	if err != nil {
		return nil, nil, err
	}
	if p.State == Recommending {
		return nil, nil, ErrNotReady
	}
	items, err := filesIn(ctx, tx, roundID)
	if err != nil {
		return nil, nil, err
	}
	if !slices.ContainsFunc(items, func(it FileItem) bool { return it.Key == key }) {
		return nil, nil, ErrStale
	}
	return rec.Group(recsOf(items), key), items, nil
}

// UndecideFile clears the decisions on a file and the files linked to it.
func (s *Store) UndecideFile(ctx context.Context, roundID int64, key string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := bump(ctx, tx, roundID); err != nil {
			return err
		}
		group, _, err := groupFor(ctx, tx, roundID, key)
		if err != nil {
			return err
		}
		for _, k := range group {
			if _, err := tx.ExecContext(ctx, `DELETE FROM file_decisions WHERE round_id = ? AND key = ?`, roundID, k); err != nil {
				return err
			}
		}
		return nil
	})
}
