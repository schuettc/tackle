package recommend

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

var ctx = context.Background()

// The guidance is product text other people's agents follow: written as
// guidance (none of sift's own negative-rule patterns match it), naming no
// provider, model or person, and short.
func TestGuidanceIsGuidance(t *testing.T) {
	for _, p := range config.Default().Negative.Patterns {
		re := regexp.MustCompile("(?im)" + p)
		if m := re.FindString(Guidance); m != "" {
			t.Errorf("negative rule %q matches %q", p, m)
		}
	}
	if m := regexp.MustCompile(`(?i)\b(claude|codex|openai|anthropic|gpt|gemini|opus|sonnet|court)\b`).FindString(Guidance); m != "" {
		t.Errorf("names %q", m)
	}
	if len(Guidance) > 2500 {
		t.Errorf("%d bytes: keep it short", len(Guidance))
	}
	for _, must := range []string{"whole file", "conventions", "one line", "guidance", "duplicate", "stale", "budget", "certain", "keep it", "links", "summary"} {
		if !strings.Contains(strings.ToLower(Guidance), must) {
			t.Errorf("says nothing about %q", must)
		}
	}
}

// A round with a repo file and a global file on disk.
func fixture(t *testing.T) (*store.Store, int64, map[string]rec.File) {
	t.Helper()
	st.Env(t)
	home := st.Home(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"AGENTS.md": "# App\n\n- Never skip z.\n"})
	head := st.Git(t, repo, "rev-parse", "HEAD")
	global := st.Write(t, home, ".agent/AGENTS.md", "# G\n\n- Don't do x.\n")
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := rec.NewFile(row.Source{File: filepath.Join(repo, "AGENTS.md"), Repo: repo, Ref: "HEAD", Path: "AGENTS.md"}, "repo", 6000, "# App\n\n- Never skip z.\n")
	a.Commit = head
	g := rec.NewFile(row.Source{File: global, Canon: row.Resolve(global)}, "global", 8000, "# G\n\n- Don't do x.\n")
	rows := []row.Row{
		{ID: "n1", Check: "negative-rule", Summary: "a prohibition", Passage: "- Never skip z.", Source: row.Source{File: a.Source.File, Start: 3, End: 3}},
		{ID: "n2", Check: "negative-rule", Summary: "a prohibition", Passage: "- Don't do x.", Source: row.Source{File: global, Start: 3, End: 3}, Certain: true},
	}
	a.Rows, g.Rows = []string{"n1"}, []string{"n2"}
	id, err := s.RecordAudit(ctx, store.Round{Kind: "on-demand"}, rows, []rec.File{a, g})
	if err != nil {
		t.Fatal(err)
	}
	return s, id, map[string]rec.File{"a": a, "g": g}
}

// next hands the agent a file to recommend: its content read back at the
// audit, its findings, the guidance and how many are left; then done.
func TestNextThenProposeUntilDone(t *testing.T) {
	s, id, f := fixture(t)
	out, err := Next(ctx, s, 0)
	if err != nil || out.Done || out.Round != id || out.Left != 2 || out.File != f["a"].Key {
		t.Fatalf("%+v %v", out, err)
	}
	if out.Content != "# App\n\n- Never skip z.\n" || out.Base != f["a"].Base || out.Commit == "" || out.Guidance != Guidance {
		t.Fatalf("%+v", out)
	}
	if len(out.Findings) != 1 || out.Findings[0].Row != "n1" || out.Findings[0].Passage != "- Never skip z." || out.Findings[0].Lines != "3" {
		t.Fatalf("findings %+v", out.Findings)
	}
	res, err := Propose(ctx, s, 0, []rec.Rec{{File: out.Path, Base: out.Base, Content: "# App\n\n- Run z every time.\n",
		Findings: []rec.Account{{Row: "n1", Did: "fixed", How: "guidance"}}, Summary: "One rule as guidance."}})
	if err != nil || res.Stored != 1 || res.Left != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	out, err = Next(ctx, s, id)
	if err != nil || out.File != f["g"].Key || out.Content != "# G\n\n- Don't do x.\n" || !out.Findings[0].Certain {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := Propose(ctx, s, id, []rec.Rec{{File: out.File, Base: out.Base, Content: "# G\n\n- Do x carefully.\n",
		Findings: []rec.Account{{Row: "n2", Did: "fixed", How: "guidance"}}, Summary: "Guidance."}}); err != nil {
		t.Fatal(err)
	}
	if out, err = Next(ctx, s, 0); err != nil || !out.Done || out.Left != 0 || out.File != "" {
		t.Fatalf("%+v %v", out, err)
	}
}

// A file that changed since the audit is not handed out as if it had not.
func TestNextRefusesAFileThatChanged(t *testing.T) {
	s, _, f := fixture(t)
	if _, err := Propose(ctx, s, 0, []rec.Rec{{File: f["a"].Key, Base: f["a"].Base, Content: "# App\n\n- Run z.\n",
		Findings: []rec.Account{{Row: "n1", Did: "fixed", How: "guidance"}}, Summary: "Guidance."}}); err != nil {
		t.Fatal(err)
	}
	st.Write(t, filepath.Dir(f["g"].Source.File), "AGENTS.md", "# G\n\n- edited since\n")
	if _, err := Next(ctx, s, 0); err == nil || !strings.Contains(err.Error(), "changed since the audit") {
		t.Fatalf("got %v", err)
	}
}
