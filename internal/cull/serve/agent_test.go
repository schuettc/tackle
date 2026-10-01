package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/store"
)

// clock is a settable time source for presence.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type agentFx struct {
	*fixture
	clk *clock
}

func newAgentFx(t *testing.T) *agentFx {
	f := newFixture(t)
	c := &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	f.srv.now = c.now
	f.srv.waitUnit = 10 * time.Millisecond // timeout=N is N*10ms
	f.srv.pageURL = "http://127.0.0.1:1/?t=ts-fake-0000"
	return &agentFx{f, c}
}

func (a *agentFx) post(path string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	return a.do("POST", path, string(b))
}

func (a *agentFx) presence(session, label, root string) {
	a.t.Helper()
	w := a.post("/api/agent/presence", map[string]string{"session": session, "harness": "pi", "label": label, "root": root})
	if w.Code != 204 {
		a.t.Fatalf("presence %d %s", w.Code, w.Body)
	}
}

func (a *agentFx) review2(session, root string) map[string]any {
	a.t.Helper()
	w := a.post("/api/agent/review", map[string]string{"session": session, "root": root})
	if w.Code != 200 {
		a.t.Fatalf("review %d %s", w.Code, w.Body)
	}
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

func (a *agentFx) wait(session, root string, secs int) *httptest.ResponseRecorder {
	return a.do("GET", fmt.Sprintf("/api/agent/wait?session=%s&root=%s&timeout=%d", session, url.QueryEscape(root), secs), "")
}

// project makes root's project hold one run and one cut answer.
func (a *agentFx) answered(root string, note string) store.Project {
	a.t.Helper()
	ctx := context.Background()
	p, err := a.st.Project(ctx, root)
	if err != nil {
		a.t.Fatal(err)
	}
	run, err := a.st.RecordRun(ctx, store.Run{ProjectID: p.ID}, []store.Item{{ID: "t1", Kind: "test", Hash: "h1", Name: "TestOne"}})
	if err != nil {
		a.t.Fatal(err)
	}
	if err := a.st.SaveAnswers(ctx, p.ID, run, []store.Answer{{ItemID: "t1", Hash: "h1", Kind: "test", Value: "cut", Note: note, Via: "item"}}); err != nil {
		a.t.Fatal(err)
	}
	return p
}

func (a *agentFx) sendNow(p store.Project) map[string]any {
	a.t.Helper()
	w := a.post("/api/send", map[string]int64{"project": p.ID})
	if w.Code != 200 {
		a.t.Fatalf("send %d %s", w.Code, w.Body)
	}
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

func TestPresenceExpiresAfter90s(t *testing.T) {
	a := newAgentFx(t)
	root := t.TempDir()
	a.presence("s1", "pi: one", root)
	if !a.srv.present("s1") {
		t.Fatal("not present at once")
	}
	a.clk.add(89 * time.Second)
	if !a.srv.present("s1") {
		t.Fatal("gone at 89s")
	}
	a.clk.add(2 * time.Second)
	if a.srv.present("s1") {
		t.Fatal("still present at 91s")
	}
	a.presence("s1", "pi: one", root) // refresh
	if !a.srv.present("s1") {
		t.Fatal("refresh did not restore")
	}
}

func TestPresenceResolvesSubdirectoryToProject(t *testing.T) {
	a := newAgentFx(t)
	root := t.TempDir()
	if err := exec.Command("git", "init", "-q", root).Run(); err != nil {
		t.Skip("git unavailable")
	}
	sub := filepath.Join(root, "pkg")
	_ = exec.Command("mkdir", sub).Run()
	a.presence("s1", "x", sub)
	top, _ := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	p, err := a.st.Project(context.Background(), strings.TrimSpace(string(top)))
	if err != nil {
		t.Fatal(err)
	}
	m := a.review2("s1", sub)
	if m["project"].(float64) != float64(p.ID) {
		t.Fatalf("%v vs project %d", m, p.ID)
	}
}

func TestReviewOwnershipSwitchesToLastCaller(t *testing.T) {
	a := newAgentFx(t)
	root := t.TempDir()
	a.presence("s1", "pi: one", root)
	a.presence("s2", "pi: two", root)
	m := a.review2("s1", root)
	if u, _ := m["url"].(string); !strings.Contains(u, "?t=ts-fake-0000") || !strings.HasSuffix(u, fmt.Sprintf("#/p/%.0f", m["project"])) {
		t.Fatalf("url %q", u)
	}
	p, _ := a.st.Project(context.Background(), root)
	if q, _ := a.st.ProjectByID(context.Background(), p.ID); q.OwnerSession != "s1" || q.OwnerLabel != "pi: one" {
		t.Fatalf("%+v", q)
	}
	a.review2("s2", root)
	if q, _ := a.st.ProjectByID(context.Background(), p.ID); q.OwnerSession != "s2" || q.OwnerLabel != "pi: two" {
		t.Fatalf("%+v", q)
	}
}

func TestSendGoesToThePresentOwner(t *testing.T) {
	a := newAgentFx(t)
	root := t.TempDir()
	a.presence("s1", "pi: one", root)
	a.presence("s2", "pi: two", root)
	a.review2("s1", root)
	p := a.answered(root, "careful")
	r := a.sendNow(p)
	if r["sent"].(float64) != 1 || r["to"] != "pi: one" {
		t.Fatalf("%v", r)
	}
	if w := a.wait("s2", root, 3); w.Code != 204 {
		t.Fatalf("non-owner got %d %s", w.Code, w.Body)
	}
	w := a.wait("s1", root, 3)
	if w.Code != 200 {
		t.Fatalf("owner got %d", w.Code)
	}
	var got struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !strings.Contains(got.Text, "1 cut, 0 keep") || !strings.Contains(got.Text, "- TestOne: careful") {
		t.Fatalf("text %q", got.Text)
	}
	if w := a.wait("s1", root, 3); w.Code != 204 {
		t.Fatalf("delivered twice: %d", w.Code)
	}
}

func TestOwnerGoneNextSessionInProjectClaims(t *testing.T) {
	a := newAgentFx(t)
	root, other := t.TempDir(), t.TempDir()
	a.presence("s1", "pi: one", root)
	a.review2("s1", root)
	p := a.answered(root, "")
	a.presence("s2", "pi: two", root)
	a.presence("s3", "pi: three", other)
	a.clk.add(100 * time.Second) // s1 gone
	a.presence("s2", "pi: two", root)
	a.presence("s3", "pi: three", other)
	r := a.sendNow(p)
	if r["to"] != "" {
		t.Fatalf("to %v with the owner gone", r["to"])
	}
	if w := a.wait("s3", other, 3); w.Code != 204 {
		t.Fatalf("another project's session got %d %s", w.Code, w.Body)
	}
	if w := a.wait("s2", root, 3); w.Code != 200 {
		t.Fatalf("project session got %d", w.Code)
	}
}

func TestTwoWaitersOneSend(t *testing.T) {
	a := newAgentFx(t)
	root := t.TempDir()
	a.presence("s1", "one", root)
	a.presence("s2", "two", root)
	p := a.answered(root, "")
	a.srv.waitUnit = 100 * time.Millisecond
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, s := range []string{"s1", "s2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- a.wait(s, root, 10).Code
		}()
	}
	time.Sleep(100 * time.Millisecond)
	a.sendNow(p)
	wg.Wait()
	close(codes)
	n200 := 0
	for c := range codes {
		if c == http.StatusOK {
			n200++
		}
	}
	if n200 != 1 {
		t.Fatalf("%d waiters got the send", n200)
	}
}

func TestSendsSurviveServeRestart(t *testing.T) {
	a := newAgentFx(t)
	root := t.TempDir()
	a.presence("s1", "one", root)
	a.review2("s1", root)
	p := a.answered(root, "")
	a.sendNow(p)
	// A new Server over the same store: the owner (present again) still gets it.
	s2 := New(a.st)
	s2.waitUnit = 10 * time.Millisecond
	a.srv, a.h = s2, s2.Handler()
	a.presence("s1", "one", root)
	if w := a.wait("s1", root, 3); w.Code != 200 {
		t.Fatalf("after restart %d %s", w.Code, w.Body)
	}
}

func TestStatus(t *testing.T) {
	a := newAgentFx(t)
	root := t.TempDir()
	a.presence("s1", "pi: one", root)
	a.review2("s1", root)
	p := a.answered(root, "")
	w := a.do("GET", "/api/agent/status?root="+url.QueryEscape(root), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"answered":1`) || !strings.Contains(w.Body.String(), `"owner":"pi: one"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	a.sendNow(p)
	w = a.do("GET", "/api/agent/status?root="+url.QueryEscape(root), "")
	if !strings.Contains(w.Body.String(), `"sent":1`) || !strings.Contains(w.Body.String(), `"undelivered":[`) || strings.Contains(w.Body.String(), `"undelivered":[]`) {
		t.Fatalf("%s", w.Body)
	}
}

func TestSendTextFormat(t *testing.T) {
	s := store.Send{
		Counts: store.Counts{Cut: 2, Keep: 1, Merge: 1, Separate: 0},
		Notes:  []store.Note{{Name: "TestA", Note: "keep, it guards X"}, {Name: "table group", Note: "same shape"}},
	}
	want := "Court sent his answers for shop: 2 cut, 1 keep, 1 merge, 0 separate.\n" +
		"Notes:\n- TestA: keep, it guards X\n- table group: same shape\n" +
		"Next: run cull_check, then cull_apply to remove the cuts.\n" +
		"Then rewrite each group he chose to merge as one table test and run cull_check_group on it."
	if got := SendText(s, "/home/dev/shop"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	s.Counts.Merge, s.Notes = 0, nil
	want = "Court sent his answers for shop: 2 cut, 1 keep, 0 merge, 0 separate.\n" +
		"Next: run cull_check, then cull_apply to remove the cuts."
	if got := SendText(s, "/home/dev/shop"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
