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

// A file whose recommendation keeps every finding and leaves it as it is
// shows as unchanged; agreeing (accept) mutes its findings, disagreeing
// (reject) needs a note, and Send asks the agent to recommend it again.
func TestAFileWithNothingToChange(t *testing.T) {
	f := newFixture(t)
	st.Env(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\n- Wait for #4.\n"})
	x := rec.NewFile(row.Source{File: filepath.Join(repo, "CLAUDE.md"), Repo: repo, Ref: "HEAD", Path: "CLAUDE.md"}, "repo", 6000, "# App\n\n- Wait for #4.\n")
	x.Commit, x.Rows = st.Git(t, repo, "rev-parse", "HEAD"), []string{"s1"}
	ctx := context.Background()
	id, err := f.st.RecordAudit(ctx, store.Round{Kind: "on-demand"},
		[]row.Row{{ID: "s1", Check: "stale-status", Summary: "may be stale", Source: row.Source{File: x.Source.File, Start: 3, End: 3}, Passage: "- Wait for #4."}},
		[]rec.File{x})
	if err != nil {
		t.Fatal(err)
	}
	f.round = id
	if _, err := f.st.Propose(ctx, id, []rec.Rec{{File: x.Key, Base: x.Base, Content: "# App\n\n- Wait for #4.\n", Summary: "Nothing to change.",
		Findings: []rec.Account{{Row: "s1", Did: "kept", How: "#4 is still open"}}}}); err != nil {
		t.Fatal(err)
	}
	state := func() (bool, bool) {
		var rv struct {
			Files []struct {
				Unchanged bool `json:"unchanged"`
				Muted     bool `json:"muted"`
			} `json:"files"`
		}
		if err := json.Unmarshal(f.do("GET", "/api/review", "").Body.Bytes(), &rv); err != nil || len(rv.Files) != 1 {
			t.Fatalf("%v %+v", err, rv)
		}
		return rv.Files[0].Unchanged, rv.Files[0].Muted
	}
	if u, m := state(); !u || m {
		t.Fatalf("before: unchanged %v muted %v", u, m)
	}
	w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"reject","prints":%s}`, id, x.Key, f.prints()))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "note") {
		t.Fatalf("a disagreement with no note: %d %s", w.Code, w.Body)
	}
	if w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"accept","prints":%s}`, id, x.Key, f.prints())); w.Code != 200 {
		t.Fatalf("agree: %d %s", w.Code, w.Body)
	}
	if u, m := state(); !u || !m {
		t.Fatalf("agreed: unchanged %v muted %v", u, m)
	}
	if w := f.do("PUT", "/api/files", fmt.Sprintf(`{"round":%d,"file":%q,"action":"reject","note":"#4 merged","prints":%s}`, id, x.Key, f.prints())); w.Code != 200 {
		t.Fatalf("disagree: %d %s", w.Code, w.Body)
	}
	if w := f.do("PUT", "/api/notes", fmt.Sprintf(`{"round":%d,"file":%q,"note":""}`, id, x.Key)); w.Code != 400 {
		t.Fatalf("emptying the note: %d %s", w.Code, w.Body)
	}
	if _, m := state(); m {
		t.Fatal("disagreed: still muted")
	}
}

func TestSendTextAsksForDisagreedFilesAgain(t *testing.T) {
	got := SendText(store.Send{Round: 4, Counts: store.Counts{Accept: 1, Reject: 1},
		Notes: []store.Note{{Row: "/w/a/CLAUDE.md", Note: "shorter"}, {Row: "/w/b/AGENTS.md", Note: "#4 merged", Again: true}}})
	want := "The user sent their decisions for sift round 4: 1 accepted, 0 edited, 1 rejected.\n" +
		"Notes:\n- /w/a/CLAUDE.md: shorter\n" +
		"Recommend again: the user disagrees that these files need no change. Read each note, then propose a rewrite of each file with sift_propose (or `sift propose`):\n- /w/b/AGENTS.md: #4 merged\n" +
		"Next: run sift_apply (or `sift apply`): it writes each accepted or edited file whole, on a branch per repo, and lists what it leaves to you. Then run `sift reconcile` and review each branch before it merges."
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
