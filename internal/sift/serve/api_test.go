package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

type fixture struct {
	t     *testing.T
	st    *store.Store
	srv   *Server
	h     http.Handler
	round int64
	rows  []row.Row
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("SIFT_HOME", t.TempDir())
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	f := &fixture{t: t, st: s}
	f.srv = New(s)
	f.h = f.srv.Handler()
	f.record()
	return f
}

func (f *fixture) record() {
	f.t.Helper()
	f.rows = []row.Row{
		{ID: "r-neg", Check: "stale-status", Summary: "status that may be stale", Source: row.Source{File: "/w/a/CLAUDE.md", Start: 3, End: 3},
			Passage: "- Never push.", Verdict: "rewrite", Text: "- Push to a branch."},
		{ID: "r-size", Check: "size", Summary: "over budget", Source: row.Source{File: "/w/a/CLAUDE.md"}, Verdict: "keep"},
		{ID: "r-dead", Check: "dead-path", Summary: "a path that is gone", Source: row.Source{File: "/w/b/AGENTS.md", Repo: "/w/b", Start: 5, End: 5},
			Passage: "see `gone.md`", Certain: true, Verdict: "delete"},
	}
	id, err := f.st.RecordRound(context.Background(), store.Round{Kind: "backlog", Summary: map[string]int{"size": 1}}, f.rows)
	if err != nil {
		f.t.Fatal(err)
	}
	f.round = id
}

func (f *fixture) do(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	body = f.withPrints(method, path, body)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	return w
}

