package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const keepEvents = 1000

type event struct {
	Cursor int64           `json:"-"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
}

// eventLog is an in-memory log with increasing integer cursors.
type eventLog struct {
	mu     sync.Mutex
	evs    []event
	next   int64
	notify chan struct{} // closed and replaced on every emit
}

func (l *eventLog) init() { l.notify = make(chan struct{}) }

func (l *eventLog) emit(typ string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	l.mu.Lock()
	l.next++
	l.evs = append(l.evs, event{Cursor: l.next, Type: typ, Data: b})
	if len(l.evs) > keepEvents {
		l.evs = append([]event(nil), l.evs[len(l.evs)-keepEvents:]...)
	}
	close(l.notify)
	l.notify = make(chan struct{})
	l.mu.Unlock()
}

// after returns the events with cursor > since, the latest cursor, and the
// channel that closes on the next emit.
func (l *eventLog) after(since int64) ([]event, int64, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []event
	for _, e := range l.evs {
		if e.Cursor > since {
			out = append(out, e)
		}
	}
	return out, l.next, l.notify
}

func (l *eventLog) latest() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.next
}

// sinceCursor parses a cursor; ok is false for empty, malformed, or a cursor
// this log never issued.
func (l *eventLog) sinceCursor(v string) (int64, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 || n > l.latest() {
		return 0, false
	}
	return n, true
}

type wireEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	since, ok := s.events.sinceCursor(r.URL.Query().Get("since"))
	if !ok {
		since = 0 // unknown: everything retained; the client reloads
	}
	evs, cur, _ := s.events.after(since)
	out := make([]wireEvent, 0, len(evs))
	for _, e := range evs {
		out = append(out, wireEvent{e.Type, e.Data})
	}
	writeJSON(w, http.StatusOK, map[string]any{"cursor": strconv.FormatInt(cur, 10), "events": out})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	s.streamWG.Add(1)
	defer s.streamWG.Done()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.mu.Lock()
	life := s.life
	s.mu.Unlock()
	if life != nil {
		defer context.AfterFunc(life, cancel)()
	}
	cursor, ok := s.events.sinceCursor(r.Header.Get("Last-Event-ID"))
	if !ok {
		cursor = s.events.latest()
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		evs, _, wait := s.events.after(cursor)
		for _, e := range evs {
			b, _ := json.Marshal(wireEvent{e.Type, e.Data})
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Cursor, b); err != nil {
				return
			}
			cursor = e.Cursor
		}
		fl.Flush()
		select {
		case <-ctx.Done():
			return
		case <-wait:
		case <-beat.C:
			s.activity.Store(time.Now().UnixMilli())
			_, _ = fmt.Fprint(w, ": hb\n\n")
		}
	}
}
