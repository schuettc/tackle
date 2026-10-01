package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/store"
)

type fixture struct {
	t    *testing.T
	st   *store.Store
	srv  *Server
	h    http.Handler
	proj store.Project
	run  int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "cull.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{t: t, st: st}
	f.srv = New(st)
	f.h = f.srv.Handler()
	f.proj, err = st.Project(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	f.run = f.record()
	return f
}

func jev(p float64, key string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"verdict":{"probabilities":{%q:%v}}}`, key, p))
}

func (f *fixture) record() int64 {
	f.t.Helper()
	items := []store.Item{
		{ID: "g1", Kind: "group", Hash: "gh1", Verdict: "keep_separate", Rule: "review_band", Jev: jev(0.4, "consolidate"), Members: []string{"a", "b"}, Rows: [][]string{{"x", "y"}}},
		{ID: "t1", Kind: "test", Hash: "h1", File: "a_test.go", Name: "A", Verdict: "keep", Rule: "review_band", Jev: jev(0.3, "cut"), State: json.RawMessage(`{"s":1}`), Model: "m"},
		{ID: "t2", Kind: "test", Hash: "h2", File: "b_test.go", Name: "B", Verdict: "keep", Rule: "truncated", Jev: jev(0.6, "cut")},
		{ID: "t0", Kind: "test", Hash: "h0", File: "c_test.go", Name: "C", Verdict: "keep", Rule: "review_band"},
	}
	id, err := f.st.RecordRun(context.Background(), store.Run{ProjectID: f.proj.ID, Mode: "all", Total: 9, Summary: map[string]int{"cut": 2}, QuestionsHash: "qh"}, items)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *fixture) do(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	return w
}

func (f *fixture) review() map[string]any {
	f.t.Helper()
	w := f.do("GET", fmt.Sprintf("/api/review?project=%d", f.proj.ID), "")
	if w.Code != 200 {
		f.t.Fatalf("review %d %s", w.Code, w.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) put(run int64, hash string) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"project":%d,"run":%d,"answers":[{"id":"t1","hash":%q,"kind":"test","value":"cut","note":"n","via":"item","blind":true}]}`, f.proj.ID, run, hash)
	return f.do("PUT", "/api/answers", body)
}

