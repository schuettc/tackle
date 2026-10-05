package apply_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/reconcile"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/serve"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Once the commit exists the repo is at least "branch": a failure after it
// (here, writing the pull request's body) goes into the detail. Undo then
// answers "already applied", and reconcile checks the branch.
func TestAFailureAfterTheCommitKeepsTheBranch(t *testing.T) {
	ctx := context.Background()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": "# App\n\nSee `docs/gone.md`.\n- Never force-push.\n"})
	bare := st.Publish(t, repo)
	st.Git(t, repo, "remote", "set-url", "origin", "https://github.com/owner/app.git")
	st.Git(t, repo, "config", "url."+bare+".insteadOf", "https://github.com/owner/app.git")
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	round, err := s.RecordRound(ctx, store.Round{Kind: "on-demand"}, []row.Row{{ID: "dead", Check: "dead-path", Certain: true,
		Source:  row.Source{File: filepath.Join(repo, "CLAUDE.md"), Repo: repo, Ref: "origin/main", Path: "CLAUDE.md", Start: 3, End: 3},
		Passage: "See `docs/gone.md`."}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, round, ""); err != nil {
		t.Fatal(err)
	}
	defer apply.SetTempFile(func(content string) (string, error) {
		if strings.HasPrefix(content, "sift round") {
			return "", errors.New("disk full")
		}
		return apply.WriteTemp(content)
	})()
	gh := func(context.Context, string, ...string) ([]byte, error) {
		t.Error("gh ran with no body")
		return nil, errors.New("no")
	}
	res, err := apply.Run(ctx, apply.Options{Store: s, Gh: gh, WorktreeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Repos[0]; r.State != "branch" || r.Branch != apply.Branch(round) || !strings.Contains(r.Detail, "disk full") {
		t.Fatalf("%+v", r)
	}
	as, err := s.Applies(ctx, round)
	if err != nil || len(as) != 1 || as[0].State != "branch" || !strings.Contains(as[0].Detail, "disk full") {
		t.Fatalf("applies %+v %v", as, err)
	}
	w := httptest.NewRecorder()
	serve.New(s).Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/undo", strings.NewReader(fmt.Sprintf(`{"round":%d,"id":"dead","fingerprint":%q}`, round, printOf(t, s, round, "dead")))))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "already applied") {
		t.Fatalf("undo: %d %s", w.Code, w.Body)
	}
	_, reps, err := reconcile.Run(ctx, s, round)
	if err != nil || len(reps) != 1 || reps[0].Branch != apply.Branch(round) || reps[0].Problems() != 0 {
		t.Fatalf("reconcile %+v %v", reps, err)
	}
}

// printOf is a row's fingerprint, as the page shows it.
func printOf(t *testing.T, s *store.Store, round int64, id string) string {
	t.Helper()
	_, rows, err := s.Round(context.Background(), round)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == id {
			return r.Fingerprint
		}
	}
	t.Fatalf("no row %s", id)
	return ""
}
