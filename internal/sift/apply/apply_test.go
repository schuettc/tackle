package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

var ctx = context.Background()

const claude = "# App\n\n## Git\n\n- Never push to main.\n- Never force-push.\n"
const other = "# Other\n\n## Notes\n\n- one\n"

// rig is two published repos (app: CLAUDE.md and docs/other.md; lib:
// AGENTS.md) and a global file on disk, and a store.
type rig struct {
	t      *testing.T
	repo   string
	bare   string
	lib    string
	global string
	s      *store.Store
	round  int64
	files  []rec.File
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st.Env(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{"CLAUDE.md": claude, "docs/other.md": other})
	bare := st.Publish(t, repo)
	lib := st.Repo(t, filepath.Join(t.TempDir(), "lib"), map[string]string{"AGENTS.md": "# Lib\n\n- Don't break the API.\n"})
	st.Publish(t, lib)
	global := st.Write(t, t.TempDir(), "AGENTS.md", "# Global\n\n- In app, never skip CI.\n")
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "sift.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &rig{t: t, repo: repo, bare: bare, lib: lib, global: global, s: s}
}

// in is repo's file rel as the audit reads it: at origin/main.
func (g *rig) in(repo, rel string) rec.File {
	g.t.Helper()
	body := st.Git(g.t, repo, "show", "origin/main:"+rel) + "\n"
	f := rec.NewFile(row.Source{File: filepath.Join(repo, rel), Repo: repo, Ref: "origin/main", Path: rel}, "repo", 6000, body)
	f.Commit = st.Git(g.t, repo, "rev-parse", "origin/main")
	return f
}

func (g *rig) disk(p string) rec.File {
	g.t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		g.t.Fatal(err)
	}
	return rec.NewFile(row.Source{File: p, Canon: row.Resolve(p)}, "global", 8000, string(b))
}

// audit records a round of these files, one stale-status finding each.
func (g *rig) audit(fs ...rec.File) {
	g.t.Helper()
	var rows []row.Row
	for i := range fs {
		id := "n-" + fs[i].Key
		fs[i].Rows = []string{id}
		rows = append(rows, row.Row{ID: id, Check: "stale-status", Summary: "stale status", Source: fs[i].Source})
	}
	id, err := g.s.RecordAudit(ctx, store.Round{Kind: "on-demand"}, rows, fs)
	if err != nil {
		g.t.Fatal(err)
	}
	g.round, g.files = id, fs
}

func (g *rig) rec(f rec.File, content string, links ...rec.File) rec.Rec {
	r := rec.Rec{File: f.Key, Base: f.Base, Content: content, Summary: "Drops the stale status.",
		Findings: []rec.Account{{Row: "n-" + f.Key, Did: "fixed", How: "removed"}}}
	for _, l := range links {
		r.Links = append(r.Links, l.Key)
	}
	return r
}

func (g *rig) propose(rs ...rec.Rec) {
	g.t.Helper()
	if _, err := g.s.Propose(ctx, g.round, rs); err != nil {
		g.t.Fatal(err)
	}
}

// decide answers f's recommendation with what the page shows: each file's
// print and decision id.
func (g *rig) decide(f rec.File, d rec.Decision) {
	g.t.Helper()
	items, err := g.s.Files(ctx, g.round)
	if err != nil {
		g.t.Fatal(err)
	}
	seen := map[string]store.Seen{}
	for _, it := range items {
		seen[it.Key] = store.Seen{Fingerprint: it.Fingerprint}
		if it.Decision != nil {
			seen[it.Key] = store.Seen{Fingerprint: it.Fingerprint, Decision: it.Decision.ID}
		}
	}
	if _, err := g.s.DecideFile(ctx, g.round, f.Key, d, seen); err != nil {
		g.t.Fatal(err)
	}
}

// run presses Send, as the user does before the agent applies, then
// applies.
func (g *rig) run(o Options) Result {
	g.t.Helper()
	if _, err := g.s.Send(ctx, g.round, "", nil); err != nil {
		g.t.Fatal(err)
	}
	return g.runUnsent(o)
}

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

func (g *rig) show(repo, ref, path string) string {
	g.t.Helper()
	return st.Git(g.t, repo, "show", ref+":"+path) + "\n"
}

// one is the round's only repo result.
func one(t *testing.T, res Result) Repo {
	t.Helper()
	if len(res.Repos) != 1 {
		t.Fatalf("repos %+v", res.Repos)
	}
	return res.Repos[0]
}

