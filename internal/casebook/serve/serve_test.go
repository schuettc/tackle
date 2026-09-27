package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/item"
)

var ctx = context.Background()

type rig struct {
	*apptest.Rig
	s   *Server
	url string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ar := apptest.New(t)
	d, err := db.Open(filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s, err := New(ctx, ar.App, d)
	if err != nil {
		t.Fatal(err)
	}
	s.Wait = 2 * time.Second
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return &rig{Rig: ar, s: s, url: hs.URL}
}

func (r *rig) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s %s: %v\n%s", method, path, err, b)
		}
	}
	return resp.StatusCode
}

type thread struct {
	ID int64 `json:"id"`
}

type message struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

type waited struct {
	Delivery struct {
		ID       int64     `json:"id"`
		Messages []message `json:"messages"`
	} `json:"delivery"`
	Text string `json:"text"`
}

func (r *rig) attach(t *testing.T, session string) int64 {
	t.Helper()
	if c := r.do(t, "POST", "/api/agent/presence", map[string]any{"id": session, "harness": "pi", "label": "pi · tools-workspace", "cwd": "/w", "pid": 1}, nil); c != 200 {
		t.Fatalf("presence %d", c)
	}
	var th thread
	if c := r.do(t, "POST", "/api/threads", map[string]any{"session": session, "name": "triage"}, &th); c != 200 {
		t.Fatalf("thread %d", c)
	}
	return th.ID
}

func (r *rig) send(t *testing.T, th int64, body string, batch bool) message {
	t.Helper()
	var m message
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": body, "batch": batch}, &m); c != 200 {
		t.Fatalf("post %d", c)
	}
	return m
}

func TestTurnAwareDeliveryOverHTTP(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	m1 := r.send(t, th, "propose decisions for the chime PRs", false)
	var w1 waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w1); c != 200 || len(w1.Delivery.Messages) != 1 || w1.Delivery.Messages[0].ID != m1.ID {
		t.Fatalf("first wait %d %+v", c, w1)
	}
	if !strings.Contains(w1.Text, "casebook: 1 message from Court") || !strings.Contains(w1.Text, "propose decisions for the chime PRs") {
		t.Fatalf("text %q", w1.Text)
	}
	m2 := r.send(t, th, "and skip #12", false)
	if c := r.do(t, "GET", "/api/agent/wait?session=s1&timeout=1", nil, nil); c != http.StatusNoContent {
		t.Fatalf("mid-turn wait %d, want 204", c)
	}
	if c := r.do(t, "POST", "/api/agent/reply", map[string]any{"session": "s1", "ids": []int64{m1.ID}, "state": "answered", "text": "done"}, nil); c != 200 {
		t.Fatalf("reply %d", c)
	}
	var w2 waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w2); c != 200 || w2.Delivery.Messages[0].ID != m2.ID {
		t.Fatalf("second wait %d %+v", c, w2)
	}
	if !strings.Contains(w2.Text, "sent while you were working on") {
		t.Fatalf("second text lacks working-on header: %q", w2.Text)
	}
}

func TestWaitWakesOnPost(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	done := make(chan waited, 1)
	go func() {
		var w waited
		r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w)
		done <- w
	}()
	time.Sleep(150 * time.Millisecond)
	start := time.Now()
	r.send(t, th, "hello", false)
	select {
	case w := <-done:
		if len(w.Delivery.Messages) != 1 || time.Since(start) > time.Second {
			t.Fatalf("woke with %+v after %v", w, time.Since(start))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not wake")
	}
}

