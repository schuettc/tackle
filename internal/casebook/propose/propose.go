// Package propose holds the agent's contributions that aren't messages:
// proposals (a suggested decision waiting for Court), evidence (a finding
// attached to an item) and the live progress line. Proposals never decide:
// Court accepts, changes or rejects them (casebook workbench spec §6.4).
package propose

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/item"
)

// Proposal states.
const (
	Pending    = "pending"
	Accepted   = "accepted"   // decided as proposed
	Changed    = "changed"    // accepted, but Court decided differently
	Rejected   = "rejected"   // Court said no
	Superseded = "superseded" // replaced by a newer proposal, or Court decided directly
)

// ErrNotFound means no such proposal.
var ErrNotFound = errors.New("proposal not found")

// Proposal is one suggested decision.
type Proposal struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	Disposition string    `json:"disposition"`
	Until       string    `json:"until,omitempty"`
	Note        string    `json:"note,omitempty"`
	Source      string    `json:"source"` // "pi:<session>", "claude:<session>" or "rule:<id>"
	State       string    `json:"state"`
	Reason      string    `json:"reason,omitempty"` // rejected: Court's why; changed: what he decided instead
	CreatedAt   time.Time `json:"created_at"`
	SettledAt   time.Time `json:"settled_at,omitzero"`
}

// Evidence is a finding attached to an item.
type Evidence struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Text      string    `json:"text"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

// Progress is a session's live progress line.
type Progress struct {
	SessionID string    `json:"session_id"`
	Text      string    `json:"text"`
	N         int       `json:"n,omitempty"`
	Total     int       `json:"total,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store reads and writes proposals, evidence and progress.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// New returns a store over d.
func New(d *db.DB) *Store { return &Store{DB: d, Now: time.Now} }

func ms(t time.Time) int64 { return t.UnixMilli() }

func tm(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

const cols = "id, key, disposition, until, note, source, state, reason, created_at, settled_at"

func scan(sc interface{ Scan(...any) error }) (Proposal, error) {
	var p Proposal
	var c, s int64
	err := sc.Scan(&p.ID, &p.Key, &p.Disposition, &p.Until, &p.Note, &p.Source, &p.State, &p.Reason, &c, &s)
	p.CreatedAt, p.SettledAt = tm(c), tm(s)
	return p, err
}

func (s *Store) list(ctx context.Context, where string, args ...any) ([]Proposal, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+cols+" FROM proposals WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Propose records source's proposed decision for each key. Each is validated
// as a decision for its item kind; an invalid one is reported and skipped. A
// pending proposal from the same source for the same key is superseded.
func (s *Store) Propose(ctx context.Context, source string, keys []string, disposition, until, note string) ([]Proposal, []error) {
	var out []Proposal
	var errs []error
	now := s.Now()
	for _, raw := range keys {
		k, err := item.ParseKey(raw)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		d := item.Decision{Disposition: item.Disposition(disposition), Until: until, Note: note, DecidedBy: source, DecidedAt: now}
		if err := d.Validate(k.Kind); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", k, err))
			continue
		}
		var id int64
		err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE proposals SET state = 'superseded', settled_at = ? WHERE key = ? AND source = ? AND state = 'pending'",
				ms(now), k.String(), source); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, "INSERT INTO proposals(key, disposition, until, note, source, created_at) VALUES (?, ?, ?, ?, ?, ?)",
				k.String(), disposition, until, note, source, ms(now))
			if err != nil {
				return err
			}
			id, _ = res.LastInsertId()
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p, _ := s.Get(ctx, id)
		out = append(out, p)
	}
	return out, errs
}