// apply writes each accepted file's recommendation and each edited file's
// own content, whole and exactly, on one branch per repo; a rejected file
// is left alone, and so is everything not approved.
func TestApplyWritesTheApprovedFiles(t *testing.T) {
	g := newRig(t)
	c, o, l := g.in(g.repo, "CLAUDE.md"), g.in(g.repo, "docs/other.md"), g.in(g.lib, "AGENTS.md")
	g.audit(c, o, l)
	g.propose(g.rec(c, "# App\n\n## Git\n\n- Push to a branch and open a pull request.\n"),
		g.rec(o, "# Other\n\n## Notes\n\n- one, rewritten\n"),
		g.rec(l, "# Lib\n\n- Keep the API stable.\n"))
	g.decide(c, rec.Decision{Action: "accept", Note: "good"})
	g.decide(o, rec.Decision{Action: "edit", Content: "# Other\n\n## Notes\n\n- my own words\n"})
	g.decide(l, rec.Decision{Action: "reject"})
	res := g.run(Options{})
	r := one(t, res)
	if r.State != "branch" || r.Path != g.repo || len(r.Applied) != 2 || len(r.Skipped) != 0 {
		t.Fatalf("%+v", r)
	}
	if got := g.show(g.repo, r.Branch, "CLAUDE.md"); got != "# App\n\n## Git\n\n- Push to a branch and open a pull request.\n" {
		t.Errorf("CLAUDE.md:\n%s", got)
	}
	if got := g.show(g.repo, r.Branch, "docs/other.md"); got != "# Other\n\n## Notes\n\n- my own words\n" {
		t.Errorf("docs/other.md:\n%s", got)
	}
	if got := st.Git(t, g.repo, "diff", "--name-only", "origin/main", r.Branch); got != "CLAUDE.md\ndocs/other.md" {
		t.Errorf("changed: %q", got)
	}
	if out := st.Git(t, g.lib, "branch", "--list", "sift/*"); out != "" {
		t.Errorf("the rejected file's repo got a branch: %s", out)
	}
	if st.Git(t, g.repo, "status", "--porcelain") != "" {
		t.Error("the primary clone changed")
	}
	as, _ := g.s.Applies(ctx, g.round)
	if len(as) != 1 || as[0].State != "branch" || strings.Join(as[0].Rows, ",") != c.Key+","+o.Key {
		t.Errorf("applies %+v", as)
	}
}

// The page shows an edited file's edit, so accepting it then approves the
// edit: edit, come back, accept, Send, and apply writes the edited content.
func TestAcceptAfterAnEditWritesTheEdit(t *testing.T) {
	g := newRig(t)
	c := g.in(g.repo, "CLAUDE.md")
	g.audit(c)
	g.propose(g.rec(c, "# App\n\n## Git\n\n- Push to a branch and open a pull request.\n"))
	mine := "# App\n\n## Git\n\n- Push to a branch; main takes merges only.\n"
	g.decide(c, rec.Decision{Action: "edit", Content: mine})
	g.decide(c, rec.Decision{Action: "accept"})
	r := one(t, g.run(Options{}))
	if r.State != "branch" || len(r.Applied) != 1 {
		t.Fatalf("%+v", r)
	}
	if got := g.show(g.repo, r.Branch, "CLAUDE.md"); got != mine {
		t.Fatalf("wrote:\n%s\nwant the edit:\n%s", got, mine)
	}
}

// A decision counts once it is sent: before Send apply writes nothing and
// says how many wait.
func TestApplyTakesOnlySentDecisions(t *testing.T) {
	g := newRig(t)
	c := g.in(g.repo, "CLAUDE.md")
	g.audit(c)
	g.propose(g.rec(c, "# App\n\n- Push to a branch.\n"))
	g.decide(c, rec.Decision{Action: "accept"})
	res := g.runUnsent(Options{})
	if len(res.Repos) != 0 || res.Unsent != 1 {
		t.Fatalf("%+v", res)
	}
	if r := one(t, g.run(Options{})); r.State != "branch" {
		t.Fatalf("%+v", r)
	}
}

