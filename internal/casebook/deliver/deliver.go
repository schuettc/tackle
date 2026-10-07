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
	"github.com/schuettc/tackle/internal/casebook/propose"
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
	Worked      = "worked"      // server-generated turn-progress summary (terminal)
)

// Delivery states.
const (
	InFlight          = "inflight"
	Done              = "done"
	Released          = "released"
	Moved             = "moved"
	Stopped           = "interrupted"
	DefaultStuckAfter = 10 * time.Minute
	DefaultLeftAfter  = 60 * time.Second
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
	Busy      bool      `json:"busy"`           // a delivery is in flight
	Queued    int       `json:"queued"`         // count of queued (unsent) messages
	Left      bool      `json:"left,omitempty"` // last_seen > LeftAfter, or the harness said it ended (server-computed)
	// Name is the name the session's harness knows it by: pi's session
	// name (Court renames sessions; pi-casebook reports each rename), ""
	// when it has none (Claude Code, an unnamed pi session). The page shows
	// the folder then.
	Name string `json:"name"`
	// Worker: a session whose parent session is live in the same harness
	// process (a pi-subagents worker). Server-computed; see sessionCols.
	Worker bool `json:"worker"`
	// Eligible: the page may offer this session to Court, attach to it on
	// its own when it is the only one, and send to it: live and not a
	// worker. Server-computed.
	Eligible bool `json:"eligible"`
}

// SessionInfo is what the session's harness side (pi-casebook, in pi's own
// process) says about a session beyond presence: its name, its parent
// session, and that it ended. PID is the harness process (pi's), the same
// one the channel reports as its parent.
type SessionInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Harness string `json:"harness"`
	CWD     string `json:"cwd"`
	PID     int    `json:"pid"`
	// Parent is the session this one was started from: the session id
	// named by the header's parentSession (pi-subagents writes it into a
	// worker's header; a fork carries it too), or, for a pi-subagents
	// worker kept in memory (no header parent), the session it runs under.
	// "" when none. A session is a worker only while its parent is live in
	// the same pi process (see sessionCols).
	Parent string `json:"parent"`
	// Ended: the harness says the session has ended (pi replaced it in
	// its process: /fork, /new, /resume; or pi quit). Only ended_at
	// changes; nothing else in the report is read.
	Ended bool `json:"ended"`
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
	// Section is the page section Court asked from when nothing in it is
	// open ("rules": asking the agent to draft one). It reads "section rules",
	// like "rule <id>" and "job <n>".
	Section string `json:"section,omitempty"`
}

// Empty reports whether nothing is attached.
func (a Attached) Empty() bool {
	return len(a.Keys) == 0 && a.Open == "" && a.Rule == "" && a.Job == "" && a.Section == ""
}

// WorkedView is the progress history carried by a 'worked' message. It holds
// the total duration and the ordered list of progress lines from the turn.
type WorkedView struct {
	DurationMs int64                  `json:"duration_ms"`
	Lines      []propose.ProgressLine `json:"lines"`
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
	// WorkedJSON holds the JSON-encoded WorkedView for messages with state
	// "worked". It is not sent over the wire (json:"-"); serve decodes it
	// into the MessageView wrapper returned by GET /api/messages.
	WorkedJSON string `json:"-"`
}