// Get reads one proposal.
func (s *Store) Get(ctx context.Context, id int64) (Proposal, error) {
	p, err := scan(s.DB.QueryRowContext(ctx, "SELECT "+cols+" FROM proposals WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// Pending returns every pending proposal, newest first per key: the map holds
// the latest pending proposal for each key.
func (s *Store) Pending(ctx context.Context) (map[string]Proposal, error) {
	ps, err := s.list(ctx, "state = 'pending' ORDER BY id")
	if err != nil {
		return nil, err
	}
	out := map[string]Proposal{}
	for _, p := range ps {
		out[p.Key] = p
	}
	return out, nil
}

// ForKey lists every proposal for an item, newest first.
func (s *Store) ForKey(ctx context.Context, key string) ([]Proposal, error) {
	return s.list(ctx, "key = ? ORDER BY id DESC", key)
}

// Settle marks a pending proposal accepted, changed or rejected (reason for
// rejections).
func (s *Store) Settle(ctx context.Context, id int64, state, reason string) error {
	switch state {
	case Accepted, Changed, Rejected:
	default:
		return fmt.Errorf("state %q", state)
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE proposals SET state = ?, reason = ?, settled_at = ? WHERE id = ? AND state = 'pending'",
		state, reason, ms(s.Now()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SupersedeKey retires every pending proposal for a key except one. Pass
// except=0 to supersede all pending proposals for the key (e.g. when Court
// decides directly); pass a non-zero proposal ID to spare that one (e.g.
// when accepting one proposal among several from different sources).
func (s *Store) SupersedeKey(ctx context.Context, key string, except int64) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE proposals SET state = 'superseded', settled_at = ? WHERE key = ? AND state = 'pending' AND id != ?",
		ms(s.Now()), key, except)
	return err
}

// Tally counts how source's proposals were settled since t.
type Tally struct{ Accepted, Changed, Rejected, Pending int }

// Tally counts source's proposals settled since t, plus those still pending.
func (s *Store) Tally(ctx context.Context, source string, since time.Time) (Tally, error) {
	var t Tally
	rows, err := s.DB.QueryContext(ctx, `SELECT state, count(*) FROM proposals WHERE source = ? AND (state = 'pending' OR settled_at >= ?) GROUP BY state`,
		source, ms(since))
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return t, err
		}
		switch st {
		case Accepted:
			t.Accepted = n
		case Changed:
			t.Changed = n
		case Rejected:
			t.Rejected = n
		case Pending:
			t.Pending = n
		}
	}
	return t, rows.Err()
}

// RejectedFor returns the set of item keys for which source's proposals were
// rejected since t. Keys with rejections before t are not returned, because a
// rule re-edited after the rejection may re-propose them (spec §4.2).
func (s *Store) RejectedFor(ctx context.Context, source string, since time.Time) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx,
		"SELECT key FROM proposals WHERE source = ? AND state = 'rejected' AND settled_at >= ?",
		source, ms(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}

// Overruled returns source's proposals Court changed or rejected since t,
// newest first, at most limit of them, and how many there are in all.
func (s *Store) Overruled(ctx context.Context, source string, since time.Time, limit int) ([]Proposal, int, error) {
	const where = "source = ? AND state IN ('changed', 'rejected') AND settled_at >= ?"
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM proposals WHERE "+where, source, ms(since)).Scan(&total); err != nil {
		return nil, 0, err
	}
	ps, err := s.list(ctx, where+" ORDER BY settled_at DESC, id DESC LIMIT ?", source, ms(since), limit)
	return ps, total, err
}

// AddEvidence attaches a finding to an item.
func (s *Store) AddEvidence(ctx context.Context, key, text, author string) (Evidence, error) {
	k, err := item.ParseKey(key)
	if err != nil {
		return Evidence{}, err
	}
	if text == "" {
		return Evidence{}, fmt.Errorf("empty evidence")
	}
	now := s.Now()
	res, err := s.DB.ExecContext(ctx, "INSERT INTO evidence(key, text, author, created_at) VALUES (?, ?, ?, ?)", k.String(), text, author, ms(now))
	if err != nil {
		return Evidence{}, err
	}
	id, _ := res.LastInsertId()
	return Evidence{ID: id, Key: k.String(), Text: text, Author: author, CreatedAt: now}, nil
}

// Evidence lists an item's findings, oldest first.
func (s *Store) Evidence(ctx context.Context, key string) ([]Evidence, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, key, text, author, created_at FROM evidence WHERE key = ? ORDER BY id", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		var e Evidence
		var c int64
		if err := rows.Scan(&e.ID, &e.Key, &e.Text, &e.Author, &c); err != nil {
			return nil, err
		}
		e.CreatedAt = tm(c)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetProgress updates a session's progress line, keeping its start time.
func (s *Store) SetProgress(ctx context.Context, session, text string, n, total int) (Progress, error) {
	now := ms(s.Now())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO progress(session_id, text, n, total, started_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET text = excluded.text, n = excluded.n, total = excluded.total, updated_at = excluded.updated_at`,
		session, text, n, total, now, now)
	if err != nil {
		return Progress{}, err
	}
	p, _, err := s.Progress(ctx, session)
	return p, err
}

// Progress reads a session's progress line (ok false when there is none).
func (s *Store) Progress(ctx context.Context, session string) (Progress, bool, error) {
	var p Progress
	var st, up int64
	err := s.DB.QueryRowContext(ctx, "SELECT session_id, text, n, total, started_at, updated_at FROM progress WHERE session_id = ?", session).
		Scan(&p.SessionID, &p.Text, &p.N, &p.Total, &st, &up)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	p.StartedAt, p.UpdatedAt = tm(st), tm(up)
	return p, err == nil, err
}

// ClearProgress removes a session's line when its turn ends and returns how
// long it ran (0 if there was none).
func (s *Store) ClearProgress(ctx context.Context, session string) (time.Duration, error) {
	p, ok, err := s.Progress(ctx, session)
	if err != nil || !ok {
		return 0, err
	}
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM progress WHERE session_id = ?", session); err != nil {
		return 0, err
	}
	return p.UpdatedAt.Sub(p.StartedAt), nil
}
