package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

// auditFixture records an audit round of two files in a real repo, linked
// in their recommendations, and returns them.
func auditFixture(t *testing.T) (*fixture, rec.File, rec.File) {
	t.Helper()
	f := newFixture(t)
	st.Env(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\n- Never push.\n", "docs/AGENTS.md": "# Docs\n"})
	commit := st.Git(t, repo, "rev-parse", "HEAD")
	mk := func(rel, body string) rec.File {
		x := rec.NewFile(row.Source{File: filepath.Join(repo, rel), Repo: repo, Ref: "HEAD", Path: rel}, "repo", 6000, body)
		x.Commit = commit
		return x
	}
	a, b := mk("CLAUDE.md", "# App\n\n- Never push.\n"), mk("docs/AGENTS.md", "# Docs\n")
	a.Rows = []string{"n1"}
	id, err := f.st.RecordAudit(context.Background(), store.Round{Kind: "on-demand"},
		[]row.Row{{ID: "n1", Check: "stale-status", Summary: "stale status", Source: row.Source{File: a.Source.File, Start: 3, End: 3}, Passage: "- Never push.", Certain: true}},
		[]rec.File{a, b})
	if err != nil {
		t.Fatal(err)
	}
	f.round = id
	return f, a, b
}

func (f *fixture) propose(a, b rec.File, body string) {
	f.t.Helper()
	if _, err := f.st.Propose(context.Background(), f.round, []rec.Rec{
		{File: a.Key, Base: a.Base, Content: body, Summary: "Moves the rule.", Links: []string{b.Key},
			Findings: []rec.Account{{Row: "n1", Did: "fixed", How: "moved and rewritten"}}},
		{File: b.Key, Base: b.Base, Content: "# Docs\n\n- Push to a branch.\n", Summary: "Takes the rule.", Links: []string{a.Key}},
	}); err != nil {
		f.t.Fatal(err)
	}
}

type fileOut struct {
	Key         string         `json:"key"`
	Path        string         `json:"path"`
	Size        int            `json:"size"`
	After       int            `json:"after"`
	Rows        []string       `json:"rows"`
	Rec         *rec.Rec       `json:"rec"`
	Decision    *rec.Decision  `json:"decision"`
	Fingerprint string         `json:"fingerprint"`
	Group       []string       `json:"group"`
	Source      map[string]any `json:"source"`
}

type filesReview struct {
	Progress store.Progress `json:"progress"`
	Files    []fileOut      `json:"files"`
}

func (f *fixture) files() filesReview {
	f.t.Helper()
	w := f.do("GET", "/api/review", "")
	var out filesReview
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		f.t.Fatalf("%v %s", err, w.Body)
	}
	return out
}

