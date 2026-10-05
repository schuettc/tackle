package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Undo, Send, redo, Send: the redo is a decision the second Send carries,
// and apply then applies the certain fix.
func TestRedoAfterASentUndoIsSent(t *testing.T) {
	st.Env(t)
	f := newFixture(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\nSee `docs/gone.md`.\n- Keep this.\n"})
	st.Publish(t, repo)
	dead := row.Row{ID: "dead", Check: "dead-path", Certain: true, Passage: "See `docs/gone.md`.",
		Source: row.Source{File: filepath.Join(repo, "CLAUDE.md"), Repo: repo, Ref: "origin/main", Path: "CLAUDE.md", Start: 3, End: 3}}
	id, err := f.st.RecordRound(context.Background(), store.Round{Kind: "on-demand"}, []row.Row{dead})
	if err != nil {
		t.Fatal(err)
	}
	f.round = id
	send := func() map[string]int {
		t.Helper()
		w := f.do("POST", "/api/send", fmt.Sprintf(`{"round":%d}`, id))
		if w.Code != 200 {
			t.Fatalf("send %d %s", w.Code, w.Body)
		}
		und, _ := f.st.Undelivered(context.Background())
		var c map[string]int
		b, _ := json.Marshal(und[len(und)-1].Counts)
		_ = json.Unmarshal(b, &c)
		return c
	}
	if w := f.do("POST", "/api/undo", fmt.Sprintf(`{"round":%d,"id":"dead"}`, id)); w.Code != 204 {
		t.Fatalf("undo %d %s", w.Code, w.Body)
	}
	if c := send(); c["undone"] != 1 {
		t.Fatalf("first send %v", c)
	}
	if w := f.do("POST", "/api/redo", fmt.Sprintf(`{"round":%d,"id":"dead","note":"it is gone after all"}`, id)); w.Code != 204 {
		t.Fatalf("redo %d %s", w.Code, w.Body)
	}
	if d := f.review().Rows[0].Decision; d == nil || d.Action != "accept" || d.Sent {
		t.Fatalf("redo stored %+v", d)
	}
	if c := send(); c["redone"] != 1 {
		t.Fatalf("second send carries no redo: %v", c)
	}
	res, err := apply.Run(context.Background(), apply.Options{Store: f.st, WorktreeDir: t.TempDir()})
	if err != nil || len(res.Repos) != 1 || len(res.Repos[0].Applied) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := st.Git(t, repo, "show", res.Repos[0].Branch+":CLAUDE.md"); strings.Contains(got, "gone.md") {
		t.Errorf("the fix was not applied:\n%s", got)
	}
}

// A redo is a decision, never a deletion: DELETE on a certain fix is
// refused, and redo is refused on a judgment.
func TestRedoIsNotADeletion(t *testing.T) {
	f := newFixture(t)
	if w := f.do("POST", "/api/undo", fmt.Sprintf(`{"round":%d,"id":"r-dead"}`, f.round)); w.Code != 204 {
		t.Fatalf("undo %d", w.Code)
	}
	if w := f.do("DELETE", fmt.Sprintf("/api/decisions?round=%d&id=r-dead", f.round), ""); w.Code != 400 {
		t.Fatalf("DELETE on a certain fix: %d %s", w.Code, w.Body)
	}
	if w := f.do("POST", "/api/redo", fmt.Sprintf(`{"round":%d,"id":"r-neg"}`, f.round)); w.Code != 400 {
		t.Fatalf("redo of a judgment: %d", w.Code)
	}
}

// An agent's proposal on a certain row makes it a judgment: it waits for
// the user (open), takes accept and reject, and can't be undone or redone.
func TestAProposalOnACertainRowIsAJudgment(t *testing.T) {
	f := newFixture(t)
	if _, err := f.st.AddRows(context.Background(), f.round, []row.Row{{ID: "r-dead", Verdict: "rewrite", Text: "see `docs/new.md`"}}); err != nil {
		t.Fatal(err)
	}
	if w := f.do("POST", "/api/undo", fmt.Sprintf(`{"round":%d,"id":"r-dead"}`, f.round)); w.Code != 400 {
		t.Fatalf("undo of a proposal: %d", w.Code)
	}
	var got struct {
		Open int `json:"open"`
	}
	_ = json.Unmarshal(f.do("POST", "/api/agent/review", `{"session":"a"}`).Body.Bytes(), &got)
	if got.Open != 3 {
		t.Fatalf("open %d, want 3", got.Open)
	}
	if w := f.decide("r-dead", "accept", ""); w.Code != 204 {
		t.Fatalf("accept %d", w.Code)
	}
	w := f.do("POST", "/api/send", fmt.Sprintf(`{"round":%d}`, f.round))
	if !strings.Contains(w.Body.String(), `"sent":1`) {
		t.Fatalf("send %s", w.Body)
	}
}
