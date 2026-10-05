package apply

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

var ctx = context.Background()

const claude = `# App

## Git

- Never push to main.
- Never force-push.

## Tests

- Run the slow suite before a release.

## Notes

See ` + "`docs/gone.md`" + ` for the layout.
- Keep the changelog current.
- Keep the changelog up to date.
`

type rig struct {
	t     *testing.T
	repo  string
	bare  string
	db    string
	s     *store.Store
	round int64
	rows  []row.Row
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": claude, "docs/other.md": "# Other\n\n## Notes\n\n- one\n"})
	bare := st.Publish(t, repo)
	db := filepath.Join(t.TempDir(), "sift.db")
	s, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &rig{t: t, repo: repo, bare: bare, db: db, s: s}
}

// at builds a row about lines of CLAUDE.md in the rig's repo.
func (g *rig) at(id, check string, start int, passage string) row.Row {
	return row.Row{ID: id, Check: check, Summary: check, Passage: passage,
		Source: row.Source{File: filepath.Join(g.repo, "CLAUDE.md"), Repo: g.repo, Ref: "origin/main", Path: "CLAUDE.md", Start: start, End: start + strings.Count(passage, "\n")}}
}

func (g *rig) record(rows ...row.Row) {
	g.t.Helper()
	id, err := g.s.RecordRound(ctx, store.Round{Kind: "on-demand"}, rows)
	if err != nil {
		g.t.Fatal(err)
	}
	g.round, g.rows = id, rows
}

func (g *rig) decide(id string, d row.Decision) {
	g.t.Helper()
	if err := g.s.Decide(ctx, g.round, id, d); err != nil {
		g.t.Fatal(err)
	}
}

// run presses Send, as the user does before the agent applies, then
// applies.
func (g *rig) run(o Options) Result {
	g.t.Helper()
	if _, err := g.s.Send(ctx, g.round, ""); err != nil {
		g.t.Fatal(err)
	}
	return g.runUnsent(o)
}

// runUnsent applies without pressing Send.
func (g *rig) runUnsent(o Options) Result {
	g.t.Helper()
	o.Store, o.Round = g.s, g.round
	if o.WorktreeDir == "" {
		o.WorktreeDir = g.t.TempDir()
	}
	res, err := Run(ctx, o)
	if err != nil {
		g.t.Fatal(err)
	}
	return res
}

func (g *rig) show(ref, path string) string {
	g.t.Helper()
	return st.Git(g.t, g.repo, "show", ref+":"+path) + "\n"
}

func TestApplyEachVerdict(t *testing.T) {
	g := newRig(t)
	rows := []row.Row{
		g.at("neg1", "negative-rule", 5, "- Never push to main."),
		g.at("neg2", "negative-rule", 6, "- Never force-push."),
		g.at("slow", "misplaced", 10, "- Run the slow suite before a release."),
		g.at("dead", "dead-path", 14, "See `docs/gone.md` for the layout."),
		g.at("dup1", "duplicate", 15, "- Keep the changelog current."),
		g.at("dup2", "duplicate", 16, "- Keep the changelog up to date."),
		g.at("judged", "negative-rule", 9, "## Tests"),
		{ID: "glob", Check: "misplaced", Source: row.Source{File: "/home/u/.claude/CLAUDE.md", Start: 3, End: 3}, Passage: "x"},
	}
	rows[3].Certain = true // applied with no decision
	g.record(rows...)
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{
		{ID: "neg1", Verdict: "rewrite", Text: "- Push to a branch and open a pull request."},
		{ID: "neg2", Verdict: "delete"},
		{ID: "slow", Verdict: "move", Destination: "docs/other.md#Notes", Text: "- Run the slow suite before a release (app)."},
		{ID: "dup2", Verdict: "merge:dup1", Text: "- Keep the changelog current with every change."},
		{ID: "judged", Verdict: "delete"},
		{ID: "glob", Verdict: "delete"},
	}); err != nil {
		t.Fatal(err)
	}
	g.decide("neg1", row.Decision{Action: "accept"})
	g.decide("neg2", row.Decision{Action: "edit", Verdict: "rewrite", Text: "- Rebase before you push; never rewrite shared history."})
	g.decide("slow", row.Decision{Action: "accept"})
	g.decide("dup2", row.Decision{Action: "accept"})
	g.decide("judged", row.Decision{Action: "reject"})
	g.decide("glob", row.Decision{Action: "accept"})

	res := g.run(Options{})
	if len(res.Repos) != 1 {
		t.Fatalf("repos %+v", res.Repos)
	}
	r := res.Repos[0]
	if r.State != "branch" || r.Branch != "sift/round-"+strconv.FormatInt(g.round, 10) || len(r.Applied) != 5 || len(r.Skipped) != 0 {
		t.Fatalf("repo %+v", r)
	}
	if len(res.Left) != 1 || res.Left[0].Row != "glob" || !strings.Contains(res.Left[0].Why, "not in a git repo") {
		t.Fatalf("left %+v", res.Left)
	}
	want := `# App

## Git

- Push to a branch and open a pull request.
- Rebase before you push; never rewrite shared history.

## Tests

## Notes

- Keep the changelog current with every change.
`
	if got := g.show(r.Branch, "CLAUDE.md"); got != want {
		t.Errorf("CLAUDE.md\n%s\nwant\n%s", got, want)
	}
	if got := g.show(r.Branch, "docs/other.md"); got != "# Other\n\n## Notes\n\n- one\n- Run the slow suite before a release (app).\n" {
		t.Errorf("docs/other.md\n%s", got)
	}
	// The primary clone's checkout is untouched, the worktree is gone, the
	// apply is recorded.
	if b, _ := os.ReadFile(filepath.Join(g.repo, "CLAUDE.md")); string(b) != claude {
		t.Error("the primary clone changed")
	}
	if out := st.Git(t, g.repo, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("worktree left behind:\n%s", out)
	}
	msg := st.Git(t, g.repo, "log", "-1", "--format=%B", r.Branch)
	if !strings.Contains(msg, "sift: round") || !strings.Contains(msg, "neg1") {
		t.Errorf("commit message %q", msg)
	}
	as, _ := g.s.Applies(ctx, g.round)
	if len(as) != 1 || as[0].State != "branch" || len(as[0].Rows) != 5 {
		t.Errorf("applies %+v", as)
	}
}