func (f *fixture) prints() string {
	f.t.Helper()
	m := map[string]string{}
	for _, x := range f.files().Files {
		m[x.Key] = x.Fingerprint
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// While the agent recommends, the page shows how far it got and takes no
// decision; once ready, it carries each file's recommendation, and the
// audited content is read on demand.
func TestTheReviewWaitsForEveryRecommendation(t *testing.T) {
	f, a, b := auditFixture(t)
	rv := f.files()
	if rv.Progress.State != store.Recommending || rv.Progress.Files != 1 || len(rv.Files) != 2 || rv.Files[0].Rec != nil {
		t.Fatalf("%+v", rv)
	}
	w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept","prints":{%q:"x"}}`, f.round, a.Key, a.Key))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "recommending") {
		t.Fatalf("a decision while recommending: %d %s", w.Code, w.Body)
	}
	f.propose(a, b, "# App\n")
	rv = f.files()
	if rv.Progress.State != store.Ready || rv.Files[0].Rec == nil || rv.Files[0].Fingerprint == "" || len(rv.Files[0].Group) != 2 ||
		rv.Files[0].Size != len("# App\n\n- Never push.\n") || rv.Files[0].After != len("# App\n") || rv.Files[0].Path != a.Source.File {
		t.Fatalf("%+v", rv.Files[0])
	}
	w = f.do("GET", fmt.Sprintf("/api/base?round=%d&file=%s", f.round, a.Key), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"# App\n\n- Never push.\n"`) {
		t.Fatalf("base: %d %s", w.Code, w.Body)
	}
	if w := f.do("GET", fmt.Sprintf("/api/base?round=%d&file=nope", f.round), ""); w.Code != 404 {
		t.Fatalf("base of no file: %d", w.Code)
	}
}

// A decision on a file decides its linked files with it, against the
// prints the page showed; an old page is refused; an audit round's rows
// are not decided one by one.
func TestFileDecisions(t *testing.T) {
	f, a, b := auditFixture(t)
	f.propose(a, b, "# App\n")
	old := f.prints()
	w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept","note":"yes","prints":%s}`, f.round, a.Key, old))
	if w.Code != 200 {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	for _, x := range f.files().Files {
		if x.Decision == nil || x.Decision.Action != "accept" {
			t.Fatalf("%s: %+v", x.Path, x.Decision)
		}
	}
	if w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"edit","prints":%s}`, f.round, a.Key, old)); w.Code != 400 {
		t.Fatalf("an edit with no content: %d", w.Code)
	}
	if w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept"}`, f.round, a.Key)); w.Code != 400 {
		t.Fatalf("no prints: %d", w.Code)
	}
	f.propose(a, b, "# App, again\n")
	w = f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"reject","prints":%s}`, f.round, a.Key, old))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "changed") {
		t.Fatalf("an old page: %d %s", w.Code, w.Body)
	}
	w = f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"edit","content":"# Docs, mine\n","prints":%s}`, f.round, b.Key, f.prints()))
	if w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	if w := f.do("POST", "/api/files/clear", fmt.Sprintf(`{"round":%d,"file":%q,"prints":%s}`, f.round, a.Key, f.prints())); w.Code != 200 {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	for _, x := range f.files().Files {
		if x.Decision != nil {
			t.Fatalf("%s still decided", x.Path)
		}
	}
	if w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept","prints":%s}`, f.round+1, a.Key, f.prints())); w.Code != 409 {
		t.Fatalf("another round: %d", w.Code)
	}
	w = f.do("PUT", "/api/decisions", fmt.Sprintf(`{"round":%d,"decisions":[{"id":"n1","action":"reject"}]}`, f.round))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "per file") {
		t.Fatalf("a row decision in an audit round: %d %s", w.Code, w.Body)
	}
}

// A decision answers the content the page shows. Its response carries the
// group's files as they now are, each with its print; an old page that still shows an edit
// another client cleared is refused, and after a reload it can accept.
func TestAcceptAnswersTheEditThePageShowed(t *testing.T) {
	f, a, b := auditFixture(t)
	f.propose(a, b, "# App\n")
	w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"edit","content":"# Docs, mine\n","prints":%s}`, f.round, b.Key, f.prints()))
	if w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	shown := f.prints()
	if got := snapPrints(t, w.Body.Bytes()); got != shown {
		t.Fatalf("the response's prints %s are not the page's %s", got, shown)
	}
	if w := f.do("POST", "/api/files/clear", fmt.Sprintf(`{"round":%d,"file":%q,"prints":%s}`, f.round, b.Key, f.prints())); w.Code != 200 {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	w = f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept","prints":%s}`, f.round, b.Key, shown))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "changed") {
		t.Fatalf("an old page's accept: %d %s", w.Code, w.Body)
	}
	for _, x := range f.files().Files {
		if x.Decision != nil {
			t.Fatalf("%s approved: %+v", x.Path, x.Decision)
		}
	}
	w = f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept","prints":%s}`, f.round, b.Key, f.prints()))
	if w.Code != 200 {
		t.Fatalf("accept after a reload: %d %s", w.Code, w.Body)
	}
	for _, x := range f.files().Files {
		if x.Decision == nil || x.Decision.Action != "accept" || x.Decision.Content != "" {
			t.Fatalf("%s: %+v", x.Path, x.Decision)
		}
	}
}