// Final reports whether a message state is settled (terminal).
func Final(state string) bool {
	switch state {
	case Answered, Declined, Failed, Unanswered, Interrupted, Worked:
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
	ShownAt    time.Time `json:"shown_at,omitzero"`
	Stuck      bool      `json:"stuck"`
	Messages   []Message `json:"messages"`
}

// Queue is the delivery store over the working-state database.
type Queue struct {
	DB  *db.DB
	Now func() time.Time
	// StuckAfter overrides the default 10-minute stuck threshold. Zero means use
	// DefaultStuckAfter. Set to a shorter duration in tests.
	StuckAfter time.Duration
	// LeftAfter overrides the default 60-second left threshold. Zero means use
	// DefaultLeftAfter. Set to a shorter duration in tests.
	LeftAfter time.Duration
}

// stuckAfter returns the effective stuck threshold for this queue.
func (q *Queue) stuckAfter() time.Duration {
	if q.StuckAfter > 0 {
		return q.StuckAfter
	}
	return DefaultStuckAfter
}

// leftAfter returns the effective left threshold for this queue.
func (q *Queue) leftAfter() time.Duration {
	if q.LeftAfter > 0 {
		return q.LeftAfter
	}
	return DefaultLeftAfter
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
//
// A session the harness said ended stays ended through a beat that lands
// within LeftAfter of the end: its channel's last beats, sent before pi
// replaced it. A beat after that is the session back (a resume in another
// process) and clears the end.
func (q *Queue) Touch(ctx context.Context, s Session) error {
	now := ms(q.Now())
	_, err := q.DB.ExecContext(ctx, `INSERT INTO sessions(id, harness, label, cwd, pid, first_seen, last_seen) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?6)
		ON CONFLICT(id) DO UPDATE SET harness=excluded.harness, label=excluded.label, cwd=excluded.cwd, pid=excluded.pid, last_seen=excluded.last_seen,
			ended_at=CASE WHEN ended_at != 0 AND ?6 - ended_at <= ?7 THEN ended_at ELSE 0 END`,
		s.ID, s.Harness, s.Label, s.CWD, s.PID, now, q.leftAfter().Milliseconds())
	return err
}

// SetInfo records what the harness says about a session: its name (a
// rename replaces it; "" clears it) and its parent, and refreshes the pid,
// harness and cwd when given. The session is running (pi-casebook reports
// from inside it), so any end is cleared. It makes the row when the channel
// hasn't announced the session yet, seen now.
//
// An Ended report only marks the session ended (it is left from now on; see
// Touch), and only a session serve knows.
func (q *Queue) SetInfo(ctx context.Context, in SessionInfo) error {
	now := ms(q.Now())
	if in.Ended {
		_, err := q.DB.ExecContext(ctx, `UPDATE sessions SET ended_at = ? WHERE id = ?`, now, in.ID)
		return err
	}
	_, err := q.DB.ExecContext(ctx, `INSERT INTO sessions(id, harness, label, cwd, pid, first_seen, last_seen, name, parent) VALUES (?, ?, '', ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, parent=excluded.parent, ended_at=0,
			harness=CASE WHEN excluded.harness != '' THEN excluded.harness ELSE harness END,
			cwd=CASE WHEN excluded.cwd != '' THEN excluded.cwd ELSE cwd END,
			pid=CASE WHEN excluded.pid != 0 THEN excluded.pid ELSE pid END,
			last_seen=excluded.last_seen`,
		in.ID, in.Harness, in.CWD, in.PID, now, now, in.Name, in.Parent)
	return err
}

// Prune deletes sessions that exited longer than keep ago and that nothing
// refers to, and returns how many went. The sessions table otherwise only
// grows: every pi session, subagent worker and Claude Code session that ever
// announced itself stays.
//
// A reference is any place a session id is held (db.go's schema): a thread,
// a delivery, a progress line or its log, a message it wrote (a reply stays
// in a thread that moved on), a job approved for it, a proposal or evidence
// it made ("<harness>:<id>"), an event in the live log that names it; and
// spare, the ids the caller knows of outside the database (serve: rule
// drafts' created_by). batches and needs_you hold no session id; they reach
// one only through a thread or a job.
func (q *Queue) Prune(ctx context.Context, keep time.Duration, spare ...string) (int, error) {
	cutoff := ms(q.Now().Add(-keep))
	if spare == nil {
		spare = []string{}
	}
	spareJSON, err := json.Marshal(spare)
	if err != nil {
		return 0, err
	}
	res, err := q.DB.ExecContext(ctx, pruneSQL, cutoff, string(spareJSON))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// pruneSQL deletes the sessions Prune may: ?1 is the cutoff (unix ms), ?2
// the spare ids as a JSON array. A "<harness>:<id>" source matches on what
// follows its first colon (harness names have none).
const pruneSQL = `DELETE FROM sessions WHERE id IN (SELECT s.id FROM sessions s WHERE s.last_seen < ?1
	AND s.id NOT IN (SELECT value FROM json_each(?2))
	AND NOT EXISTS(SELECT 1 FROM threads t WHERE t.session_id = s.id)
	AND NOT EXISTS(SELECT 1 FROM deliveries d WHERE d.session_id = s.id)
	AND NOT EXISTS(SELECT 1 FROM progress p WHERE p.session_id = s.id)
	AND NOT EXISTS(SELECT 1 FROM progress_log p WHERE p.session_id = s.id)
	AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.author = s.id)
	AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.session = s.id)
	AND NOT EXISTS(SELECT 1 FROM proposals p WHERE substr(p.source, instr(p.source, ':') + 1) = s.id)
	AND NOT EXISTS(SELECT 1 FROM evidence e WHERE substr(e.author, instr(e.author, ':') + 1) = s.id)
	AND NOT EXISTS(SELECT 1 FROM events e WHERE instr(e.payload, '"' || s.id || '"') > 0 OR instr(e.payload, ':' || s.id || '"') > 0))`

// sessionCols selects a session row with its server-computed facts. Its
// one parameter is the left cutoff (unix ms): a session last seen before
// it, or one the harness said ended, has left.
//
// worker: the session's parent session is live in the same harness process
// (same pid, above 1: 0 is unknown, 1 an orphaned channel's parent).
// pi-subagents runs its workers inside the parent's pi process and names
// the parent in each worker's header. A fork names its parent too, but its
// parent runs in another pi process (a fork started with --fork), or has
// ended (pi's in-process /fork replaces it: pi-casebook reports the old
// session ended, so its last heartbeat window doesn't count). So a fork,
// even one running subagents of its own, is not a worker; its subagents
// are. Measured 2026-10-06 on Court's machine: pi 92602 hosted 01a10e0b
// (no parentSession) and four worker#<hex> sessions whose parentSession
// named it; the forked bettor-help-workspace/platform ran alone in pi 24023.
const sessionCols = `s.id, s.harness, s.label, s.cwd, s.pid, s.first_seen, s.last_seen, s.looked_at, s.name, s.ended_at != 0,
		EXISTS(SELECT 1 FROM deliveries d WHERE d.session_id = s.id AND d.state = 'inflight'),
		(s.parent != '' AND s.pid > 1 AND EXISTS(SELECT 1 FROM sessions p WHERE p.id = s.parent AND p.pid = s.pid AND p.last_seen >= ?1 AND p.ended_at = 0))`

// queuedCol counts a session's queued (unsent) messages.
const queuedCol = `COALESCE((SELECT count(*) FROM messages m JOIN threads t ON t.id = m.thread_id WHERE t.session_id = s.id AND m.state = 'queued'), 0)`

// Sessions lists every known session, most recently seen first.
// Left is computed server-side: a session is left when its last_seen is older
// than the configured LeftAfter threshold (default 60 s, spec §2.1).
func (q *Queue) Sessions(ctx context.Context) ([]Session, error) {
	now := q.Now()
	rows, err := q.DB.QueryContext(ctx, `SELECT `+sessionCols+`, `+queuedCol+`
		FROM sessions s ORDER BY s.last_seen DESC`, q.leftCutoff(now))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Session
	for rows.Next() {
		var s Session
		var fs, ls, la int64
		var ended bool
		if err := rows.Scan(&s.ID, &s.Harness, &s.Label, &s.CWD, &s.PID, &fs, &ls, &la, &s.Name, &ended, &s.Busy, &s.Worker, &s.Queued); err != nil {
			return nil, err
		}
		q.facts(&s, fs, ls, la, ended, now)
		out = append(out, s)
	}
	return out, rows.Err()
}

// MoveSession atomically moves all threads (and their queued messages) from
// session `from` to session `to`. Returns the number of threads moved and,
// if an in-flight delivery on `from` was rescued, its ID (0 if none).
//
// When an in-flight delivery exists on `from`, MoveSession marks it 'moved'
// and requeues its unsettled messages — the same approach MoveDelivery uses —
// so the target session receives them in order on its next turn.
func (q *Queue) MoveSession(ctx context.Context, from, to string) (threads int, movedDeliveryID int64, err error) {
	now := ms(q.Now())
	err = q.DB.Tx(ctx, func(tx *sql.Tx) error {
		// Rescue any in-flight delivery on `from`.
		var did int64
		qErr := tx.QueryRowContext(ctx, "SELECT id FROM deliveries WHERE session_id = ? AND state = 'inflight'", from).Scan(&did)
		if qErr != nil && !errors.Is(qErr, sql.ErrNoRows) {
			return qErr
		}
		if did != 0 {
			// Mark the delivery moved (same as MoveDelivery).
			if _, err := tx.ExecContext(ctx, "UPDATE deliveries SET state = 'moved', finished_at = ? WHERE id = ?", now, did); err != nil {
				return err
			}
			// Requeue its unsettled court messages so they reach the target.
			// queued_at is kept: the rescued messages were sent first, so they
			// stay first in the target's delivery.
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET state = 'queued', delivery_id = NULL, settled_at = 0
				WHERE delivery_id = ? AND author = 'court' AND state NOT IN ('answered', 'declined', 'failed')`, did); err != nil {
				return err
			}
			movedDeliveryID = did
		}
		// Move all threads from `from` to `to`.
		res, err := tx.ExecContext(ctx, "UPDATE threads SET session_id = ? WHERE session_id = ?", to, from)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		threads = int(n)
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return threads, movedDeliveryID, nil
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
	defer func() { _ = rows.Close() }()
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

const msgCols = "id, thread_id, author, body, attached, COALESCE(batch_id, 0), batch_pos, COALESCE(delivery_id, 0), COALESCE(reply_to, 0), state, created_at, queued_at, settled_at, worked_json"

func scanMessage(sc interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var att string
	var c, qd, st int64
	if err := sc.Scan(&m.ID, &m.ThreadID, &m.Author, &m.Body, &att, &m.BatchID, &m.BatchPos, &m.DeliveryID, &m.ReplyTo, &m.State, &c, &qd, &st, &m.WorkedJSON); err != nil {
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
	defer func() { _ = rows.Close() }()
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

// Underway lists Court's messages sent and not yet settled (queued,
// delivered, received or being worked on: no draft, nothing final), each
// with the session its thread belongs to, oldest first.
func (q *Queue) Underway(ctx context.Context) ([]UnderwayMessage, error) {
	rows, err := q.DB.QueryContext(ctx, `SELECT m.id, m.body, m.attached, t.session_id
		FROM messages m JOIN threads t ON t.id = m.thread_id
		WHERE m.author = 'court' AND m.state IN (?, ?, ?, ?) ORDER BY m.id`,
		Queued, Delivered, Received, Working)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []UnderwayMessage
	for rows.Next() {
		var u UnderwayMessage
		var att string
		if err := rows.Scan(&u.ID, &u.Body, &att, &u.Session); err != nil {
			return nil, err
		}
		if att != "" {
			_ = json.Unmarshal([]byte(att), &u.Attached)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UnderwayMessage is one of Underway's messages.
type UnderwayMessage struct {
	ID       int64
	Body     string
	Attached Attached
	Session  string
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

// PostWorked inserts a server-generated 'worked' message into a thread,
// recording a turn's progress history. The message is immediately in the
// Worked (terminal) state with no delivery or batch.
func (q *Queue) PostWorked(ctx context.Context, thread int64, author, body, workedJSON string) (Message, error) {
	now := ms(q.Now())
	res, err := q.DB.ExecContext(ctx,
		`INSERT INTO messages(thread_id, author, body, attached, state, created_at, worked_json)
		 VALUES (?, ?, ?, '', ?, ?, ?)`,
		thread, author, body, Worked, now, workedJSON)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
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

// Stuck lists the deliveries in flight and stuck (untouched for
// StuckAfter, as Delivery computes it), by id, with their sessions.
func (q *Queue) Stuck(ctx context.Context) (map[int64]string, error) {
	cutoff := ms(q.Now().Add(-q.stuckAfter()))
	rows, err := q.DB.QueryContext(ctx, "SELECT id, session_id FROM deliveries WHERE state = 'inflight' AND touched_at < ?", cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var session string
		if err := rows.Scan(&id, &session); err != nil {
			return nil, err
		}
		out[id] = session
	}
	return out, rows.Err()
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
	var sent, touched, fin, shown int64
	err := q.DB.QueryRowContext(ctx, "SELECT id, session_id, state, sent_at, touched_at, finished_at, shown_at FROM deliveries WHERE id = ?", id).
		Scan(&d.ID, &d.SessionID, &d.State, &sent, &touched, &fin, &shown)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.SentAt, d.TouchedAt, d.FinishedAt, d.ShownAt = tm(sent), tm(touched), tm(fin), tm(shown)
	d.Stuck = d.State == InFlight && q.Now().Sub(d.TouchedAt) > q.stuckAfter()
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
		// Commit the delivery as 'inflight' and mark its messages 'delivered'.
		// Semantics: a delivery is marked delivered when serve hands it to the
		// channel's long-poll, before the agent confirms receipt. If that
		// response is lost the delivery stays in flight until the agent settles
		// it, Court releases/moves it (stuck after StuckAfter), or serve
		// restarts (→ interrupted state, eligible for resend).
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

// SkippedMessage reports why one id was not changed by Reply.
type SkippedMessage struct {
	ID     int64  `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// Reply is the agent settling (or updating) messages from its deliveries.
// state is received, working, answered, declined or failed. When text is set
// it is added to the thread as the agent's reply to the first id. A delivery
// ends once every message in it is settled.
//
// Late replies (to unanswered or interrupted messages from this session) are
// accepted when state is a final state (answered, declined, failed).
// Reply settles messages and optionally posts an agent reply text. The optional
// replyAttached argument (at most one) sets attached items on the reply message;
// callers that omit it get the existing behaviour (no attached).
func (q *Queue) Reply(ctx context.Context, session string, ids []int64, state, text string, replyAttached ...Attached) ([]int64, []SkippedMessage, error) {
	var att Attached
	if len(replyAttached) > 0 {
		att = replyAttached[0]
	}
	switch state {
	case Received, Working, Answered, Declined, Failed:
	default:
		return nil, nil, fmt.Errorf("state %q: want received, working, answered, declined or failed", state)
	}
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("no message ids")
	}
	now := ms(q.Now())
	var touched []int64
	var skipped []SkippedMessage
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
			// Set thread from the first id that belongs to this session.
			if thread == 0 {
				thread = tid
			}
			switch st {
			case Answered, Declined, Failed:
				// Already settled; skip.
				skipped = append(skipped, SkippedMessage{ID: id, State: st, Reason: "already " + st})
			case Unanswered, Interrupted:
				// Late reply: only final states are accepted; non-final states are not meaningful.
				if !Final(state) {
					skipped = append(skipped, SkippedMessage{ID: id, State: st, Reason: "not settleable with state " + state})
					continue
				}
				// Update message state and settled_at; don't touch the delivery (it has already ended).
				if _, err := tx.ExecContext(ctx, "UPDATE messages SET state = ?, settled_at = ? WHERE id = ?", state, now, id); err != nil {
					return err
				}
				touched = append(touched, id)
			default:
				// In-flight message: update normally and track delivery.
				settled := int64(0)
				if Final(state) {
					settled = now
				}
				if _, err := tx.ExecContext(ctx, "UPDATE messages SET state = ?, settled_at = ? WHERE id = ?", state, settled, id); err != nil {
					return err
				}
				touched = append(touched, id)
				deliveries[did] = true
			}
		}
		if text != "" && thread != 0 {
			attJSON := ""
			if !att.Empty() {
				b, _ := json.Marshal(att)
				attJSON = string(b)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO messages(thread_id, author, body, attached, reply_to, state, created_at) VALUES (?, ?, ?, ?, ?, 'reply', ?)`,
				thread, session, text, attJSON, ids[0], now); err != nil {
				return err
			}
		}
		for did := range deliveries {
			// touched_at always refreshed; shown_at stamped on first reply (agent evidently saw it).
			if _, err := tx.ExecContext(ctx,
				"UPDATE deliveries SET touched_at = ?, shown_at = CASE WHEN shown_at = 0 THEN ? ELSE shown_at END WHERE id = ?",
				now, now, did); err != nil {
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
	return touched, skipped, err
}

// closeDelivery is the tx-scoped core of ending a delivery: the delivery row
// transitions from 'inflight' to dState (finished_at = now) and all unsettled
// messages take msgState (settled_at = now). Returns ErrNotFound when the
// delivery is not inflight. Used by both end() and Settled() so the two NOT IN
// lists stay identical.
func closeDelivery(ctx context.Context, tx *sql.Tx, now int64, id int64, dState, msgState string) error {
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
}

// end finishes a delivery: unsettled messages take msgState, the delivery
// takes dState.
func (q *Queue) end(ctx context.Context, id int64, dState, msgState string) error {
	now := ms(q.Now())
	return q.DB.Tx(ctx, func(tx *sql.Tx) error {
		return closeDelivery(ctx, tx, now, id, dState, msgState)
	})
}

// Settled is the harness reporting the session's turn ended. shownIDs lists
// the delivery ids the agent was shown this turn (for this session only);
// shown_at is recorded for each. The in-flight delivery is then ended (unsettled
// messages → unanswered) only if it has been shown (shown_at > 0). An unshown
// delivery stays in flight — it will be shown in the next turn and that turn's
// end will settle it. Returns the ended delivery, or nil if nothing was ended.
func (q *Queue) Settled(ctx context.Context, session string, shownIDs []int64) (*Delivery, error) {
	now := ms(q.Now())
	var endedID int64

	err := q.DB.Tx(ctx, func(tx *sql.Tx) error {
		// Record shown_at (and refresh touched_at) for each provided delivery id
		// that belongs to this session. Refreshing touched_at here means the
		// stuck clock counts from when the agent was shown the delivery, not
		// from when it was sent (which can be StuckAfter ago if the agent was
		// mid-turn when it arrived).
		for _, id := range shownIDs {
			if _, err := tx.ExecContext(ctx,
				"UPDATE deliveries SET shown_at = ?, touched_at = ? WHERE id = ? AND session_id = ? AND shown_at = 0",
				now, now, id, session); err != nil {
				return err
			}
		}

		// Find the in-flight delivery and its shown status.
		var id int64
		var shownAt int64
		err := tx.QueryRowContext(ctx,
			"SELECT id, shown_at FROM deliveries WHERE session_id = ? AND state = 'inflight'",
			session).Scan(&id, &shownAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // nothing in flight
		}
		if err != nil {
			return err
		}

		// Only end the delivery if it has been shown to the agent.
		if shownAt == 0 {
			return nil // unshown: stays in flight for the next turn
		}

		// End the delivery via the shared helper (same NOT IN list as end()).
		if err := closeDelivery(ctx, tx, now, id, Done, Unanswered); err != nil {
			return err
		}
		endedID = id
		return nil
	})
	if err != nil || endedID == 0 {
		return nil, err
	}
	out, err := q.Delivery(ctx, endedID)
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
		// queued_at is kept, so moved messages keep their place in line.
		_, err = tx.ExecContext(ctx, `UPDATE messages SET state = 'queued', delivery_id = NULL, settled_at = 0
			WHERE delivery_id = ? AND author = 'court' AND state NOT IN ('answered', 'declined', 'failed')`, id)
		return err
	})
}

// Interruption is one delivery Interrupt ended: whose it was, and the
// messages in it that were still unsettled (now interrupted).
type Interruption struct {
	Delivery int64
	Session  string
	Messages []int64
}

// Interrupt runs at serve start: deliveries left in flight by a previous serve
// are marked interrupted, and so are their unsettled messages. It returns
// what it interrupted, so serve can tell each session about its own
// deliveries and no others.
func (q *Queue) Interrupt(ctx context.Context) ([]Interruption, error) {
	var out []Interruption
	rows, err := q.DB.QueryContext(ctx, "SELECT id, session_id FROM deliveries WHERE state = 'inflight' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var in Interruption
		if err := rows.Scan(&in.Delivery, &in.Session); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close()
	for i := range out {
		if err := q.end(ctx, out[i].Delivery, Stopped, Interrupted); err != nil {
			return nil, err
		}
		ids, err := q.interruptedMessages(ctx, out[i].Delivery)
		if err != nil {
			return nil, err
		}
		out[i].Messages = ids
	}
	return out, nil
}

// interruptedMessages lists a delivery's interrupted messages, by id.
func (q *Queue) interruptedMessages(ctx context.Context, delivery int64) ([]int64, error) {
	rows, err := q.DB.QueryContext(ctx, "SELECT id FROM messages WHERE delivery_id = ? AND author = 'court' AND state = ? ORDER BY id", delivery, Interrupted)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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

// Session reads one session (with its queued count, as Sessions).
func (q *Queue) Session(ctx context.Context, id string) (Session, error) {
	var s Session
	var fs, ls, la int64
	var ended bool
	now := q.Now()
	err := q.DB.QueryRowContext(ctx, `SELECT `+sessionCols+`, `+queuedCol+` FROM sessions s WHERE s.id = ?2`, q.leftCutoff(now), id).
		Scan(&s.ID, &s.Harness, &s.Label, &s.CWD, &s.PID, &fs, &ls, &la, &s.Name, &ended, &s.Busy, &s.Worker, &s.Queued)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	q.facts(&s, fs, ls, la, ended, now)
	return s, nil
}

// leftCutoff is the last_seen (unix ms) before which a session has left.
func (q *Queue) leftCutoff(now time.Time) int64 { return ms(now.Add(-q.leftAfter())) }

// facts fills a scanned session's times and its server-computed flags.
func (q *Queue) facts(s *Session, fs, ls, la int64, ended bool, now time.Time) {
	s.FirstSeen, s.LastSeen, s.LookedAt = tm(fs), tm(ls), tm(la)
	s.Left = ended || now.Sub(s.LastSeen) > q.leftAfter()
	s.Eligible = !s.Left && !s.Worker
}
