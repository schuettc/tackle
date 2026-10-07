package bus

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

func newBus(t *testing.T) *Bus {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return New(d)
}

var ctx = context.Background()

func TestPublishSinceHead(t *testing.T) {
	b := newBus(t)
	c1, _ := b.Publish(ctx, "message", map[string]int{"id": 1})
	c2, _ := b.Publish(ctx, "progress", map[string]string{"text": "x"})
	evs, cur, err := b.Since(ctx, c1, 10)
	if err != nil || len(evs) != 1 || evs[0].Type != "progress" || cur != c2 {
		t.Fatalf("since %+v %d %v", evs, cur, err)
	}
	h, _ := b.Head(ctx)
	if h != c2 {
		t.Fatalf("head %d", h)
	}
	if evs, cur, _ := b.Since(ctx, c2, 10); len(evs) != 0 || cur != c2 {
		t.Fatalf("empty since %+v %d", evs, cur)
	}
}

func TestPollContract(t *testing.T) {
	b := newBus(t)
	_, _ = b.Publish(ctx, "index", map[string]int{"new": 3})
	rec := httptest.NewRecorder()
	b.ServePoll(rec, httptest.NewRequest("GET", "/api/state?since=0", nil))
	var got struct {
		Cursor int64 `json:"cursor"`
		Events []struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Cursor != 1 || len(got.Events) != 1 || got.Events[0].Type != "index" || string(got.Events[0].Data) != `{"new":3}` {
		t.Fatalf("poll %s %v", rec.Body.String(), err)
	}
	rec = httptest.NewRecorder()
	b.ServePoll(rec, httptest.NewRequest("GET", "/api/state?since=1", nil))
	if !strings.Contains(rec.Body.String(), `"events":[]`) {
		t.Fatalf("empty poll %s", rec.Body.String())
	}
}

func TestSSEBacklogThenLive(t *testing.T) {
	b := newBus(t)
	_, _ = b.Publish(ctx, "a", 1)
	srv := httptest.NewServer(http.HandlerFunc(b.ServeSSE))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Last-Event-ID", "0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	rd := bufio.NewReader(resp.Body)
	readEvent := func() (string, string) {
		var id, data string
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "id: "):
				id = line[4:]
			case strings.HasPrefix(line, "data: "):
				data = line[6:]
			case line == "" && id != "":
				return id, data
			}
		}
	}
	if id, data := readEvent(); id != "1" || data != `{"type":"a","data":1}` {
		t.Fatalf("backlog %s %s", id, data)
	}
	go func() { time.Sleep(50 * time.Millisecond); _, _ = b.Publish(ctx, "b", 2) }()
	if id, data := readEvent(); id != "2" || data != `{"type":"b","data":2}` {
		t.Fatalf("live %s %s", id, data)
	}
}

func TestTrim(t *testing.T) {
	b := newBus(t)
	_, _ = b.Publish(ctx, "old", 1)
	_, _ = b.db.Exec("UPDATE events SET created_at = 0")
	_, _ = b.Publish(ctx, "new", 2)
	_ = b.Trim(ctx, 1, time.Hour)
	evs, _, _ := b.Since(ctx, 0, 10)
	if len(evs) != 1 || evs[0].Type != "new" {
		t.Fatalf("after trim %+v", evs)
	}
}