// A file whose base moved on since the audit is held, with the reason; the
// repo's other files still apply. A repo with nothing left gets no branch.
func TestApplyHoldsAFileWhoseBaseChanged(t *testing.T) {
	g := newRig(t)
	c, o := g.in(g.repo, "CLAUDE.md"), g.in(g.repo, "docs/other.md")
	g.audit(c, o)
	g.propose(g.rec(c, "# App\n\n- Push to a branch.\n"), g.rec(o, "# Other\n\n- two\n"))
	g.decide(c, rec.Decision{Action: "accept"})
	g.decide(o, rec.Decision{Action: "accept"})
	st.Commit(t, g.repo, map[string]string{"docs/other.md": other + "- added since\n"})
	st.Git(t, g.repo, "push", "-q", "origin", "main")
	r := one(t, g.run(Options{}))
	if r.State != "branch" || len(r.Applied) != 1 || r.Applied[0].Key != c.Key || len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0].Why, "changed") {
		t.Fatalf("%+v", r)
	}
	if got := g.show(g.repo, r.Branch, "docs/other.md"); got != other+"- added since\n" {
		t.Errorf("the changed file was written:\n%s", got)
	}

	h := newRig(t)
	c = h.in(h.repo, "CLAUDE.md")
	h.audit(c)
	h.propose(h.rec(c, "# App\n\n- Push to a branch.\n"))
	h.decide(c, rec.Decision{Action: "accept"})
	st.Commit(t, h.repo, map[string]string{"CLAUDE.md": claude + "- added since\n"})
	st.Git(t, h.repo, "push", "-q", "origin", "main")
	if r := one(t, h.run(Options{})); r.State != "nothing" || r.Branch != "" {
		t.Fatalf("%+v", r)
	}
	if out := st.Git(t, h.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("an empty apply left a branch: %s", out)
	}
}

// Linked files apply together: when one side is held, the other is too,
// in whichever repo it is.
func TestApplyHoldsLinkedFilesTogether(t *testing.T) {
	g := newRig(t)
	c, o, l := g.in(g.repo, "CLAUDE.md"), g.in(g.repo, "docs/other.md"), g.in(g.lib, "AGENTS.md")
	g.audit(c, o, l)
	g.propose(g.rec(c, "# App\n\n- Push to a branch.\n", l), g.rec(l, "# Lib\n\n- Keep the API stable.\n- Push to a branch.\n", c),
		g.rec(o, "# Other\n\n- two\n"))
	g.decide(c, rec.Decision{Action: "accept"})
	g.decide(o, rec.Decision{Action: "accept"})
	st.Write(t, g.lib, "AGENTS.md", "# wip\n")
	res := g.run(Options{})
	if len(res.Repos) != 2 {
		t.Fatalf("%+v", res.Repos)
	}
	by := map[string]Repo{}
	for _, r := range res.Repos {
		by[r.Path] = r
	}
	if r := by[g.lib]; r.State != "held" {
		t.Fatalf("lib %+v", r)
	}
	app := by[g.repo]
	if app.State != "branch" || len(app.Applied) != 1 || app.Applied[0].Key != o.Key || len(app.Skipped) != 1 ||
		app.Skipped[0].Key != c.Key || !strings.Contains(app.Skipped[0].Why, "linked") {
		t.Fatalf("app %+v", app)
	}
	if got := g.show(g.repo, app.Branch, "CLAUDE.md"); got != claude {
		t.Errorf("the linked file was written without its other side:\n%s", got)
	}
}

// A file outside any repo (a global file) has no branch to go on: it is
// left for the user, with its approved content saved beside sift's state;
// a dry run saves nothing.
func TestApplyLeavesAFileOutsideARepo(t *testing.T) {
	g := newRig(t)
	gl := g.disk(g.global)
	g.audit(gl)
	g.propose(g.rec(gl, "# Global\n\n- Run CI on every change.\n"))
	g.decide(gl, rec.Decision{Action: "accept"})
	dry := g.run(Options{DryRun: true})
	if len(dry.Left) != 1 || dry.Left[0].Approved != "" {
		t.Fatalf("dry run %+v", dry.Left)
	}
	res := g.run(Options{})
	if len(res.Left) != 1 || res.Left[0].Key != gl.Key || !strings.Contains(res.Left[0].Why, "not in a git repo") || len(res.Repos) != 0 {
		t.Fatalf("%+v", res)
	}
	b, err := os.ReadFile(res.Left[0].Approved)
	if err != nil || string(b) != "# Global\n\n- Run CI on every change.\n" {
		t.Fatalf("approved copy %q %v", b, err)
	}
	if b, _ := os.ReadFile(g.global); string(b) != "# Global\n\n- In app, never skip CI.\n" {
		t.Errorf("the global file was written: %q", b)
	}
}

