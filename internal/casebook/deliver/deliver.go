// Package deliver is the turn-aware path from the page to an agent session:
// sessions (presence), threads, messages, batches and deliveries.
//
// The rule it enforces (casebook workbench spec §6.2): a session has at most one
// delivery in flight. A delivery goes out only when nothing is in flight, and
// it carries everything queued so far, in order, batches intact. It ends when
// the agent settles every message in it, when the harness reports the turn
// settled, or when Court releases it. Messages sent meanwhile wait: there is
// no interrupt.
package deliver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

// Message states.
const (
	Draft       = "draft"       // in a batch tray, not sent
	Queued      = "queued"      // waiting for the agent's turn to end
	Delivered   = "delivered"   // sent to the session
	Received    = "received"    // the agent picked it up
	Working     = "working"     // the agent is on it
	Answered    = "answered"    // settled
	Declined    = "declined"    // settled
	Failed      = "failed"      // settled
	Unanswered  = "unanswered"  // the turn ended without a reply
	Interrupted = "interrupted" // serve restarted mid-delivery
	AgentReply  = "reply"       // a message written by the agent
)

// Delivery states.
const (
	InFlight   = "inflight"
	Done       = "done"
	Released   = "released"
	Moved      = "moved"
	Stopped    = "interrupted"
	StuckAfter = 10 * time.Minute
)

// ErrNotFound means an id doesn't exist (or isn't the caller's).
var ErrNotFound = errors.New("not found")

// Session is one attached agent session.
type Session struct {
	ID        string    `json:"id"`
	Harness   string    `json:"harness"`
	Label     string    `json:"label"`
	CWD       string    `json:"cwd"`
	PID       int       `json:"pid"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	LookedAt  time.Time `json:"looked_at,omitzero"`
	Busy      bool      `json:"busy"` // a delivery is in flight
}

// Thread is a conversation with one session.
type Thread struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Attached is what a message refers to, captured when it was written, so
// "these" still means what Court meant.
type Attached struct {
	Keys []string `json:"keys,omitempty"` // selected items
	Open string   `json:"open,omitempty"` // the open item
	Rule string   `json:"rule,omitempty"`
	Job  string   `json:"job,omitempty"`
}

// Empty reports whether nothing is attached.
func (a Attached) Empty() bool {
	return len(a.Keys) == 0 && a.Open == "" && a.Rule == "" && a.Job == ""
}

// Message is one message in a thread.
type Message struct {
	ID         int64     `json:"id"`
	ThreadID   int64     `json:"thread_id"`
	Author     string    `json:"author"` // "court" or the session id
	Body       string    `json:"body"`
	Attached   Attached  `json:"attached"`
	BatchID    int64     `json:"batch_id,omitempty"`
	BatchPos   int       `json:"batch_pos,omitempty"`
	DeliveryID int64     `json:"delivery_id,omitempty"`
	ReplyTo    int64     `json:"reply_to,omitempty"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	QueuedAt   time.Time `json:"queued_at,omitzero"`
	SettledAt  time.Time `json:"settled_at,omitzero"`
}

// Final reports whether a message state is settled.
func Final(state string) bool {
	switch state {
	case Answered, Declined, Failed, Unanswered, Interrupted:
		return true
	}
	return false
}

// Delivery is a set of messages sent to a session together.
type Delivery struct {
	ID         int64     `json:"id"`
	SessionID  string    `json:"session_id"`
	State      string    `json:"state"`
	SentAt     time.Time `json:"sent_at"`
	TouchedAt  time.Time `json:"touched_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Stuck      bool      `json:"stuck"`
	Messages   []Message `json:"messages"`
}

// Queue is the delivery store over the working-state database.
type Queue struct {
	DB  *db.DB
	Now func() time.Time
}

// New returns a queue over d.
func New(d *db.DB) *Queue { return &Queue{DB: d, Now: time.Now} }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func tm(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// Touch records a session's presence (insert or refresh).
func (q *Queue) Touch(ctx context.Context, s Session) error {
	now := ms(q.Now())
	_, err := q.DB.ExecContext(ctx, `INSERT INTO sessions(id, harness, label, cwd, pid, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET harness=excluded.harness, label=excluded.label, cwd=excluded.cwd, pid=excluded.pid, last_seen=excluded.last_seen`,
		s.ID, s.Harness, s.Label, s.CWD, s.PID, now, now)
	return err
}

// Sessions lists every known session, most recently seen first.
func (q *Queue) Sessions(ctx context.Context) ([]Session, error) {
	rows, err := q.DB.QueryContext(ctx, `SELECT s.id, s.harness, s.label, s.cwd, s.pid, s.first_seen, s.last_seen, s.looked_at,
		EXISTS(SELECT 1 FROM deliveries d WHERE d.session_id = s.id AND d.state = 'inflight') FROM sessions s ORDER BY s.last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var fs, ls, la int64
		if err := rows.Scan(&s.ID, &s.Harness, &s.Label, &s.CWD, &s.PID, &fs, &ls, &la, &s.Busy); err != nil {
			return nil, err
		}
		s.FirstSeen, s.LastSeen, s.LookedAt = tm(fs), tm(ls), tm(la)
		out = append(out, s)
	}
	return out, rows.Err()
}