// cursors lists the kept events' cursors, oldest first.
func cursors(t *testing.T, b *Bus) []int64 {
	t.Helper()
	evs, _, err := b.Since(ctx, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, e := range evs {
		out = append(out, e.Cursor)
	}
	return out
}

// TestPruneKeepsNewestOrRecent: an event goes only when it is neither among
// the newest keep nor younger than age (whichever keeps more).
func TestPruneKeepsNewestOrRecent(t *testing.T) {
	b := newBus(t)
	for i := 0; i < 20; i++ {
		_, _ = b.Publish(ctx, "old", i)
	}
	_, _ = b.db.Exec("UPDATE events SET created_at = 0")
	for i := 0; i < 15; i++ {
		_, _ = b.Publish(ctx, "recent", i)
	}
	// keep 25: the newest 25 (cursors 11..35), though 10 of them are old.
	if n, err := b.Prune(ctx, 25, time.Hour); err != nil || n != 10 {
		t.Fatalf("prune 25: %d %v", n, err)
	}
	if c := cursors(t, b); len(c) != 25 || c[0] != 11 || c[24] != 35 {
		t.Fatalf("after prune 25: %v", c)
	}
	// keep 10: the last hour's 15 are more, so all of them stay (21..35).
	if n, err := b.Prune(ctx, 10, time.Hour); err != nil || n != 10 {
		t.Fatalf("prune 10: %d %v", n, err)
	}
	if c := cursors(t, b); len(c) != 15 || c[0] != 21 {
		t.Fatalf("after prune 10: %v", c)
	}
	// Nothing more to prune.
	if n, _ := b.Prune(ctx, 10, time.Hour); n != 0 {
		t.Fatalf("prune again: %d", n)
	}
}

// pollTypes asks ServePoll for since and returns the event types and cursor.
func pollTypes(t *testing.T, b *Bus, query string) ([]string, int64) {
	t.Helper()
	rec := httptest.NewRecorder()
	b.ServePoll(rec, httptest.NewRequest("GET", "/api/state"+query, nil))
	var got struct {
		Cursor int64 `json:"cursor"`
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("poll %s: %v", rec.Body.String(), err)
	}
	var types []string
	for _, e := range got.Events {
		types = append(types, e.Type)
	}
	return types, got.Cursor
}

// TestPruneSignalsGap: a client whose cursor is older than the oldest kept
// event hears "gap" first (so it reloads its views), then the kept events;
// one that missed nothing hears no gap.
func TestPruneSignalsGap(t *testing.T) {
	b := newBus(t)
	for i := 0; i < 10; i++ {
		_, _ = b.Publish(ctx, "e", i)
	}
	_, _ = b.db.Exec("UPDATE events SET created_at = 0")
	if _, err := b.Prune(ctx, 4, time.Hour); err != nil { // keeps 7..10
		t.Fatal(err)
	}
	types, cur := pollTypes(t, b, "?since=3")
	if strings.Join(types, " ") != "gap e e e e" || cur != 10 {
		t.Fatalf("since=3 (events 4..6 pruned): %v cursor %d, want gap then 4 events", types, cur)
	}
	if types, _ := pollTypes(t, b, "?since=0"); len(types) == 0 || types[0] != "gap" {
		t.Fatalf("since=0 after a prune: %v, want a gap first", types)
	}
	if types, _ := pollTypes(t, b, "?since=6"); strings.Join(types, " ") != "e e e e" {
		t.Fatalf("since=6 (nothing missed): %v, want no gap", types)
	}
	if types, _ := pollTypes(t, b, "?since=8"); strings.Join(types, " ") != "e e" {
		t.Fatalf("since=8: %v", types)
	}

	// The stream: the gap's id is the cursor just before the oldest kept
	// event, so a reconnect after it asks from there.
	srv := httptest.NewServer(http.HandlerFunc(b.ServeSSE))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Last-Event-ID", "2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	rd := bufio.NewReader(resp.Body)
	var ids, datas []string
	for len(datas) < 2 {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimRight(line, "\n")
		if strings.HasPrefix(line, "id: ") {
			ids = append(ids, line[4:])
		} else if strings.HasPrefix(line, "data: ") {
			datas = append(datas, line[6:])
		}
	}
	if ids[0] != "6" || !strings.HasPrefix(datas[0], `{"type":"gap"`) || ids[1] != "7" {
		t.Fatalf("stream from 2: ids %v data %v, want the gap (id 6) then event 7", ids, datas)
	}
}