// moveToGlobal is a move from app's CLAUDE.md to the global file, both
// sides accepted and sent.
func moveToGlobal(g *rig) (gl, c rec.File) {
	gl, c = g.disk(g.global), g.in(g.repo, "CLAUDE.md")
	g.audit(gl, c)
	g.propose(g.rec(gl, "# Global\n\n- In app, never skip CI.\n- Never force-push.\n", c), g.rec(c, "# App\n\n## Git\n\n- Never push to main.\n", gl))
	g.decide(gl, rec.Decision{Action: "accept"})
	return gl, c
}

// A file outside any repo can't be written, so a move linked to it is held
// whole: the repo side gets no branch, and the global side's approved
// content is still saved for the user.
func TestApplyHoldsAMoveToAFileOutsideARepo(t *testing.T) {
	g := newRig(t)
	gl, c := moveToGlobal(g)
	res := g.run(Options{})
	r := one(t, res)
	if r.State != "nothing" || r.Branch != "" || len(r.Applied) != 0 || len(r.Skipped) != 1 || r.Skipped[0].Key != c.Key ||
		!strings.Contains(r.Skipped[0].Why, "linked") || !strings.Contains(r.Skipped[0].Why, "not in a git repo") {
		t.Fatalf("the repo side: %+v", r)
	}
	if out := st.Git(t, g.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("the repo side was written without its other side: %s", out)
	}
	if len(res.Left) != 1 || res.Left[0].Key != gl.Key {
		t.Fatalf("left %+v", res.Left)
	}
	if b, err := os.ReadFile(res.Left[0].Approved); err != nil || string(b) != "# Global\n\n- In app, never skip CI.\n- Never force-push.\n" {
		t.Fatalf("approved copy %q %v", b, err)
	}
}

// When the approved copy can't be saved, apply says so and still holds the
// move whole.
func TestApplyHoldsAMoveWhenTheApprovedCopyCannotBeSaved(t *testing.T) {
	g := newRig(t)
	home := t.TempDir()
	t.Setenv("SIFT_HOME", home)
	st.Write(t, home, "state", "a file where sift's state directory goes\n")
	gl, c := moveToGlobal(g)
	res := g.run(Options{})
	if len(res.Left) != 1 || res.Left[0].Key != gl.Key || res.Left[0].Approved != "" || !strings.Contains(res.Left[0].Why, "saving it failed") {
		t.Fatalf("left %+v", res.Left)
	}
	if r := one(t, res); r.State != "nothing" || len(r.Skipped) != 1 || r.Skipped[0].Key != c.Key {
		t.Fatalf("the repo side: %+v", r)
	}
	if out := st.Git(t, g.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("the repo side was written: %s", out)
	}
	if b, _ := os.ReadFile(g.global); string(b) != "# Global\n\n- In app, never skip CI.\n" {
		t.Errorf("the global file was written: %q", b)
	}
}

