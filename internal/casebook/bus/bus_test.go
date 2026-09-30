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
	_ = b.Trim(ctx, time.Hour)
	evs, _, _ := b.Since(ctx, 0, 10)
	if len(evs) != 1 || evs[0].Type != "new" {
		t.Fatalf("after trim %+v", evs)
	}
}