func TestApplyHoldsADirtyRepo(t *testing.T) {
	g := newRig(t)
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."))
	_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}})
	g.decide("neg1", row.Decision{Action: "accept"})
	st.Write(t, g.repo, "CLAUDE.md", claude+"- wip\n")
	r := g.run(Options{}).Repos[0]
	if r.State != "held" || !strings.Contains(r.Detail, "uncommitted") || !strings.Contains(r.Detail, "CLAUDE.md") {
		t.Fatalf("%+v", r)
	}
	if out := st.Git(t, g.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("a held repo got a branch: %s", out)
	}
}

func TestApplyHoldsUnpushedWork(t *testing.T) {
	g := newRig(t)
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."))
	_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}})
	g.decide("neg1", row.Decision{Action: "accept"})
	st.Commit(t, g.repo, map[string]string{"NOTES.md": "local\n"})
	r := g.run(Options{}).Repos[0]
	if r.State != "held" || !strings.Contains(r.Detail, "unpushed") {
		t.Fatalf("%+v", r)
	}
}

// With gh and a GitHub remote, apply pushes the branch and opens a pull
// request with --body-file.
func TestApplyOpensAPullRequest(t *testing.T) {
	g := newRig(t)
	st.Git(t, g.repo, "remote", "set-url", "origin", "https://github.com/owner/app.git")
	st.Git(t, g.repo, "config", "url."+g.bare+".insteadOf", "https://github.com/owner/app.git")
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."))
	_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}})
	g.decide("neg1", row.Decision{Action: "accept", Note: "obvious"})
	var calls [][]string
	var body string
	gh := func(_ context.Context, dir string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		for i, a := range args {
			if a == "--body-file" {
				b, _ := os.ReadFile(args[i+1])
				body = string(b)
			}
		}
		return []byte("https://github.com/owner/app/pull/9\n"), nil
	}
	r := g.run(Options{Gh: gh}).Repos[0]
	if r.State != "pr" || r.PR != "https://github.com/owner/app/pull/9" {
		t.Fatalf("%+v", r)
	}
	if len(calls) != 1 || strings.Join(calls[0][:2], " ") != "pr create" || !contains(calls[0], "--repo", "owner/app") ||
		!contains(calls[0], "--base", "main") || !contains(calls[0], "--head", r.Branch) {
		t.Fatalf("gh %v", calls)
	}
	if !strings.Contains(body, "neg1") || !strings.Contains(body, "obvious") {
		t.Errorf("body %q", body)
	}
	if out := st.Git(t, filepath.Dir(g.bare), "--git-dir", g.bare, "branch", "--list", r.Branch); !strings.Contains(out, r.Branch) {
		t.Errorf("branch not pushed: %q", out)
	}
}