// NewThread starts a thread with a session.
func (q *Queue) NewThread(ctx context.Context, session, name string) (Thread, error) {
	now := q.Now()
	res, err := q.DB.ExecContext(ctx, "INSERT INTO threads(session_id, name, created_at) VALUES (?, ?, ?)", session, name, ms(now))
	if err != nil {
		return Thread{}, err
	}
	id, _ := res.LastInsertId()
	return Thread{ID: id, SessionID: session, Name: name, CreatedAt: now}, nil
}

// Threads lists a session's threads, oldest first ("" lists all).
func (q *Queue) Threads(ctx context.Context, session string) ([]Thread, error) {
	query, args := "SELECT id, session_id, name, created_at FROM threads ORDER BY id", []any{}
	if session != "" {
		query, args = "SELECT id, session_id, name, created_at FROM threads WHERE session_id = ? ORDER BY id", []any{session}
	}
	rows, err := q.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		var c int64
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Name, &c); err != nil {
			return nil, err
		}
		t.CreatedAt = tm(c)
		out = append(out, t)
	}
	return out, rows.Err()
}

// MoveThread hands a thread to another session. Its queued messages follow.
func (q *Queue) MoveThread(ctx context.Context, thread int64, session string) error {
	res, err := q.DB.ExecContext(ctx, "UPDATE threads SET session_id = ? WHERE id = ?", session, thread)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const msgCols = "id, thread_id, author, body, attached, COALESCE(batch_id, 0), batch_pos, COALESCE(delivery_id, 0), COALESCE(reply_to, 0), state, created_at, queued_at, settled_at"

func scanMessage(sc interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var att string
	var c, qd, st int64
	if err := sc.Scan(&m.ID, &m.ThreadID, &m.Author, &m.Body, &att, &m.BatchID, &m.BatchPos, &m.DeliveryID, &m.ReplyTo, &m.State, &c, &qd, &st); err != nil {
		return m, err
	}
	if att != "" {
		_ = json.Unmarshal([]byte(att), &m.Attached)
	}
	m.CreatedAt, m.QueuedAt, m.SettledAt = tm(c), tm(qd), tm(st)
	return m, nil
}

func (q *Queue) messages(ctx context.Context, where string, args ...any) ([]Message, error) {
	rows, err := q.DB.QueryContext(ctx, "SELECT "+msgCols+" FROM messages WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Messages lists a thread's messages in order.
func (q *Queue) Messages(ctx context.Context, thread int64) ([]Message, error) {
	return q.messages(ctx, "thread_id = ? ORDER BY id", thread)
}

// Message reads one message.
func (q *Queue) Message(ctx context.Context, id int64) (Message, error) {
	m, err := scanMessage(q.DB.QueryRowContext(ctx, "SELECT "+msgCols+" FROM messages WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// Post adds Court's message to a thread: queued for delivery, or, with
// toBatch, appended to the thread's draft batch (created if needed).
func (q *Queue) Post(ctx context.Context, thread int64, body string, att Attached, toBatch bool) (Message, error) {
	if strings.TrimSpace(body) == "" {
		return Message{}, fmt.Errorf("empty message")
	}
	attJSON := ""
	if !att.Empty() {
		b, _ := json.Marshal(att)
		attJSON = string(b)
	}
	now := ms(q.Now())
	var id int64
	err := q.DB.Tx(ctx, func(tx *sql.Tx) error {
		var batch sql.NullInt64
		pos, state, queued := 0, Queued, now
		if toBatch {
			var bid int64
			err := tx.QueryRowContext(ctx, "SELECT id FROM batches WHERE thread_id = ? AND state = 'draft'", thread).Scan(&bid)
			if errors.Is(err, sql.ErrNoRows) {
				res, err := tx.ExecContext(ctx, "INSERT INTO batches(thread_id, state, created_at) VALUES (?, 'draft', ?)", thread, now)
				if err != nil {
					return err
				}
				bid, _ = res.LastInsertId()
			} else if err != nil {
				return err
			}
			batch = sql.NullInt64{Int64: bid, Valid: true}
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(batch_pos), 0) + 1 FROM messages WHERE batch_id = ?", bid).Scan(&pos); err != nil {
				return err
			}
			state, queued = Draft, 0
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO messages(thread_id, author, body, attached, batch_id, batch_pos, state, created_at, queued_at)
			VALUES (?, 'court', ?, ?, ?, ?, ?, ?, ?)`, thread, body, attJSON, batch, pos, state, now, queued)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	return q.Message(ctx, id)
}

// DraftBatch returns a thread's draft batch and its messages in order (id 0
// when there is none).
func (q *Queue) DraftBatch(ctx context.Context, thread int64) (int64, []Message, error) {
	var bid int64
	err := q.DB.QueryRowContext(ctx, "SELECT id FROM batches WHERE thread_id = ? AND state = 'draft'", thread).Scan(&bid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	ms, err := q.messages(ctx, "batch_id = ? AND state = 'draft' ORDER BY batch_pos", bid)
	return bid, ms, err
}

// EditDraft changes a draft's text.
func (q *Queue) EditDraft(ctx context.Context, id int64, body string) error {
	return q.draftExec(ctx, "UPDATE messages SET body = ? WHERE id = ? AND state = 'draft'", body, id)
}

// RemoveDraft deletes a draft.
func (q *Queue) RemoveDraft(ctx context.Context, id int64) error {
	return q.draftExec(ctx, "DELETE FROM messages WHERE id = ? AND state = 'draft'", id)
}

func (q *Queue) draftExec(ctx context.Context, query string, args ...any) error {
	res, err := q.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderBatch sets a draft batch's order; ids must be exactly its drafts.
func (q *Queue) ReorderBatch(ctx context.Context, batch int64, ids []int64) error {
	return q.DB.Tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE batch_id = ? AND state = 'draft'", batch).Scan(&n); err != nil {
			return err
		}
		if n != len(ids) {
			return fmt.Errorf("reorder needs all %d drafts, got %d", n, len(ids))
		}
		for i, id := range ids {
			res, err := tx.ExecContext(ctx, "UPDATE messages SET batch_pos = ? WHERE id = ? AND batch_id = ? AND state = 'draft'", i+1, id, batch)
			if err != nil {
				return err
			}
			if k, _ := res.RowsAffected(); k == 0 {
				return fmt.Errorf("message %d is not a draft in batch %d", id, batch)
			}
		}
		return nil
	})
}

// SendBatch queues a draft batch as one unit (one queued_at, batch order).
func (q *Queue) SendBatch(ctx context.Context, batch int64) (int, error) {
	now := ms(q.Now())
	var n int64
	err := q.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE messages SET state = 'queued', queued_at = ? WHERE batch_id = ? AND state = 'draft'", now, batch)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("batch %d has no drafts", batch)
		}
		_, err = tx.ExecContext(ctx, "UPDATE batches SET state = 'sent' WHERE id = ?", batch)
		return err
	})
	return int(n), err
}

// Inflight returns the session's delivery in flight, or nil.
func (q *Queue) Inflight(ctx context.Context, session string) (*Delivery, error) {
	var id int64
	err := q.DB.QueryRowContext(ctx, "SELECT id FROM deliveries WHERE session_id = ? AND state = 'inflight'", session).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := q.Delivery(ctx, id)
	return &d, err
}

// Delivery reads one delivery with its messages.
func (q *Queue) Delivery(ctx context.Context, id int64) (Delivery, error) {
	var d Delivery
	var sent, touched, fin int64
	err := q.DB.QueryRowContext(ctx, "SELECT id, session_id, state, sent_at, touched_at, finished_at FROM deliveries WHERE id = ?", id).
		Scan(&d.ID, &d.SessionID, &d.State, &sent, &touched, &fin)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.SentAt, d.TouchedAt, d.FinishedAt = tm(sent), tm(touched), tm(fin)
	d.Stuck = d.State == InFlight && q.Now().Sub(d.TouchedAt) > StuckAfter
	d.Messages, err = q.messages(ctx, "delivery_id = ? AND author = 'court' ORDER BY queued_at, COALESCE(batch_id, 0), batch_pos, id", id)
	return d, err
}

// Pending counts a session's queued messages.
func (q *Queue) Pending(ctx context.Context, session string) (int, error) {
	var n int
	err := q.DB.QueryRowContext(ctx, `SELECT count(*) FROM messages m JOIN threads t ON t.id = m.thread_id
		WHERE t.session_id = ? AND m.state = 'queued'`, session).Scan(&n)
	return n, err
}

// Next starts a delivery for the session when none is in flight and messages
// are queued: every queued message, in order, becomes part of it. It returns
// nil when there is nothing to send or a delivery is still in flight.
func (q *Queue) Next(ctx context.Context, session string) (*Delivery, error) {
	var id int64
	now := ms(q.Now())
	err := q.DB.Tx(ctx, func(tx *sql.Tx) error {
		var busy bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM deliveries WHERE session_id = ? AND state = 'inflight')", session).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return nil
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages m JOIN threads t ON t.id = m.thread_id
			WHERE t.session_id = ? AND m.state = 'queued'`, session).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO deliveries(session_id, state, sent_at, touched_at) VALUES (?, 'inflight', ?, ?)", session, now, now)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET state = 'delivered', delivery_id = ?
			WHERE state = 'queued' AND thread_id IN (SELECT id FROM threads WHERE session_id = ?)`, id, session); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE sessions SET looked_at = ? WHERE id = ?", now, session)
		return err
	})
	if err != nil || id == 0 {
		return nil, err
	}
	d, err := q.Delivery(ctx, id)
	return &d, err
}

// Reply is the agent settling (or updating) messages from its deliveries.
// state is received, working, answered, declined or failed. When text is set
// it is added to the thread as the agent's reply to the first id. A delivery
// ends once every message in it is settled.
func (q *Queue) Reply(ctx context.Context, session string, ids []int64, state, text string) ([]int64, error) {
	switch state {
	case Received, Working, Answered, Declined, Failed:
	default:
		return nil, fmt.Errorf("state %q: want received, working, answered, declined or failed", state)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no message ids")
	}
	now := ms(q.Now())
	var touched []int64
	err := q.DB.Tx(ctx, func(tx *sql.Tx) error {
		var thread int64
		deliveries := map[int64]bool{}
		for _, id := range ids {
			var did, tid int64
			var st string
			err := tx.QueryRowContext(ctx, `SELECT COALESCE(m.delivery_id, 0), m.thread_id, m.state FROM messages m JOIN threads t ON t.id = m.thread_id
				WHERE m.id = ? AND m.author = 'court' AND t.session_id = ?`, id, session).Scan(&did, &tid, &st)
			if errors.Is(err, sql.ErrNoRows) || did == 0 {
				return fmt.Errorf("message %d: %w (not delivered to %s)", id, ErrNotFound, session)
			}
			if err != nil {
				return err
			}
			if Final(st) {
				continue
			}
			settled := int64(0)
			if Final(state) {
				settled = now
			}
			if _, err := tx.ExecContext(ctx, "UPDATE messages SET state = ?, settled_at = ? WHERE id = ?", state, settled, id); err != nil {
				return err
			}
			touched = append(touched, id)
			deliveries[did] = true
			if thread == 0 {
				thread = tid
			}
		}
		if text != "" && thread != 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO messages(thread_id, author, body, reply_to, state, created_at) VALUES (?, ?, ?, ?, 'reply', ?)`,
				thread, session, text, ids[0], now); err != nil {
				return err
			}
		}
		for did := range deliveries {
			if _, err := tx.ExecContext(ctx, "UPDATE deliveries SET touched_at = ? WHERE id = ?", now, did); err != nil {
				return err
			}
			var open int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE delivery_id = ? AND author = 'court'
				AND state NOT IN ('answered', 'declined', 'failed', 'unanswered', 'interrupted')`, did).Scan(&open); err != nil {
				return err
			}
			if open == 0 {
				if _, err := tx.ExecContext(ctx, "UPDATE deliveries SET state = 'done', finished_at = ? WHERE id = ? AND state = 'inflight'", now, did); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return touched, err
}

// end finishes a delivery: unsettled messages take msgState, the delivery
// takes dState.
func (q *Queue) end(ctx context.Context, id int64, dState, msgState string) error {
	now := ms(q.Now())
	return q.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE deliveries SET state = ?, finished_at = ? WHERE id = ? AND state = 'inflight'", dState, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `UPDATE messages SET state = ?, settled_at = ? WHERE delivery_id = ? AND author = 'court'
			AND state NOT IN ('answered', 'declined', 'failed', 'unanswered', 'interrupted')`, msgState, now, id)
		return err
	})
}

// Settled is the harness reporting the session's turn ended: its delivery in
// flight ends and anything it didn't settle becomes unanswered. No-op when
// nothing is in flight.
func (q *Queue) Settled(ctx context.Context, session string) (*Delivery, error) {
	d, err := q.Inflight(ctx, session)
	if err != nil || d == nil {
		return nil, err
	}
	if err := q.end(ctx, d.ID, Done, Unanswered); err != nil {
		return nil, err
	}
	out, err := q.Delivery(ctx, d.ID)
	return &out, err
}

// Release is Court ending a delivery by hand (a stuck turn).
func (q *Queue) Release(ctx context.Context, id int64) error {
	return q.end(ctx, id, Released, Unanswered)
}

// MoveDelivery gives a delivery's unsettled messages to another session: its
// threads move there and those messages are queued again.
func (q *Queue) MoveDelivery(ctx context.Context, id int64, session string) error {
	now := ms(q.Now())
	return q.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE deliveries SET state = 'moved', finished_at = ? WHERE id = ? AND state IN ('inflight', 'interrupted')", now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `UPDATE threads SET session_id = ? WHERE id IN (SELECT thread_id FROM messages WHERE delivery_id = ?)`, session, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE messages SET state = 'queued', delivery_id = NULL, queued_at = ?, settled_at = 0
			WHERE delivery_id = ? AND author = 'court' AND state NOT IN ('answered', 'declined', 'failed')`, now, id)
		return err
	})
}

// Interrupt runs at serve start: deliveries left in flight by a previous serve
// are marked interrupted, and so are their unsettled messages.
func (q *Queue) Interrupt(ctx context.Context) (int, error) {
	var ids []int64
	rows, err := q.DB.QueryContext(ctx, "SELECT id FROM deliveries WHERE state = 'inflight'")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := q.end(ctx, id, Stopped, Interrupted); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// Resend queues unanswered or interrupted messages again.
func (q *Queue) Resend(ctx context.Context, ids []int64) (int, error) {
	now := ms(q.Now())
	n := 0
	for _, id := range ids {
		res, err := q.DB.ExecContext(ctx, `UPDATE messages SET state = 'queued', delivery_id = NULL, queued_at = ?, settled_at = 0
			WHERE id = ? AND author = 'court' AND state IN ('unanswered', 'interrupted')`, now, id)
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	return n, nil
}

// Previous returns the session's most recent finished delivery before id
// (nil if none), used to tell the agent what it was working on.
func (q *Queue) Previous(ctx context.Context, session string, before int64) (*Delivery, error) {
	var id int64
	err := q.DB.QueryRowContext(ctx, "SELECT id FROM deliveries WHERE session_id = ? AND id < ? ORDER BY id DESC LIMIT 1", session, before).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := q.Delivery(ctx, id)
	return &d, err
}

// Thread reads one thread.
func (q *Queue) Thread(ctx context.Context, id int64) (Thread, error) {
	var t Thread
	var c int64
	err := q.DB.QueryRowContext(ctx, "SELECT id, session_id, name, created_at FROM threads WHERE id = ?", id).Scan(&t.ID, &t.SessionID, &t.Name, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.CreatedAt = tm(c)
	return t, err
}

// Session reads one session.
func (q *Queue) Session(ctx context.Context, id string) (Session, error) {
	all, err := q.Sessions(ctx)
	if err != nil {
		return Session{}, err
	}
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return Session{}, ErrNotFound
}
