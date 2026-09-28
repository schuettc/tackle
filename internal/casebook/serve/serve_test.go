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
	var w waited
	r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w)
	r.do(t, "POST", "/api/agent/progress", map[string]any{"session": "s1", "text": "checking CI on #671", "n": 2, "total": 4}, nil)
	var out map[string]any
	// Pass the delivery id in shown so Settled ends it (updated from old unconditional call).
	if c := r.do(t, "POST", "/api/agent/settled", map[string]any{"session": "s1", "shown": []int64{w.Delivery.ID}}, &out); c != 200 || out["delivery"] == nil {
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

func TestLateReplyHTTPShape(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	m1 := r.send(t, th, "one", false)
	m2 := r.send(t, th, "two", false)
	var w waited
	r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w) // deliver both
	// End the turn: both messages become unanswered. Pass shown id (updated from old unconditional call).
	if c := r.do(t, "POST", "/api/agent/settled", map[string]any{"session": "s1", "shown": []int64{w.Delivery.ID}}, nil); c != 200 {
		t.Fatalf("settled %d", c)
	}

	// Late reply with final state to m1: settled=[m1.ID], skipped=[].
	var out1 struct {
		Settled []int64 `json:"settled"`
		Skipped []struct {
			ID     int64  `json:"id"`
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if c := r.do(t, "POST", "/api/agent/reply", map[string]any{"session": "s1", "ids": []int64{m1.ID}, "state": "answered", "text": "late reply"}, &out1); c != 200 {
		t.Fatalf("late answered reply %d", c)
	}
	if len(out1.Settled) != 1 || out1.Settled[0] != m1.ID {
		t.Fatalf("settled %v, want [%d]", out1.Settled, m1.ID)
	}
	if len(out1.Skipped) != 0 {
		t.Fatalf("skipped %v, want []", out1.Skipped)
	}

	// Non-final state on unanswered m2: settled=[], skipped=[m2].
	var out2 struct {
		Settled []int64 `json:"settled"`
		Skipped []struct {
			ID     int64  `json:"id"`
			State  string `json:"state"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if c := r.do(t, "POST", "/api/agent/reply", map[string]any{"session": "s1", "ids": []int64{m2.ID}, "state": "working"}, &out2); c != 200 {
		t.Fatalf("working on unanswered reply %d", c)
	}
	if len(out2.Settled) != 0 {
		t.Fatalf("settled %v, want []", out2.Settled)
	}
	if len(out2.Skipped) != 1 || out2.Skipped[0].ID != m2.ID || out2.Skipped[0].State != "unanswered" {
		t.Fatalf("skipped %+v", out2.Skipped)
	}
}

// TestSettledPiPattern tests the pi harness pattern end to end:
// a delivery goes out, settled without it in shown 	→ still in flight;
// settled with it 	→ ended, next queued message goes out.
func TestSettledPiPattern(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	m1 := r.send(t, th, "first", false)
	m2 := r.send(t, th, "second", false)

	// Deliver m1 (and m2 is queued behind it).
	var w1 waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w1); c != 200 || len(w1.Delivery.Messages) != 2 {
		t.Fatalf("first wait %d %+v", c, w1)
	}
	if w1.Delivery.Messages[0].ID != m1.ID || w1.Delivery.Messages[1].ID != m2.ID {
		t.Fatalf("first delivery messages %+v", w1.Delivery.Messages)
	}

	// Now queue a third message AFTER the delivery is in flight (it must wait).
	m3 := r.send(t, th, "third", false)

	// Settle without showing the delivery 	→ delivery stays in flight.
	var out1 map[string]any
	if c := r.do(t, "POST", "/api/agent/settled", map[string]any{"session": "s1"}, &out1); c != 200 {
		t.Fatalf("settled (no shown) %d", c)
	}
	if out1["delivery"] != nil {
		t.Fatalf("expected no delivery ended, got %v", out1["delivery"])
	}
	// m3 must still be queued (inflight delivery blocks it).
	if c := r.do(t, "GET", "/api/agent/wait?session=s1&timeout=1", nil, nil); c != http.StatusNoContent {
		t.Fatalf("mid-turn wait after unshown settle: %d, want 204", c)
	}

	// Settle with the delivery id in shown 	→ delivery ends.
	var out2 map[string]any
	if c := r.do(t, "POST", "/api/agent/settled", map[string]any{"session": "s1", "shown": []int64{w1.Delivery.ID}}, &out2); c != 200 {
		t.Fatalf("settled (shown) %d", c)
	}
	if out2["delivery"] == nil {
		t.Fatalf("expected delivery ended, got nil")
	}
	// m3 must now be delivered.
	var w3 waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w3); c != 200 || len(w3.Delivery.Messages) != 1 || w3.Delivery.Messages[0].ID != m3.ID {
		t.Fatalf("third wait %d %+v", c, w3)
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

// TestUnknownSessionErrorHasCode verifies that the serve session() guard
// returns a 404 whose JSON body includes "code":"unknown_session", so the
// channel can distinguish it from other 404s (e.g. message-not-found).
// Fail-before evidence: before adding errCode to httpError and the matching
// JSON output in reply(), the response body had no "code" field.
func TestUnknownSessionErrorHasCode(t *testing.T) {
	r := newRig(t)
	// Request a session-bound endpoint with an unregistered session.
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	var rd bytes.Buffer
	json.NewEncoder(&rd).Encode(map[string]any{"session": "ghost", "shown": []int64{}})
	req, _ := http.NewRequest("POST", r.url+"/api/agent/settled", &rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "unknown_session" {
		t.Fatalf("code field: want \"unknown_session\", got %q (body: %+v)", body.Code, body)
	}
	if !strings.Contains(body.Error, "ghost") {
		t.Fatalf("error message must mention session id: %q", body.Error)
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

func TestAcceptManyDecidesOncePerRequest(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")

	// Propose two keys with different dispositions from session s1.
	var res1 struct {
		Proposals []struct{ ID int64 } `json:"proposals"`
	}
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{
		"session": "s1", "keys": []string{"pr:schuettc/hail#3"}, "disposition": "close",
	}, &res1); c != 200 || len(res1.Proposals) != 1 {
		t.Fatalf("propose1 %d %+v", c, res1)
	}
	var res2 struct {
		Proposals []struct{ ID int64 } `json:"proposals"`
	}
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{
		"session": "s1", "keys": []string{"issue:schuettc/hail#4"}, "disposition": "keep",
	}, &res2); c != 200 || len(res2.Proposals) != 1 {
		t.Fatalf("propose2 %d %+v", c, res2)
	}
	p1, p2 := res1.Proposals[0].ID, res2.Proposals[0].ID

	// Snapshot the bus cursor before the accept request.
	_, before, _ := r.s.Bus.Since(ctx, 0, 10000)

	// Accept both proposals in a single request.
	var acc map[string]any
	if c := r.do(t, "POST", "/api/proposals/accept", map[string]any{"ids": []int64{p1, p2}}, &acc); c != 200 {
		t.Fatalf("accept %d %v", c, acc)
	}
	if acc["accepted"].(float64) != 2 {
		t.Fatalf("want accepted=2, got %v", acc)
	}

	// Both decision files carry their own disposition and ProposedBy.
	d1, _ := r.App.Repo.ReadDecision(item.PRKey("schuettc/hail", 3))
	if d1 == nil || d1.Disposition != "close" || string(d1.ProposedBy) != "pi:s1" {
		t.Fatalf("decision pr#3 %+v", d1)
	}
	d2, _ := r.App.Repo.ReadDecision(item.IssueKey("schuettc/hail", 4))
	if d2 == nil || d2.Disposition != "keep" || string(d2.ProposedBy) != "pi:s1" {
		t.Fatalf("decision issue#4 %+v", d2)
	}

	// Both proposals are now settled as accepted.
	prop1, _ := r.s.Props.Get(ctx, p1)
	if prop1.State != "accepted" {
		t.Fatalf("proposal %d state %q, want accepted", p1, prop1.State)
	}
	prop2, _ := r.s.Props.Get(ctx, p2)
	if prop2.State != "accepted" {
		t.Fatalf("proposal %d state %q, want accepted", p2, prop2.State)
	}

	// The remote's main has both decision commits.
	log, err := git(t, r.Remote, "log", "-5", "--format=%s", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "pr:schuettc/hail#3") || !strings.Contains(log, "issue:schuettc/hail#4") {
		t.Fatalf("remote log missing commits: %q", log)
	}

	// Exactly one "index" event was published by the request.
	events, _, _ := r.s.Bus.Since(ctx, before, 10000)
	indexCount := 0
	for _, e := range events {
		if e.Type == "index" {
			indexCount++
		}
	}
	if indexCount != 1 {
		t.Fatalf("want exactly 1 index event from the accept request, got %d", indexCount)
	}
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

// TestMoveDeliveryWakesOldSession checks that moving an in-flight delivery
// to another session promptly wakes the old session's waiting long-poll so
// the message queued behind it is delivered without waiting the full long-poll
// timeout (~55 s in production; s.Wait in the test rig).
//
// MoveDelivery moves the delivery's threads to the new session, so the queued
// "second" message must live in a separate thread that stays with s1.
func TestMoveDeliveryWakesOldSession(t *testing.T) {
	r := newRig(t)
	// Register s2 so the move target is valid.
	r.attach(t, "s2")

	// Thread1 for s1: m1 goes in-flight in D1.
	th1 := r.attach(t, "s1")
	r.send(t, th1, "first message", false)
	var w1 waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w1); c != 200 || len(w1.Delivery.Messages) == 0 {
		t.Fatalf("first wait %d %+v", c, w1)
	}

	// Thread2 for s1 (separate from D1's thread): m2 is queued behind D1.
	// MoveDelivery only moves threads that appear in D1, so thread2 stays with s1.
	var th2 struct {
		ID int64 `json:"id"`
	}
	if c := r.do(t, "POST", "/api/threads", map[string]any{"session": "s1", "name": "triage2"}, &th2); c != 200 {
		t.Fatalf("create thread2 %d", c)
	}
	r.send(t, th2.ID, "second message", false)

	// Start a long-poll for s1 – it should block because D1 is in flight.
	done := make(chan waited, 1)
	go func() {
		var w waited
		r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w)
		done <- w
	}()
	time.Sleep(150 * time.Millisecond)

	// Move D1 to s2 and check that s1's long-poll wakes promptly.
	start := time.Now()
	if c := r.do(t, "POST", "/api/deliveries/move", map[string]any{"id": w1.Delivery.ID, "session": "s2"}, nil); c != 200 {
		t.Fatalf("move %d", c)
	}

	select {
	case w := <-done:
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("s1 woke too slowly after move: %v (want < 1s)", elapsed)
		}
		if len(w.Delivery.Messages) != 1 {
			t.Fatalf("expected the queued second message in s1's new delivery, got %+v", w.Delivery.Messages)
		}
	case <-time.After(r.s.Wait + time.Second):
		t.Fatal("s1 did not wake after move; expected prompt wakeup")
	}
}