func contains(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

// The base moved since the audit: a passage that moved is still found; one
// that is gone is skipped, and a repo with nothing left gets no branch.
func TestApplyAfterTheBaseMoved(t *testing.T) {
	g := newRig(t)
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."), g.at("neg2", "negative-rule", 6, "- Never force-push."))
	_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}, {ID: "neg2", Verdict: "delete"}})
	g.decide("neg1", row.Decision{Action: "accept"})
	g.decide("neg2", row.Decision{Action: "accept"})
	moved := strings.Replace(claude, "## Git\n\n", "## Git\n\nIntro line.\n\n", 1)
	moved = strings.Replace(moved, "- Never force-push.\n", "", 1)
	st.Commit(t, g.repo, map[string]string{"CLAUDE.md": moved})
	st.Git(t, g.repo, "push", "-q", "origin", "main")
	r := g.run(Options{}).Repos[0]
	if r.State != "branch" || len(r.Applied) != 1 || len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0].Why, "not found") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(g.show(r.Branch, "CLAUDE.md"), "Never push to main") {
		t.Error("the moved passage was not deleted")
	}

	h := newRig(t)
	h.record(h.at("gone", "negative-rule", 5, "- Never sleep."))
	_, _ = h.s.AddRows(ctx, h.round, []row.Row{{ID: "gone", Verdict: "delete"}})
	h.decide("gone", row.Decision{Action: "accept"})
	if r := h.run(Options{}).Repos[0]; r.State != "nothing" || r.Branch != "" {
		t.Fatalf("%+v", r)
	}
	if out := st.Git(t, h.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("an empty apply left a branch: %s", out)
	}
}

func TestApplyTwiceIsHeld(t *testing.T) {
	g := newRig(t)
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."))
	_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}})
	g.decide("neg1", row.Decision{Action: "accept"})
	g.run(Options{})
	r := g.run(Options{}).Repos[0]
	if r.State != "held" || !strings.Contains(r.Detail, "already exists") {
		t.Fatalf("%+v", r)
	}
}

func TestApplyDryRunTouchesNothing(t *testing.T) {
	g := newRig(t)
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."))
	_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}})
	g.decide("neg1", row.Decision{Action: "accept"})
	r := g.run(Options{DryRun: true}).Repos[0]
	if r.State != "planned" || len(r.Applied) != 1 {
		t.Fatalf("%+v", r)
	}
	if out := st.Git(t, g.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("dry run made a branch: %s", out)
	}
	if as, _ := g.s.Applies(ctx, g.round); len(as) != 0 {
		t.Fatalf("dry run recorded %+v", as)
	}
}

// An undone certain row is not applied.
func TestApplySkipsAnUndoneCertainRow(t *testing.T) {
	g := newRig(t)
	dead := g.at("dead", "dead-path", 14, "See `docs/gone.md` for the layout.")
	dead.Certain = true
	g.record(dead)
	g.decide("dead", row.Decision{Action: "reject"})
	res := g.run(Options{})
	if len(res.Repos) != 0 {
		t.Fatalf("%+v", res.Repos)
	}
}

// A decision counts once it is sent: apply runs after Send, and a row the
// user decided since is not approved yet. An undo needs no send: leaving a
// file alone is always safe.
func TestApplyTakesOnlySentDecisions(t *testing.T) {
	g := newRig(t)
	dead := g.at("dead", "dead-path", 14, "See `docs/gone.md` for the layout.")
	dead.Certain = true
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."), g.at("neg2", "negative-rule", 6, "- Never force-push."), dead)
	_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}, {ID: "neg2", Verdict: "delete"}})
	g.decide("neg1", row.Decision{Action: "accept"})
	if _, err := g.s.Send(ctx, g.round, ""); err != nil {
		t.Fatal(err)
	}
	g.decide("neg2", row.Decision{Action: "accept"}) // after the send
	g.decide("dead", row.Decision{Action: "reject"}) // an undo, unsent
	res := g.runUnsent(Options{})
	r := res.Repos[0]
	if len(r.Applied) != 1 || r.Applied[0].Row != "neg1" || res.Unsent != 1 {
		t.Fatalf("%+v unsent %d", r, res.Unsent)
	}
}