// A backlog round is decided per item: apply writes no file and leaves the
// approved rows to the agent.
func TestApplyLeavesABacklogRoundsRowsToTheAgent(t *testing.T) {
	g := newRig(t)
	id, err := g.s.RecordRound(ctx, store.Round{Kind: "backlog"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.round = id
	if _, err := g.s.AddRows(ctx, id, []row.Row{{ID: "i1", Check: "intake", Verdict: "issue", Title: "t", Destination: "o/r", Source: row.Source{Entry: "1"}},
		{ID: "i2", Check: "intake", Verdict: "close:done", Source: row.Source{Entry: "2"}}}); err != nil {
		t.Fatal(err)
	}
	if err := g.s.Decide(ctx, id, "i1", row.Decision{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if err := g.s.Decide(ctx, id, "i2", row.Decision{Action: "reject"}); err != nil {
		t.Fatal(err)
	}
	res := g.run(Options{})
	if len(res.Repos) != 0 || len(res.Left) != 1 || res.Left[0].Key != "i1" || res.Left[0].Action != "issue" {
		t.Fatalf("%+v", res)
	}
}

func approvedOne(g *rig) rec.File {
	c := g.in(g.repo, "CLAUDE.md")
	g.audit(c)
	g.propose(g.rec(c, "# App\n\n- Push to a branch.\n"))
	g.decide(c, rec.Decision{Action: "accept", Note: "obvious"})
	return c
}

func TestApplyHoldsADirtyRepo(t *testing.T) {
	g := newRig(t)
	approvedOne(g)
	st.Write(t, g.repo, "CLAUDE.md", claude+"- wip\n")
	r := one(t, g.run(Options{}))
	if r.State != "held" || !strings.Contains(r.Detail, "uncommitted") || !strings.Contains(r.Detail, "CLAUDE.md") {
		t.Fatalf("%+v", r)
	}
	if out := st.Git(t, g.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("a held repo got a branch: %s", out)
	}
}

func TestApplyHoldsUnpushedWork(t *testing.T) {
	g := newRig(t)
	approvedOne(g)
	st.Commit(t, g.repo, map[string]string{"NOTES.md": "local\n"})
	if r := one(t, g.run(Options{})); r.State != "held" || !strings.Contains(r.Detail, "unpushed") {
		t.Fatalf("%+v", r)
	}
}

// With gh and a GitHub remote, apply pushes the branch and opens a pull
// request with --body-file: each file with its summary, what was done about
// each finding, and the user's note.
func TestApplyOpensAPullRequest(t *testing.T) {
	g := newRig(t)
	st.Git(t, g.repo, "remote", "set-url", "origin", "https://github.com/owner/app.git")
	st.Git(t, g.repo, "config", "url."+g.bare+".insteadOf", "https://github.com/owner/app.git")
	approvedOne(g)
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
	r := one(t, g.run(Options{Gh: gh}))
	if r.State != "pr" || r.PR != "https://github.com/owner/app/pull/9" {
		t.Fatalf("%+v", r)
	}
	if len(calls) != 1 || strings.Join(calls[0][:2], " ") != "pr create" || !contains(calls[0], "--repo", "owner/app") ||
		!contains(calls[0], "--base", "main") || !contains(calls[0], "--head", r.Branch) {
		t.Fatalf("gh %v", calls)
	}
	for _, want := range []string{"CLAUDE.md", "Drops the stale status.", "fixed: removed", "obvious"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
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

func TestApplyTwiceIsHeld(t *testing.T) {
	g := newRig(t)
	approvedOne(g)
	g.run(Options{})
	if r := one(t, g.run(Options{})); r.State != "held" || !strings.Contains(r.Detail, "already exists") {
		t.Fatalf("%+v", r)
	}
}

func TestApplyDryRunTouchesNothing(t *testing.T) {
	g := newRig(t)
	approvedOne(g)
	if r := one(t, g.run(Options{DryRun: true})); r.State != "planned" || len(r.Applied) != 1 {
		t.Fatalf("%+v", r)
	}
	if out := st.Git(t, g.repo, "branch", "--list", "sift/*"); out != "" {
		t.Fatalf("dry run made a branch: %s", out)
	}
	if as, _ := g.s.Applies(ctx, g.round); len(as) != 0 {
		t.Fatalf("dry run recorded %+v", as)
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
		approvedOne(g)
		st.Write(t, g.repo, c.file, "# untracked\n")
		r := one(t, g.run(Options{DryRun: true, Instructions: c.names}))
		if held := r.State == "held"; held != c.held || (held && !strings.Contains(r.Detail, c.file)) {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

// A file whose approved content is the file as it is has nothing to write:
// agreeing with a file the agent recommends leaving alone only mutes its
// findings, and an edit back to the original is no change. Neither plans a
// branch.
func TestApplyPlansNothingForAFileLeftAsItIs(t *testing.T) {
	g := newRig(t)
	c, o, l := g.in(g.repo, "CLAUDE.md"), g.in(g.repo, "docs/other.md"), g.in(g.lib, "AGENTS.md")
	g.audit(c, o, l)
	same := g.rec(c, c.Content)
	same.Findings[0].Did, same.Summary = "kept", "Nothing to change."
	g.propose(same, g.rec(o, "# Other\n\n## Notes\n\n- one, rewritten\n"), g.rec(l, "# Lib\n\n- Keep the API stable.\n"))
	items, err := g.s.Files(ctx, g.round)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Key == c.Key && !it.Unchanged {
			t.Fatal("CLAUDE.md: its recommendation leaves it as it is")
		}
	}
	g.decide(c, rec.Decision{Action: "accept"})
	g.decide(o, rec.Decision{Action: "edit", Content: o.Content})
	g.decide(l, rec.Decision{Action: "accept"})
	res := g.run(Options{DryRun: true})
	r := one(t, res)
	if r.Path != g.lib || r.State != "planned" || len(r.Applied) != 1 || len(r.Skipped) != 0 {
		t.Fatalf("only lib's AGENTS.md changes: %+v", res.Repos)
	}
	if res.Unsent != 0 || len(res.Left) != 0 {
		t.Fatalf("%+v", res)
	}
	if sel := Select(items); len(sel.Files) != 0 {
		t.Fatalf("before the decisions: %+v", sel)
	}
}
