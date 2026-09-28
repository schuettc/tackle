// Package bus is casebook serve's live event log: every change the page must
// see (a message, a proposal, a progress line, a new index) is appended to the
// events table and pushed to subscribers. The page reads it as server-sent
// events, falling back to polling; the cursor makes a missed event
// recoverable either way (the tools-common localweb/page live() contract:
// GET <poll>?since=<cursor> → {cursor, events:[{type,data}]}, and each SSE
// event's id is its cursor).
package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

// Event is one entry in the log.
type Event struct {
	Cursor int64           `json:"-"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
}

// Bus appends events and wakes subscribers.
type Bus struct {
	db   *db.DB
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
	// Heartbeat is how often an idle stream sends a comment; tests shorten it.
	Heartbeat time.Duration
}

// New returns a bus over d.
func New(d *db.DB) *Bus {
	return &Bus{db: d, subs: map[chan struct{}]struct{}{}, Heartbeat: 25 * time.Second}
}

// Publish appends an event of type kind with payload (marshaled to JSON) and
// wakes every subscriber. It returns the event's cursor.
func (b *Bus) Publish(ctx context.Context, kind string, payload any) (int64, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	res, err := b.db.ExecContext(ctx, "INSERT INTO events(kind, payload, created_at) VALUES (?, ?, ?)", kind, string(data), time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	cur, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	b.mu.Lock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	b.mu.Unlock()
	return cur, nil
}

// Since returns up to limit events after cursor, oldest first, and the cursor
// to pass next time (unchanged when there is nothing new).
func (b *Bus) Since(ctx context.Context, cursor int64, limit int) ([]Event, int64, error) {
	rows, err := b.db.QueryContext(ctx, "SELECT cursor, kind, payload FROM events WHERE cursor > ? ORDER BY cursor LIMIT ?", cursor, limit)
	if err != nil {
		return nil, cursor, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	last := cursor
	for rows.Next() {
		var e Event
		var p string
		if err := rows.Scan(&e.Cursor, &e.Type, &p); err != nil {
			return nil, cursor, err
		}
		e.Data = json.RawMessage(p)
		out = append(out, e)
		last = e.Cursor
	}
	return out, last, rows.Err()
}

// Head is the newest cursor (0 when the log is empty).
func (b *Bus) Head(ctx context.Context) (int64, error) {
	var c int64
	err := b.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(cursor), 0) FROM events").Scan(&c)
	return c, err
}

// Trim drops events older than age, and also trims progress_log rows for
// sessions that have not been seen since before the same cutoff.
func (b *Bus) Trim(ctx context.Context, age time.Duration) error {
	cutoff := time.Now().Add(-age).UnixMilli()
	if _, err := b.db.ExecContext(ctx, "DELETE FROM events WHERE created_at < ?", cutoff); err != nil {
		return err
	}
	// Remove orphaned progress_log rows for sessions that have been absent
	// longer than the trim window (sessions are never deleted, but their log
	// rows can accumulate if a session died without settling its turn).
	_, err := b.db.ExecContext(ctx,
		"DELETE FROM progress_log WHERE session_id IN (SELECT id FROM sessions WHERE last_seen < ?)", cutoff)
	return err
}

// Subscribe returns a channel that receives a (coalesced) signal after each
// Publish, and a cancel func.
func (b *Bus) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

func cursorParam(r *http.Request) int64 {
	s := r.Header.Get("Last-Event-ID")
	if s == "" {
		s = r.URL.Query().Get("since")
	}
	c, _ := strconv.ParseInt(s, 10, 64)
	return c
}

// ServePoll answers GET ?since=<cursor> with {cursor, events}.
func (b *Bus) ServePoll(w http.ResponseWriter, r *http.Request) {
	evs, cur, err := b.Since(r.Context(), cursorParam(r), 500)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if evs == nil {
		evs = []Event{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"cursor": cur, "events": evs}); err != nil {
		fmt.Fprintf(os.Stderr, "casebook bus: ServePoll encode: %v\n", err)
	}
}

// ServeSSE streams events after Last-Event-ID (or ?since) until the client
// goes away. Each event's id is its cursor and its data is {type, data}.
func (b *Bus) ServeSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	sig, cancel := b.Subscribe()
	defer cancel()
	cur := cursorParam(r)
	tick := time.NewTicker(b.Heartbeat)
	defer tick.Stop()
	for {
		evs, next, err := b.Since(r.Context(), cur, 500)
		if err != nil {
			return
		}
		for _, e := range evs {
			line, _ := json.Marshal(e)
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Cursor, line); err != nil {
				fmt.Fprintf(os.Stderr, "casebook bus: ServeSSE write: %v\n", err)
				return
			}
		}
		if len(evs) > 0 {
			fl.Flush()
			cur = next
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-sig:
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				fmt.Fprintf(os.Stderr, "casebook bus: ServeSSE keepalive: %v\n", err)
				return
			}
			fl.Flush()
		}
	}
}