func TestBatchArrivesTogether(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	r.send(t, th, "first", true)
	r.send(t, th, "second", true)
	var msgs struct {
		Batch  int64     `json:"batch"`
		Drafts []message `json:"drafts"`
	}
	r.do(t, "GET", "/api/messages?thread="+itoa(th), nil, &msgs)
	if msgs.Batch == 0 || len(msgs.Drafts) != 2 {
		t.Fatalf("drafts %+v", msgs)
	}
	if c := r.do(t, "GET", "/api/agent/wait?session=s1&timeout=1", nil, nil); c != http.StatusNoContent {
		t.Fatalf("drafts delivered before send: %d", c)
	}
	r.do(t, "POST", "/api/batches/send", map[string]any{"batch": msgs.Batch}, nil)
	var w waited
	r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w)
	if len(w.Delivery.Messages) != 2 || !strings.Contains(w.Text, "batch b-1 (2 of 2)") {
		t.Fatalf("batch delivery %+v", w)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestSettledEndsTurn(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	m := r.send(t, th, "check CI", false)
	r.do(t, "GET", "/api/agent/wait?session=s1", nil, nil)
	r.do(t, "POST", "/api/agent/progress", map[string]any{"session": "s1", "text": "checking CI on #671", "n": 2, "total": 4}, nil)
	var out map[string]any
	if c := r.do(t, "POST", "/api/agent/settled", map[string]any{"session": "s1"}, &out); c != 200 || out["delivery"] == nil {
		t.Fatalf("settled %d %v", c, out)
	}
	got, _ := r.s.Queue.Message(ctx, m.ID)
	if got.State != "unanswered" {
		t.Fatalf("state %s", got.State)
	}
	if _, ok, _ := r.s.Props.Progress(ctx, "s1"); ok {
		t.Fatal("progress not cleared")
	}
}

func TestProposeAcceptWritesDecision(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	var res struct {
		Proposed  int      `json:"proposed"`
		Errors    []string `json:"errors"`
		Proposals []struct {
			ID int64 `json:"id"`
		} `json:"proposals"`
	}
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{"issue:schuettc/hail#4", "repo:schuettc/hail"},
		"disposition": "close", "note": "fixed upstream"}, &res)
	if res.Proposed != 1 || len(res.Errors) != 1 {
		t.Fatalf("propose %+v (a repo can't be closed)", res)
	}
	var list struct {
		Total int `json:"total"`
		Items []struct {
			Key      string `json:"key"`
			Proposal *struct {
				Source string `json:"source"`
			} `json:"proposal"`
		} `json:"items"`
	}
	r.do(t, "GET", "/api/items?view=proposed", nil, &list)
	if list.Total != 1 || list.Items[0].Key != "issue:schuettc/hail#4" || list.Items[0].Proposal.Source != "pi:s1" {
		t.Fatalf("proposed view %+v", list)
	}
	var acc map[string]any
	r.do(t, "POST", "/api/proposals/accept", map[string]any{"ids": []int64{res.Proposals[0].ID}}, &acc)
	if acc["accepted"].(float64) != 1 {
		t.Fatalf("accept %v", acc)
	}
	d, _ := r.App.Repo.ReadDecision(item.IssueKey("schuettc/hail", 4))
	if d == nil || d.Disposition != "close" || d.ProposedBy != "pi:s1" || d.DecidedBy != "schuettc" || d.Note != "fixed upstream" {
		t.Fatalf("decision %+v", d)
	}
	r.do(t, "GET", "/api/items?view=proposed", nil, &list)
	if list.Total != 0 {
		t.Fatalf("still proposed %+v", list)
	}
	var v struct {
		Since string `json:"since"`
	}
	r.do(t, "GET", "/api/agent/status?session=s1", nil, &v)
	if !strings.Contains(v.Since, "Court accepted 1 of your 1 settled proposals") {
		t.Fatalf("since %q", v.Since)
	}
}

func TestDecideDirectSupersedesProposal(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{"pr:schuettc/hail#3"}, "disposition": "close"}, nil)
	var out map[string]any
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"pr:schuettc/hail#3"}, "disposition": "keep", "note": "still useful"}, &out); c != 200 || out["decided"].(float64) != 1 {
		t.Fatalf("decide %d %v", c, out)
	}
	pending, _ := r.s.Props.Pending(ctx)
	if len(pending) != 0 {
		t.Fatalf("pending %+v", pending)
	}
	if got := strings.TrimSpace(r.gitRemoteHead(t)); !strings.HasPrefix(got, "decide pr:schuettc/hail#3 → keep") {
		t.Fatalf("remote head %q", got)
	}
}

func (r *rig) gitRemoteHead(t *testing.T) string {
	out, err := git(t, r.Remote, "log", "-1", "--format=%s", "main")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRejectsUnknownFieldsAndUnknownSessions(t *testing.T) {
	r := newRig(t)
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"repo:a/b"}, "disposition": "keep", "colour": "red"}, nil); c != 400 {
		t.Fatalf("unknown field %d", c)
	}
	if c := r.do(t, "GET", "/api/agent/wait?session=ghost", nil, nil); c != 404 {
		t.Fatalf("unknown session %d", c)
	}
	if c := r.do(t, "POST", "/api/agent/reply", map[string]any{"session": "", "ids": []int64{1}, "state": "answered"}, nil); c != 400 {
		t.Fatalf("empty session %d", c)
	}
}

func TestItemDetail(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.do(t, "POST", "/api/agent/evidence", map[string]any{"session": "s1", "key": "pr:schuettc/hail#3", "text": "CI is green"}, nil)
	var out struct {
		Item struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		} `json:"item"`
		Evidence []struct {
			Text   string `json:"text"`
			Author string `json:"author"`
		} `json:"evidence"`
	}
	if c := r.do(t, "GET", "/api/item?key=pr:schuettc/hail%233", nil, &out); c != 200 {
		t.Fatalf("item %d", c)
	}
	if out.Item.Title != "fix nudge" || len(out.Evidence) != 1 || out.Evidence[0].Author != "pi:s1" {
		t.Fatalf("detail %+v", out)
	}
	if c := r.do(t, "GET", "/api/item?key=repo:nobody/nothing", nil, nil); c != 404 {
		t.Fatalf("missing item %d", c)
	}
}