// withPrints gives a decision request that names no fingerprint the row's
// current one, and an edit to merge:C that names no target fingerprint C's
// current one, as the page sends what it shows. A test about fingerprints
// sets the key itself.
func (f *fixture) withPrints(method, path, body string) string {
	if method == "POST" && path == "/api/send" {
		return f.withShown(body)
	}
	if method == "GET" || path != "/api/decisions" {
		return body
	}
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return body
	}
	add := func(d map[string]any) {
		if _, ok := d["fingerprint"]; !ok {
			id, _ := d["id"].(string)
			d["fingerprint"] = f.currentPrint(id)
		}
		v, _ := d["verdict"].(string)
		if _, ok := d["target_fingerprint"]; !ok && d["action"] == "edit" && row.MergeTarget(v) != "" {
			d["target_fingerprint"] = f.currentPrint(row.MergeTarget(v))
		}
	}
	if ds, ok := m["decisions"].([]any); ok {
		for _, d := range ds {
			if d, ok := d.(map[string]any); ok {
				add(d)
			}
		}
	} else {
		add(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// withShown gives a Send that names no decisions the round's unsent ones,
// as the page sends what it shows. A test about what a Send covers names
// them itself.
func (f *fixture) withShown(body string) string {
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return body
	}
	if _, ok := m["files"]; ok {
		return body
	}
	if _, ok := m["rows"]; ok {
		return body
	}
	ctx := context.Background()
	rows, files := map[string]row.Decision{}, map[string]rec.Decision{}
	if rd, rs, err := f.st.LatestRound(ctx); err == nil {
		for _, r := range rs {
			if r.Decision != nil && !r.Decision.Sent {
				rows[r.ID] = *r.Decision
			}
		}
		items, _ := f.st.Files(ctx, rd.ID)
		for _, it := range items {
			if it.Decision != nil && !it.Decision.Sent {
				files[it.Key] = *it.Decision
			}
		}
	}
	m["rows"], m["files"] = rows, files
	b, _ := json.Marshal(m)
	return string(b)
}

// currentPrint is a row's fingerprint in the latest round ("-" when it has
// no such row).
func (f *fixture) currentPrint(id string) string {
	_, rows, err := f.st.LatestRound(context.Background())
	if err != nil {
		return "-"
	}
	for _, r := range rows {
		if r.ID == id {
			return r.Fingerprint
		}
	}
	return "-"
}

type reviewOut struct {
	Sends  int        `json:"sends"`
	Cursor string     `json:"cursor"`
	Round  *roundJSON `json:"round"`
	Rows   []row.Row  `json:"rows"`
}

func (f *fixture) review() reviewOut {
	f.t.Helper()
	w := f.do("GET", "/api/review", "")
	if w.Code != 200 {
		f.t.Fatalf("review %d %s", w.Code, w.Body)
	}
	var m reviewOut
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fixture) decide(id, action string, extra string) *httptest.ResponseRecorder {
	return f.do("PUT", "/api/decisions", fmt.Sprintf(`{"round":%d,"decisions":[{"id":%q,"action":%q%s}]}`, f.round, id, action, extra))
}

func (f *fixture) events(since string) []wireEvent {
	f.t.Helper()
	var r struct {
		Events []wireEvent `json:"events"`
	}
	_ = json.Unmarshal(f.do("GET", "/api/poll?since="+since, "").Body.Bytes(), &r)
	return r.Events
}

func TestReviewShape(t *testing.T) {
	f := newFixture(t)
	m := f.review()
	if m.Round == nil || m.Round.ID != f.round || m.Round.Kind != "backlog" || m.Round.Summary["size"] != 1 {
		t.Fatalf("round %+v", m.Round)
	}
	if len(m.Rows) != 3 || m.Rows[0].ID != "r-neg" || m.Rows[0].Text != "- Push to a branch." || !m.Rows[2].Certain {
		t.Fatalf("rows %+v", m.Rows)
	}
	if !strings.HasPrefix(m.Cursor, f.srv.events.boot+"-") {
		t.Errorf("cursor %q", m.Cursor)
	}
}

// A backlog round with one item the agent has answered and one it hasn't
// is still being recommended: the page gets the progress, and a decision
// is refused, until the last verdict is in.
func TestABacklogRoundWaitsForItsRecommendations(t *testing.T) {
	f := newFixture(t)
	id, err := f.st.RecordRound(context.Background(), store.Round{Kind: "backlog"}, []row.Row{
		{ID: "b1", Check: "intake", Summary: "s", Verdict: "close:done", Source: row.Source{Entry: "1"}},
		{ID: "b2", Check: "intake", Summary: "s", Source: row.Source{Entry: "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.round = id
	var m struct {
		Progress store.Progress `json:"progress"`
	}
	if err := json.Unmarshal(f.do("GET", "/api/review", "").Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Progress.State != store.Recommending || m.Progress.Files != 2 || m.Progress.Recommended != 1 {
		t.Fatalf("progress %+v", m.Progress)
	}
	if w := f.decide("b1", "accept", ""); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "1 of 2") {
		t.Fatalf("decide while recommending: %d %s", w.Code, w.Body)
	}
	if _, err := f.st.AddRows(context.Background(), id, []row.Row{{ID: "b2", Verdict: "ask", Text: "which repo?"}}); err != nil {
		t.Fatal(err)
	}
	if w := f.decide("b1", "accept", ""); w.Code != http.StatusOK {
		t.Fatalf("decide when ready: %d %s", w.Code, w.Body)
	}
}

func TestReviewWithNoRound(t *testing.T) {
	t.Setenv("SIFT_HOME", t.TempDir())
	s, _ := store.Open(context.Background(), filepath.Join(t.TempDir(), "sift.db"))
	defer func() { _ = s.Close() }()
	w := httptest.NewRecorder()
	New(s).Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/review", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"round":null`) || !strings.Contains(w.Body.String(), `"rows":[]`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestPutStoresAndEmits(t *testing.T) {
	f := newFixture(t)
	cur := f.review().Cursor
	if w := f.decide("r-neg", "edit", `,"text":"- Push to a feature branch.","note":"shorter"`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := f.review().Rows[0].Decision
	if got == nil || got.Action != "edit" || got.Text != "- Push to a feature branch." || got.Note != "shorter" || got.Sent {
		t.Fatalf("decision %+v", got)
	}
	if evs := f.events(cur); len(evs) != 1 || evs[0].Type != "decisions" {
		t.Fatalf("events %+v", evs)
	}
}

func TestPutStaleAndInvalid(t *testing.T) {
	f := newFixture(t)
	old := f.round
	for name, c := range map[string]struct {
		body string
		code int
	}{
		"bad action":   {fmt.Sprintf(`{"round":%d,"decisions":[{"id":"r-neg","action":"approve"}]}`, old), 400},
		"bad verdict":  {fmt.Sprintf(`{"round":%d,"decisions":[{"id":"r-neg","action":"edit","verdict":"cut"}]}`, old), 400},
		"empty edit":   {fmt.Sprintf(`{"round":%d,"decisions":[{"id":"r-neg","action":"edit"}]}`, old), 400},
		"unknown row":  {fmt.Sprintf(`{"round":%d,"decisions":[{"id":"nope","action":"accept"}]}`, old), 409},
		"no decisions": {fmt.Sprintf(`{"round":%d,"decisions":[]}`, old), 400},
		"unknown key":  {fmt.Sprintf(`{"round":%d,"decisions":[],"x":1}`, old), 400},
		"no round":     {`{"decisions":[{"id":"r-neg","action":"accept"}]}`, 400},
	} {
		if w := f.do("PUT", "/api/decisions", c.body); w.Code != c.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// A newer round makes the old tab stale.
	f.record()
	if w := f.do("PUT", "/api/decisions", fmt.Sprintf(`{"round":%d,"decisions":[{"id":"r-neg","action":"accept"}]}`, old)); w.Code != 409 {
		t.Fatalf("old round: %d", w.Code)
	}
}

func TestSend(t *testing.T) {
	f := newFixture(t)
	f.decide("r-neg", "accept", "")
	w := f.do("POST", "/api/send", fmt.Sprintf(`{"round":%d}`, f.round))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sent":1`) {
		t.Fatalf("send %d %s", w.Code, w.Body)
	}
	if rv := f.review(); !rv.Rows[0].Decision.Sent || rv.Sends != 1 {
		t.Errorf("after send: sent %v, sends %d", rv.Rows[0].Decision.Sent, rv.Sends)
	}
	w = f.do("POST", "/api/send", fmt.Sprintf(`{"round":%d}`, f.round))
	if !strings.Contains(w.Body.String(), `"sent":0`) {
		t.Fatalf("second send %s", w.Body)
	}
}

// A Send names the decisions the page shows, and sends each only while it
// is still in force: one changed by another page since is left unsent, and
// the response names what it sent. A Send that names nothing is refused.
func TestSendSendsWhatThePageShowed(t *testing.T) {
	f := newFixture(t)
	f.decide("r-neg", "accept", "")
	shown := fmt.Sprintf(`{"round":%d,"files":{},"rows":{"r-neg":{"action":"accept"}}}`, f.round)
	// Another page rejects it before the Send is stored.
	f.decide("r-neg", "reject", "")
	w := f.do("POST", "/api/send", shown)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sent":0`) || !strings.Contains(w.Body.String(), `"rows":[]`) {
		t.Fatalf("send %d %s", w.Code, w.Body)
	}
	if rv := f.review(); rv.Rows[0].Decision.Sent || rv.Sends != 0 {
		t.Fatalf("the reject was sent: %+v, sends %d", rv.Rows[0].Decision, rv.Sends)
	}
	w = f.do("POST", "/api/send", fmt.Sprintf(`{"round":%d,"files":{},"rows":{"r-neg":{"action":"reject"}}}`, f.round))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sent":1`) || !strings.Contains(w.Body.String(), `"rows":["r-neg"]`) || !strings.Contains(w.Body.String(), `"files":[]`) {
		t.Fatalf("send %d %s", w.Code, w.Body)
	}
	if rv := f.review(); !rv.Rows[0].Decision.Sent {
		t.Fatal("not sent")
	}
	req := httptest.NewRequest("POST", "/api/send", strings.NewReader(fmt.Sprintf(`{"round":%d}`, f.round)))
	out := httptest.NewRecorder()
	f.h.ServeHTTP(out, req)
	if out.Code != 400 {
		t.Fatalf("a Send naming nothing: %d %s", out.Code, out.Body)
	}
}

func TestFileReadsTheAuditedVersion(t *testing.T) {
	st.Env(t)
	f := newFixture(t)
	repo := st.Repo(t, t.TempDir(), map[string]string{"CLAUDE.md": "# at the base\n- Never push.\n"})
	st.Write(t, repo, "CLAUDE.md", "# edited since\n")
	rows := []row.Row{{ID: "x", Check: "stale-status", Source: row.Source{File: filepath.Join(repo, "CLAUDE.md"), Repo: repo, Ref: "HEAD", Path: "CLAUDE.md", Start: 2, End: 2}}}
	id, _ := f.st.RecordRound(context.Background(), store.Round{Kind: "on-demand"}, rows)
	w := f.do("GET", fmt.Sprintf("/api/file?round=%d&id=x", id), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "# at the base") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := f.do("GET", fmt.Sprintf("/api/file?round=%d&id=other", id), ""); w.Code != 404 {
		t.Fatalf("a file no row names: %d", w.Code)
	}
}

func TestWatchEmitsOnRevision(t *testing.T) {
	f := newFixture(t)
	cur := f.review().Cursor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.srv.Watch(ctx, 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	if evs := f.events(cur); len(evs) != 0 {
		t.Fatalf("events while nothing changed: %+v", evs)
	}
	if _, err := f.st.AddRows(context.Background(), f.round, []row.Row{{ID: "r-size", Verdict: "keep"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if evs := f.events(cur); len(evs) == 1 && evs[0].Type == "round" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no round event after the agent's proposals")
}

// An edit can clear the title or text: "cleared" names the fields, which
// an empty value can't (empty means "keep the proposal's").
func TestPutClearsAField(t *testing.T) {
	f := newFixture(t)
	if w := f.decide("r-neg", "edit", `,"verdict":"delete","cleared":["text"]`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	d := f.review().Rows[0].Decision
	if d == nil || len(d.Cleared) != 1 || d.Cleared[0] != "text" {
		t.Fatalf("decision %+v", d)
	}
	if c, ok := f.review().Rows[0].Effective(); !ok || c.Text != "" || c.Verdict != "delete" {
		t.Fatalf("effective %+v %v", c, ok)
	}
	if w := f.decide("r-neg", "edit", `,"cleared":["verdict"]`); w.Code != 400 {
		t.Fatalf("clearing the verdict: %d", w.Code)
	}
}
