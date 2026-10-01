package serve

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	boot   string
	mu     sync.Mutex
	evs    []event
	next   int64
	notify chan struct{} // closed and replaced on every emit
}

func (l *eventLog) init() {
	l.notify = make(chan struct{})
	var b [4]byte
	_, _ = rand.Read(b[:])
	l.boot = hex.EncodeToString(b[:])
}

// format renders the wire cursor for event n: "<boot>-<n>".
func (l *eventLog) format(n int64) string { return l.boot + "-" + strconv.FormatInt(n, 10) }

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

// resume parses a client cursor. ok is false (the client must reload) when
// it is malformed, from another boot, ahead of the log, or older than the
// oldest retained event.
func (l *eventLog) resume(v string) (int64, bool) {
	boot, num, found := strings.Cut(v, "-")
	if !found || boot != l.boot {
		return 0, false
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	oldest := l.next + 1 // first event the client would need next
	if len(l.evs) > 0 {
		oldest = l.evs[0].Cursor
	}
	if n > l.next || n < oldest-1 {
		return 0, false
	}
	return n, true
}

type wireEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query().Get("since")
	since, ok := int64(0), true // no cursor: everything retained
	if v != "" {
		since, ok = s.events.resume(v)
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"cursor": s.events.format(s.events.latest()), "reset": true, "events": []wireEvent{}})
		return
	}
	evs, cur, _ := s.events.after(since)
	out := make([]wireEvent, 0, len(evs))
	for _, e := range evs {
		out = append(out, wireEvent{e.Type, e.Data})
	}
	writeJSON(w, http.StatusOK, map[string]any{"cursor": s.events.format(cur), "events": out})
}

const defaultWriteTimeout = 10 * time.Second

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	s.streamWG.Add(1)
	defer s.streamWG.Done()
	s.streams.Add(1)
	defer s.streams.Add(-1)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.mu.Lock()
	life := s.life
	s.mu.Unlock()
	if life != nil {
		defer context.AfterFunc(life, cancel)()
	}
	rc := http.NewResponseController(w)
	timeout := s.writeTimeout
	if timeout <= 0 {
		timeout = defaultWriteTimeout
	}
	// write sends one frame under a fresh write deadline, so a client that
	// stopped reading ends its stream instead of pinning it.
	write := func(format string, args ...any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(timeout))
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	var cursor int64
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		cursor = s.events.latest()
	} else {
		cursor, ok = s.events.resume(lastID)
		if !ok {
			cursor = s.events.latest()
			if !write(": ok\n\nid: %s\ndata: {\"type\":\"reset\",\"data\":{}}\n\n", s.events.format(cursor)) {
				return
			}
			lastID = "reset"
		}
	}
	if lastID != "reset" && !write(": ok\n\n") {
		return
	}
	beat := time.NewTicker(15 * time.Second)
	defer beat.Stop()
	for {
		evs, _, wait := s.events.after(cursor)
		for _, e := range evs {
			b, _ := json.Marshal(wireEvent{e.Type, e.Data})
			if !write("id: %s\ndata: %s\n\n", s.events.format(e.Cursor), b) {
				return
			}
			cursor = e.Cursor
		}
		select {
		case <-ctx.Done():
			return
		case <-wait:
		case <-beat.C:
			// The heartbeat is not activity: an open but idle tab must not
			// keep the server alive.
			if !write(": hb\n\n") {
				return
			}
		}
	}
}