func TestHeadMoveRebuildsIndex(t *testing.T) {
	r := newRig(t)
	r.s.WatchEvery = 50 * time.Millisecond
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.s.watch(wctx)
	before := r.s.Index.Head()
	if _, _, err := r.App.Decide(ctx, "repo:schuettc/hail", "keep", appOpts()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for r.s.Index.Head() == before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if r.s.Index.Head() == before {
		t.Fatal("index not rebuilt after HEAD moved")
	}
	it, _ := r.s.Index.Item("repo:schuettc/hail")
	if it.Decision == nil || it.Decision.Disposition != "keep" {
		t.Fatalf("item %+v", it)
	}
}

func TestSinceListsOverruledWithReasons(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	var res struct {
		Proposals []struct{ ID int64 } `json:"proposals"`
	}
	keys := []string{"pr:schuettc/hail#3", "issue:schuettc/hail#4"}
	for i := 100; i < 112; i++ {
		keys = append(keys, "pr:schuettc/hail#"+strconv.Itoa(i))
	}
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": keys, "disposition": "close"}, &res)
	if len(res.Proposals) != 14 {
		t.Fatalf("proposed %d", len(res.Proposals))
	}
	r.do(t, "POST", "/api/proposals/change", map[string]any{"id": res.Proposals[0].ID, "disposition": "keep", "note": "active work"}, nil)
	var ids []int64
	for _, p := range res.Proposals[1:] {
		ids = append(ids, p.ID)
	}
	r.do(t, "POST", "/api/proposals/reject", map[string]any{"ids": ids, "reason": "still in use"}, nil)
	var st struct {
		Since    string         `json:"since"`
		Counts   map[string]any `json:"counts"`
		PageOpen bool           `json:"page_open"`
	}
	r.do(t, "GET", "/api/agent/status?session=s1", nil, &st)
	if !strings.HasPrefix(st.Since, "Court accepted 0 of your 14 settled proposals, changed 1, rejected 13.") {
		t.Fatalf("since %q", st.Since)
	}
	if n := strings.Count(st.Since, "\n- "); n != 11 { // 10 listed + "and N more"
		t.Fatalf("%d lines in %q", n, st.Since)
	}
	if !strings.Contains(st.Since, "you proposed close; Court rejected it: still in use") || !strings.Contains(st.Since, "- and 4 more") {
		t.Fatalf("since %q", st.Since)
	}
	if st.Counts == nil || st.PageOpen {
		t.Fatalf("status %+v (page requests alone don't make the page open; a stream does)", st)
	}
	// The change is listed when it's among the newest ten.
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{"pr:schuettc/hail#3"}, "disposition": "close"}, &res)
	r.do(t, "POST", "/api/proposals/change", map[string]any{"id": res.Proposals[0].ID, "disposition": "wait", "until": "date(2026-12-01)", "note": "after the demo"}, nil)
	r.do(t, "GET", "/api/agent/status?session=s1", nil, &st)
	if !strings.Contains(st.Since, "pr:schuettc/hail#3: you proposed close; Court changed it: decided wait until date(2026-12-01): after the demo") {
		t.Fatalf("since %q", st.Since)
	}
}

// stream opens the page's event stream (a connected tab) until the returned
// function closes it.
func stream(t *testing.T, base, token string) func() {
	t.Helper()
	sctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(sctx, "GET", base+"/api/events", nil)
	if token != "" {
		req.Header.Set("X-Local-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: %v %v", resp, err)
	}
	return func() { cancel(); resp.Body.Close() }
}

// eventually polls cond for up to 5 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPageOpenFollowsTheStream(t *testing.T) {
	r := newRig(t)
	r.do(t, "POST", "/api/agent/presence", map[string]any{"id": "s1", "harness": "pi"}, nil)
	status := func() bool {
		var st struct {
			PageOpen bool `json:"page_open"`
		}
		r.do(t, "GET", "/api/agent/status?session=s1", nil, &st)
		return st.PageOpen
	}
	flag := func() string {
		var v string
		r.s.DB.QueryRow("SELECT value FROM meta WHERE key = 'page_open'").Scan(&v)
		return v
	}
	r.do(t, "GET", "/api/summary", nil, nil)
	if status() || flag() == "1" {
		t.Fatal("agent and page requests without a stream count as an open page")
	}
	if c := r.do(t, "POST", "/api/agent/open", map[string]any{}, nil); c != http.StatusConflict {
		t.Fatalf("open without a browser: %d", c)
	}
	closeA := stream(t, r.url, "")
	closeB := stream(t, r.url, "")
	eventually(t, "the page to be open", func() bool { return status() && flag() == "1" })
	closeA()
	time.Sleep(100 * time.Millisecond)
	if !status() || flag() != "1" {
		t.Fatal("closing one of two tabs closed the page")
	}
	closeB()
	eventually(t, "the last tab to close the page", func() bool { return !status() && flag() == "0" })
}