// The agent changed an intake row after the user sent its acceptance: the
// acceptance was for the old row, so apply does not write the new one.
func TestApplySkipsAnIntakeRowChangedAfterItsSend(t *testing.T) {
	g := newRig(t)
	g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."))
	in := g.at("mem", "intake", 6, "- Never force-push.")
	in.Verdict = "delete"
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{in}); err != nil {
		t.Fatal(err)
	}
	g.decide("mem", row.Decision{Action: "accept"})
	if _, err := g.s.Send(ctx, g.round, ""); err != nil {
		t.Fatal(err)
	}
	in.Passage, in.Source.Start, in.Source.End = "- Never push to main.", 5, 5
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{in}); err != nil {
		t.Fatal(err)
	}
	if res := g.runUnsent(Options{}); len(res.Repos) != 0 {
		t.Fatalf("applied a changed intake row: %+v", res.Repos)
	}
}

// A certain row's own fix applies with no decision. An agent's proposal that
// replaces it makes the row a judgment: not applied until the user accepts
// it and sends.
func TestApplyAProposalOnACertainRowNeedsApproval(t *testing.T) {
	g := newRig(t)
	plain := g.at("plain", "dead-path", 14, "See `docs/gone.md` for the layout.")
	plain.Certain = true
	proposed := g.at("proposed", "dead-path", 5, "- Never push to main.")
	proposed.Certain = true
	g.record(plain, proposed)
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{{ID: "proposed", Verdict: "rewrite", Text: "- Rewritten by the agent."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.Send(ctx, g.round, ""); err != nil {
		t.Fatal(err)
	}
	g.decide("proposed", row.Decision{Action: "accept"}) // not sent
	res := g.runUnsent(Options{DryRun: true})
	if r := res.Repos[0]; len(r.Applied) != 1 || r.Applied[0].Row != "plain" || r.Applied[0].Verdict != "delete" || res.Unsent != 1 {
		t.Fatalf("%+v unsent %d", r, res.Unsent)
	}
	// Sent, it applies as the proposal.
	res = g.run(Options{})
	r := res.Repos[0]
	if len(r.Applied) != 2 {
		t.Fatalf("%+v", r)
	}
	if got := g.show(r.Branch, "CLAUDE.md"); !strings.Contains(got, "- Rewritten by the agent.") || strings.Contains(got, "docs/gone.md") {
		t.Errorf("CLAUDE.md\n%s", got)
	}
}

// An edit that clears a move's text moves the passage itself.
func TestApplyAnEditThatClearsTheText(t *testing.T) {
	g := newRig(t)
	g.record(g.at("slow", "misplaced", 10, "- Run the slow suite before a release."))
	if _, err := g.s.AddRows(ctx, g.round, []row.Row{{ID: "slow", Verdict: "move", Destination: "docs/other.md#Notes", Text: "- Reworded by the agent."}}); err != nil {
		t.Fatal(err)
	}
	g.decide("slow", row.Decision{Action: "edit", Cleared: []string{"text"}})
	r := g.run(Options{}).Repos[0]
	if got := g.show(r.Branch, "docs/other.md"); got != "# Other\n\n## Notes\n\n- one\n- Run the slow suite before a release.\n" {
		t.Errorf("docs/other.md\n%s", got)
	}
}

// An untracked instruction file (a name an enabled profile loads) holds the
// repo: it may be the real destination, not committed yet. Any other
// untracked file does not.
func TestApplyHoldsForAnUntrackedInstructionFile(t *testing.T) {
	for name, c := range map[string]struct {
		file  string
		names []string
		held  bool
	}{
		"CLAUDE.md, default names": {"docs/CLAUDE.md", nil, true},
		"SKILL.md":                 {"skills/x/SKILL.md", nil, true},
		"AGENTS.md, codex only":    {"pkg/AGENTS.md", []string{"AGENTS.override.md", "AGENTS.md", "SKILL.md"}, true},
		"CLAUDE.md, codex only":    {"pkg/CLAUDE.md", []string{"AGENTS.override.md", "AGENTS.md", "SKILL.md"}, false},
		"a stray file":             {"notes.txt", nil, false},
	} {
		g := newRig(t)
		g.record(g.at("neg1", "negative-rule", 5, "- Never push to main."))
		_, _ = g.s.AddRows(ctx, g.round, []row.Row{{ID: "neg1", Verdict: "delete"}})
		g.decide("neg1", row.Decision{Action: "accept"})
		st.Write(t, g.repo, c.file, "# untracked\n")
		r := g.run(Options{DryRun: true, Instructions: c.names}).Repos[0]
		if held := r.State == "held"; held != c.held || (held && !strings.Contains(r.Detail, c.file)) {
			t.Errorf("%s: %+v", name, r)
		}
	}
}