func (f *fixture) events(since string) (string, []wireEvent) {
	f.t.Helper()
	w := f.do("GET", "/api/poll?since="+since, "")
	var r struct {
		Cursor string      `json:"cursor"`
		Events []wireEvent `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		f.t.Fatal(err)
	}
	return r.Cursor, r.Events
}

func TestReviewShapeAndOrder(t *testing.T) {
	f := newFixture(t)
	m := f.review()
	run := m["run"].(map[string]any)
	if run["id"].(float64) != float64(f.run) || run["total"].(float64) != 9 || run["mode"] != "all" {
		t.Fatalf("run %v", run)
	}
	if m["project"].(map[string]any)["root"] != "/repo" || m["blind"] != false {
		t.Fatalf("project %v", m)
	}
	var ids []string
	for _, it := range m["items"].([]any) {
		ids = append(ids, it.(map[string]any)["id"].(string))
	}
	if strings.Join(ids, ",") != "t2,t1,t0,g1" {
		t.Fatalf("order %v", ids)
	}
	g := m["items"].([]any)[3].(map[string]any)
	if len(g["members"].([]any)) != 2 || g["rows"] == nil {
		t.Fatalf("group %v", g)
	}
	t1 := m["items"].([]any)[1].(map[string]any)
	if t1["model"] != "m" || t1["state"].(map[string]any)["s"].(float64) != 1 || t1["answer"] != nil {
		t.Fatalf("t1 %v", t1)
	}
}

func TestReviewUnknownAndEmptyProject(t *testing.T) {
	f := newFixture(t)
	if w := f.do("GET", "/api/review?project=999", ""); w.Code != 404 || !strings.Contains(w.Body.String(), `"no project"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	p, _ := f.st.Project(context.Background(), "/empty")
	w := f.do("GET", fmt.Sprintf("/api/review?project=%d", p.ID), "")
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if w.Code != 200 || m["run"] != nil || len(m["items"].([]any)) != 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestPutStoresAndEmits(t *testing.T) {
	f := newFixture(t)
	if w := f.put(f.run, "h1"); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	it := f.review()["items"].([]any)[1].(map[string]any)
	a := it["answer"].(map[string]any)
	if a["value"] != "cut" || a["note"] != "n" || a["via"] != "item" || a["blind"] != true || a["answered_at"] == nil || a["sent_at"] != nil {
		t.Fatalf("answer %v", a)
	}
	got, _ := f.st.Answers(context.Background(), f.proj.ID)
	if got[store.Key{ItemID: "t1", Hash: "h1"}].Model != "m" || got[store.Key{ItemID: "t1", Hash: "h1"}].QuestionsHash != "qh" {
		t.Fatalf("stored %+v", got)
	}
	_, evs := f.events("")
	if len(evs) != 1 || evs[0].Type != "answers" || !strings.Contains(string(evs[0].Data), fmt.Sprintf(`"project":%d`, f.proj.ID)) {
		t.Fatalf("events %v", evs)
	}
}

func TestPutStaleAndInvalid(t *testing.T) {
	f := newFixture(t)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"hash": f.put(f.run, "nope"),
		"run":  f.put(f.run+7, "h1"),
	} {
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"stale"`) {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// one stale answer sinks the batch
	body := fmt.Sprintf(`{"project":%d,"run":%d,"answers":[{"id":"t1","hash":"h1","kind":"test","value":"cut","via":"item"},{"id":"t2","hash":"bad","kind":"test","value":"keep","via":"item"}]}`, f.proj.ID, f.run)
	if w := f.do("PUT", "/api/answers", body); w.Code != 409 {
		t.Fatalf("%d", w.Code)
	}
	if got, _ := f.st.Answers(context.Background(), f.proj.ID); len(got) != 0 {
		t.Fatalf("stored %v", got)
	}
	if _, evs := f.events(""); len(evs) != 0 {
		t.Fatalf("events %v", evs)
	}
	for _, b := range []string{
		`{"project":1,"run":1,"answers":[{"id":"t1","hash":"h1","kind":"test","value":"merge","via":"item"}]}`,
		`{"project":1,"run":1,"answers":[{"id":"t1","hash":"h1","kind":"test","value":"cut","via":"x"}]}`,
		`{"project":1,"run":1,"extra":1,"answers":[]}`,
		`{"project":0,"run":0,"answers":[]}`,
		`not json`,
	} {
		if w := f.do("PUT", "/api/answers", b); w.Code != 400 {
			t.Fatalf("%s: %d %s", b, w.Code, w.Body)
		}
	}
}

func TestDeleteAndSend(t *testing.T) {
	f := newFixture(t)
	f.put(f.run, "h1")
	w := f.do("POST", "/api/send", fmt.Sprintf(`{"project":%d}`, f.proj.ID))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"sent":1}` {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do("POST", "/api/send", fmt.Sprintf(`{"project":%d}`, f.proj.ID)); strings.TrimSpace(w.Body.String()) != `{"sent":0}` {
		t.Fatalf("%s", w.Body)
	}
	if _, evs := f.events(""); len(evs) != 2 { // put + first send only
		t.Fatalf("events %v", evs)
	}
	a := f.review()["items"].([]any)[1].(map[string]any)["answer"].(map[string]any)
	if a["sent_at"] == nil {
		t.Fatalf("not sent: %v", a)
	}
	if w := f.do("DELETE", fmt.Sprintf("/api/answers?project=%d&id=t1&hash=h1", f.proj.ID), ""); w.Code != 204 {
		t.Fatalf("%d", w.Code)
	}
	if f.review()["items"].([]any)[1].(map[string]any)["answer"] != nil {
		t.Fatal("answer remains")
	}
	if w := f.do("DELETE", fmt.Sprintf("/api/answers?project=%d&id=t1&hash=h1", f.proj.ID), ""); w.Code != 204 {
		t.Fatalf("idempotent %d", w.Code)
	}
}

func TestRunEventWithinThreeSeconds(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.srv.Watch(ctx, 50*time.Millisecond)
	time.Sleep(150 * time.Millisecond) // first read recorded
	cur, evs := f.events("")
	if len(evs) != 0 {
		t.Fatalf("events at start %v", evs)
	}
	id := f.record()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, evs = f.events(cur); len(evs) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	want := fmt.Sprintf(`{"project":%d,"run":%d}`, f.proj.ID, id)
	if len(evs) != 1 || evs[0].Type != "run" || string(evs[0].Data) != want {
		t.Fatalf("events %v want %s", evs, want)
	}
}

func TestPollCursorSemantics(t *testing.T) {
	f := newFixture(t)
	f.put(f.run, "h1")
	f.put(f.run, "h1")
	cur, evs := f.events("")
	if cur != "2" || len(evs) != 2 {
		t.Fatalf("%s %v", cur, evs)
	}
	if _, evs = f.events("1"); len(evs) != 1 {
		t.Fatalf("since 1: %v", evs)
	}
	if _, evs = f.events("2"); len(evs) != 0 {
		t.Fatalf("since 2: %v", evs)
	}
	if _, evs = f.events("99"); len(evs) != 2 {
		t.Fatalf("unknown: %v", evs)
	}
}

func TestSSEFlushesAndDelivers(t *testing.T) {
	f := newFixture(t)
	ts := httptest.NewServer(f.h)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req) // returns only once headers are flushed
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	f.put(f.run, "h1")
	sc := bufio.NewScanner(resp.Body)
	var id, data string
	for sc.Scan() {
		l := sc.Text()
		if v, ok := strings.CutPrefix(l, "id: "); ok {
			id = v
		}
		if v, ok := strings.CutPrefix(l, "data: "); ok {
			data = v
			break
		}
	}
	if id != "1" || !strings.Contains(data, `"type":"answers"`) {
		t.Fatalf("id %q data %q", id, data)
	}
}