// The agent's review opens the page only once the round is ready.
func TestAgentReviewWaitsForReady(t *testing.T) {
	f, a, b := auditFixture(t)
	f.presence("s1", "pi · app")
	w := f.do("POST", "/api/agent/review", `{"session":"s1"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "0 of 1") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	f.propose(a, b, "# App\n")
	w = f.do("POST", "/api/agent/review", `{"session":"s1"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"open":2`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// snapPrints is the prints of a decision's or clear's snapshot, as JSON.
func snapPrints(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		Files []fileOut `json:"files"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%v %s", err, body)
	}
	m := map[string]string{}
	for _, x := range got.Files {
		m[x.Key] = x.Fingerprint
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// A clear answers the content the page shows, as a decision does: a page
// still showing an edit on a recommendation another client replaced is
// refused with no snapshot; once it shows the new one (the snapshot a
// reload gives), accepting it approves exactly that.
func TestAStaleClearIsRefused(t *testing.T) {
	f, a, b := auditFixture(t)
	f.propose(a, b, "# App\n")
	w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"edit","content":"# Docs, mine\n","prints":%s}`, f.round, b.Key, f.prints()))
	if w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	shown := f.prints()
	f.propose(a, b, "# App, again\n")
	w = f.do("POST", "/api/files/clear", fmt.Sprintf(`{"round":%d,"file":%q,"prints":%s}`, f.round, b.Key, shown))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "changed") || strings.Contains(w.Body.String(), "fingerprint") {
		t.Fatalf("a stale clear: %d %s", w.Code, w.Body)
	}
	if w := f.do("POST", "/api/files/clear", fmt.Sprintf(`{"round":%d,"file":%q}`, f.round, b.Key)); w.Code != 400 {
		t.Fatalf("a clear with no prints: %d %s", w.Code, w.Body)
	}
	rv := f.files()
	w = f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept","prints":%s}`, f.round, a.Key, f.prints()))
	if w.Code != 200 {
		t.Fatalf("accept after a reload: %d %s", w.Code, w.Body)
	}
	var got struct {
		Files []fileOut `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.Files) != 2 {
		t.Fatalf("the snapshot: %v %s", err, w.Body)
	}
	for i, x := range got.Files {
		if x.Decision == nil || x.Decision.Action != "accept" || x.Decision.Content != "" || x.Rec.Content != rv.Files[i].Rec.Content {
			t.Fatalf("%s: %+v %+v", x.Path, x.Decision, x.Rec)
		}
	}
	if !strings.Contains(got.Files[0].Rec.Content, "again") {
		t.Fatalf("approved %q, not the recommendation shown", got.Files[0].Rec.Content)
	}
}

// A note sets the note alone, on a file or a backlog row, and carries no
// print: it approves nothing.
func TestANoteSetsTheNoteAlone(t *testing.T) {
	f, a, b := auditFixture(t)
	f.propose(a, b, "# App\n")
	if w := f.do("PUT", "/api/notes", fmt.Sprintf(`{"round":%d,"file":%q,"note":"why"}`, f.round, a.Key)); w.Code != 409 {
		t.Fatalf("a note on an undecided file: %d %s", w.Code, w.Body)
	}
	if w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"reject","prints":%s}`, f.round, a.Key, f.prints())); w.Code != 200 {
		t.Fatalf("reject: %d %s", w.Code, w.Body)
	}
	before := f.prints()
	if w := f.do("PUT", "/api/notes", fmt.Sprintf(`{"round":%d,"file":%q,"note":"why"}`, f.round, a.Key)); w.Code != 204 {
		t.Fatalf("a note: %d %s", w.Code, w.Body)
	}
	for _, x := range f.files().Files {
		if x.Key == a.Key && (x.Decision.Action != "reject" || x.Decision.Note != "why") {
			t.Fatalf("after the note: %+v", x.Decision)
		}
	}
	if f.prints() != before {
		t.Fatal("a note changed the prints")
	}
	if w := f.do("PUT", "/api/notes", fmt.Sprintf(`{"round":%d,"note":"why"}`, f.round)); w.Code != 400 {
		t.Fatalf("a note on nothing: %d %s", w.Code, w.Body)
	}
}
