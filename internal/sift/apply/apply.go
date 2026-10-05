// Package apply is `sift apply`: it writes each file the user approved and
// sent, its recommendation or the user's edit, whole, as one branch per
// repo, cut from the fetched base in a worktree of its own, so the primary
// clone's checkout is never touched. A file whose base moved on since the
// audit is held, and so are the files linked to it, wherever they are. A
// repo whose primary clone holds uncommitted or unpushed work, or an
// untracked instruction file, is held: the change might belong on top of
// that work. Every path is confined to the repo, and written through an
// os.Root on the worktree (confine.go). With gh and a GitHub remote the
// branch is pushed and a pull request opened; otherwise the committed
// branch is left for the user. A file outside any repo has no branch: its
// approved content is saved for the user. A backlog round writes nothing:
// its approved rows are left for the agent. Every git command goes through
// discover.Git, so a hook's variables never point it at another
// repository.
package apply

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/host"
	"github.com/schuettc/tackle/internal/sift/profile"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

// Runner runs gh in dir; tests replace it.
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// Gh runs the gh on PATH, with the repository-selecting variables cleared.
func Gh(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	cmd.Env = discover.GitEnv(os.Environ())
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("gh %s: %w: %s", strings.Join(args[:min(2, len(args))], " "), err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}

// Options says what to apply and how.
type Options struct {
	Store *store.Store
	Round int64 // 0: the latest round
	// Gh opens pull requests; nil: no gh, branches are left local.
	Gh Runner
	// WorktreeDir holds the worktrees while they are written ("": sift's
	// state directory).
	WorktreeDir string
	// DryRun works out what would change and touches nothing.
	DryRun bool
	// Instructions are the instruction file names the enabled profiles
	// load (nil: every shipped profile's, see InstructionNames). An
	// untracked one holds its repo.
	Instructions []string
}

// Item is one file (or, in a backlog round, one row) in the result.
type Item struct {
	Key    string `json:"key"`   // the file's key, or the row's id
	Where  string `json:"where"` // the file's path, or where the row is
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
	Why    string `json:"why,omitempty"` // why it was skipped or left
	// Approved is where the approved content of a file outside any repo
	// was saved, for the user to put in place.
	Approved string `json:"approved,omitempty"`
}

// Repo is what apply did in one repo.
type Repo struct {
	Path   string `json:"path"`
	Base   string `json:"base"`
	Branch string `json:"branch,omitempty"`
	PR     string `json:"pr,omitempty"`
	// State is pr, branch, held, nothing (no file written), failed, or
	// planned (a dry run).
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
	Applied []Item `json:"applied"`
	Skipped []Item `json:"skipped"`
}

// Result is a whole apply.
type Result struct {
	Round int64  `json:"round"`
	Repos []Repo `json:"repos"`
	// Left are approved files apply does not write (outside a repo) and,
	// in a backlog round, approved rows for the agent (issues to file,
	// items to close).
	Left []Item `json:"left"`
	// Unsent counts decisions made on the page but not sent: not approved
	// yet, so not applied.
	Unsent int `json:"unsent"`
}

// Branch is the branch apply writes a round to.
func Branch(round int64) string { return "sift/round-" + strconv.FormatInt(round, 10) }

// Approved is a file the user approved and sent, with the content to write.
type Approved struct {
	store.FileItem
	Content string
}

// Selection is what a round's decisions approve.
type Selection struct {
	Files  []Approved
	Unsent int
}

// Select works out which files the decisions approve, in the round's
// order. It is the one place that says what counts as approved: an accept
// or edit the user sent (reconcile uses it too).
func Select(items []store.FileItem) Selection {
	var sel Selection
	for _, it := range items {
		d := it.Decision
		switch {
		case d == nil || it.Rec == nil:
		case !d.Sent:
			sel.Unsent++
		default:
			if body, ok := rec.Approved(*it.Rec, *d); ok {
				sel.Files = append(sel.Files, Approved{FileItem: it, Content: body})
			}
		}
	}
	return sel
}

// SelectRows is a backlog round's approved rows, each left for the agent
// (filing an issue, closing an item), and how many decisions are unsent.
func SelectRows(rows []row.Row) ([]Item, int) {
	left, unsent := []Item{}, 0
	for _, r := range rows {
		if d := r.Decision; d != nil && !d.Sent {
			unsent++
			continue
		}
		c, ok := r.Effective()
		if !ok || c.Verdict == "keep" || c.Verdict == "ask" {
			continue
		}
		where := r.Source.File
		if where == "" {
			where = r.Source.Entry
		}
		left = append(left, Item{Key: r.ID, Where: where, Action: c.Verdict, Note: r.Decision.Note,
			Why: "left for the agent: file the issue, or close the item, as approved"})
	}
	return left, unsent
}

// Run applies the round.
func Run(ctx context.Context, o Options) (Result, error) {
	var rd store.Round
	var rows []row.Row
	var err error
	if o.Round == 0 {
		rd, rows, err = o.Store.LatestRound(ctx)
	} else {
		rd, rows, err = o.Store.Round(ctx, o.Round)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, errors.New("no such round: run sift check")
	}
	if err != nil {
		return Result{}, err
	}
	res := Result{Round: rd.ID, Repos: []Repo{}, Left: []Item{}}
	if store.PerItem(rd.Kind) {
		res.Left, res.Unsent = SelectRows(rows)
		return res, nil
	}
	items, err := o.Store.Files(ctx, rd.ID)
	if err != nil {
		return Result{}, err
	}
	sel := Select(items)
	res.Unsent = sel.Unsent

	// Plan every repo before writing any, so a file held in one repo holds
	// the files linked to it in the others.
	byRepo := map[string][]*plan{}
	var outside []*plan
	all := map[string]*plan{}
	for _, a := range sel.Files {
		p := &plan{a: a, it: fileItem(a)}
		all[a.Key] = p
		if a.Source.Repo == "" {
			outside = append(outside, p)
			continue
		}
		byRepo[a.Source.Repo] = append(byRepo[a.Source.Repo], p)
	}
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	preps := make([]*prep, 0, len(repos))
	for _, r := range repos {
		preps = append(preps, prepare(ctx, o, rd.ID, r, byRepo[r]))
	}
	for _, pr := range preps {
		if pr.rp.State != "" {
			for _, p := range pr.plans {
				p.why = pr.rp.State + ": " + pr.rp.Detail
			}
		}
	}
	byKey := map[string]store.FileItem{}
	for _, it := range items {
		byKey[it.Key] = it
	}
	holdLinked(all, byKey)
	for _, p := range outside {
		if p.why != "" {
			p.it.Why = p.why
		} else {
			p.it.Why = "not in a git repo, so there is no branch to put it on: put the approved content in place yourself"
			if !o.DryRun {
				if p.it.Approved, err = saveApproved(rd.ID, p.a); err != nil {
					p.it.Why += " (saving it failed: " + oneLine(err.Error()) + ")"
				}
			}
		}
		res.Left = append(res.Left, p.it)
	}
	for _, pr := range preps {
		rp := write(ctx, o, rd.ID, pr)
		if !o.DryRun {
			keys := make([]string, 0, len(rp.Applied))
			for _, it := range rp.Applied {
				keys = append(keys, it.Key)
			}
			if err := o.Store.RecordApply(ctx, store.Apply{Round: rd.ID, Repo: rp.Path, Base: rp.Base, Branch: rp.Branch,
				PR: rp.PR, State: rp.State, Detail: rp.Detail, Rows: keys}); err != nil {
				return res, err
			}
		}
		res.Repos = append(res.Repos, rp)
	}
	return res, nil
}

// plan is one approved file on its way: why it can't be written ("" when
// it can).
type plan struct {
	a   Approved
	it  Item
	why string
}

func fileItem(a Approved) Item {
	it := Item{Key: a.Key, Where: a.Source.File, Action: a.Decision.Action, Note: a.Decision.Note}
	return it
}

// holdLinked holds every approved file linked to one that won't be
// written as approved: held itself, or not approved and sent. It repeats
// until nothing changes, so a chain of links holds whole.
func holdLinked(all map[string]*plan, items map[string]store.FileItem) {
	for changed := true; changed; {
		changed = false
		for _, p := range all {
			if p.why != "" {
				continue
			}
			for _, k := range p.a.Group {
				if k == p.a.Key {
					continue
				}
				o, ok := all[k]
				switch {
				case !ok:
					p.why = "linked to " + items[k].Source.File + ", whose decision is not an accept or edit you sent: linked files go in together"
				case o.why != "":
					p.why = "linked to " + o.a.Source.File + ", which is not applied (" + firstReason(o.why) + "): linked files go in together"
				default:
					continue
				}
				changed = true
				break
			}
		}
	}
}

// firstReason is a hold reason without the chain of links before it.
func firstReason(why string) string {
	for {
		_, rest, ok := strings.Cut(why, "which is not applied (")
		if !ok {
			return why
		}
		r, _, ok := strings.Cut(rest, "): linked files")
		if !ok {
			return why
		}
		why = r
	}
}

// saveApproved writes the approved content of a file outside any repo to
// sift's state directory and returns where.
func saveApproved(round int64, a Approved) (string, error) {
	dir := filepath.Join(tools.StateDir("sift"), "approved", "round-"+strconv.FormatInt(round, 10))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, a.Key+"-"+filepath.Base(a.Source.File))
	return p, tools.WriteFileAtomic(p, []byte(a.Content), 0o600)
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := discover.Git(ctx, dir, append([]string{"-c", "core.quotepath=off"}, args...)...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// InstructionNames is every instruction file name the profiles load: their
// repo files, their global files' names, and SKILL.md.
func InstructionNames(ps []profile.Profile) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n = path.Base(filepath.ToSlash(n)); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, p := range ps {
		for _, n := range p.RepoFiles {
			add(n)
		}
		for _, n := range p.Global {
			add(n)
		}
	}
	add(profile.SkillFile)
	return out
}

// hold reports why the primary clone's work must land first: tracked files
// changed, an untracked instruction file (one of names: it may be the real
// destination, not committed yet), or commits on its branch that the base
// (or its upstream) lacks. Other untracked files don't hold a repo: stray
// ones would hold it for good.
func hold(ctx context.Context, repo, base string, names []string) (string, error) {
	st, err := git(ctx, repo, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	if st != "" {
		var files []string
		for _, l := range strings.Split(st, "\n") {
			if len(l) > 3 {
				files = append(files, l[3:])
			}
		}
		return fmt.Sprintf("uncommitted changes in the primary clone (%s): commit or stash them, push, then apply again", strings.Join(first(files, 3), ", ")), nil
	}
	others, err := git(ctx, repo, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	var untracked []string
	for _, f := range strings.Split(others, "\x00") {
		for _, n := range names {
			if f != "" && strings.EqualFold(path.Base(f), n) {
				untracked = append(untracked, f)
				break
			}
		}
	}
	if len(untracked) > 0 {
		return fmt.Sprintf("untracked instruction file(s) in the primary clone (%s): commit them (or remove them), push, then apply again", strings.Join(first(untracked, 3), ", ")), nil
	}
	upstream := "@{u}"
	if _, err := git(ctx, repo, "rev-parse", "--verify", "-q", upstream); err != nil {
		upstream = base
	}
	if upstream == "HEAD" {
		return "", nil
	}
	n, err := git(ctx, repo, "rev-list", "--count", upstream+"..HEAD")
	if err != nil {
		return "", err
	}
	if n != "0" {
		branch, _ := git(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")
		return fmt.Sprintf("unpushed work in the primary clone: %s commit(s) on %s not on %s: push or land them, then apply again", n, branch, upstream), nil
	}
	return "", nil
}

func first(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(xs[:n:n], fmt.Sprintf("%d more", len(xs)-n))
}

// prep is one repo planned: its result so far (State "" while it can still
// be written) and its files.
type prep struct {
	rp    Repo
	plans []*plan
}

// prepare checks a repo and its files before anything is written: the
// primary clone holds no work, the base is fetched, the round's branch is
// new, and each file's path is the repo's own, no symlink at the base, and
// its content there still the audited base.
func prepare(ctx context.Context, o Options, round int64, repo string, plans []*plan) *prep {
	base := plans[0].a.Source.Ref
	if base == "" {
		base = "HEAD"
	}
	pr := &prep{rp: Repo{Path: repo, Base: base, Applied: []Item{}, Skipped: []Item{}}, plans: plans}
	instr := o.Instructions
	if instr == nil {
		instr = InstructionNames(profile.Builtins())
	}
	why, err := hold(ctx, repo, base, instr)
	switch {
	case err != nil:
		pr.rp.State, pr.rp.Detail = "failed", err.Error()
		return pr
	case why != "":
		pr.rp.State, pr.rp.Detail = "held", why
		return pr
	}
	if remoteBranch, ok := strings.CutPrefix(base, "origin/"); ok && !o.DryRun {
		if _, err := git(ctx, repo, "fetch", "-q", "origin", remoteBranch); err != nil {
			pr.rp.Detail = "fetch failed, applied at the last fetched base: " + oneLine(err.Error())
		}
	}
	if _, err := git(ctx, repo, "rev-parse", "--verify", "-q", "refs/heads/"+Branch(round)); err == nil {
		pr.rp.State, pr.rp.Detail = "held", "branch "+Branch(round)+" already exists: this round was applied here before"
		return pr
	}
	for _, p := range plans {
		p.why = checkFile(ctx, repo, base, p.a)
	}
	return pr
}

// checkFile says why a file can't be written at base ("" when it can).
func checkFile(ctx context.Context, repo, base string, a Approved) string {
	rel, err := RepoPath(a.Source.Path)
	if err != nil {
		return err.Error()
	}
	if err := noSymlink(ctx, repo, base, rel); err != nil {
		return err.Error()
	}
	now, err := discover.Git(ctx, repo, "show", base+":"+rel).Output()
	if err != nil {
		return "not at " + base + " any more: " + oneLine(err.Error())
	}
	if rec.Hash(string(now)) != a.Base {
		return "changed at " + base + " since the audit: run sift check for a new round"
	}
	return ""
}

// noSymlink refuses rel when it, or a directory on the way to it, is a
// symlink at the base: what it points at is not the repo's to write.
func noSymlink(ctx context.Context, repo, base, rel string) error {
	parts := strings.Split(rel, "/")
	args := []string{"ls-tree", "-z", base, "--"}
	for i := range parts {
		args = append(args, strings.Join(parts[:i+1], "/"))
	}
	out, err := git(ctx, repo, args...)
	if err != nil {
		return err
	}
	for _, e := range strings.Split(out, "\x00") {
		if mode, rest, ok := strings.Cut(e, " "); ok && mode == "120000" {
			_, name, _ := strings.Cut(rest, "\t")
			return fmt.Errorf("%s is a symlink at %s: apply does not write through one", name, base)
		}
	}
	return nil
}

// write writes a prepared repo's files that can be written on the round's
// branch, in a worktree, and commits them; then pushes and opens a pull
// request when it can.
func write(ctx context.Context, o Options, round int64, pr *prep) (rp Repo) {
	rp = pr.rp
	for _, p := range pr.plans {
		it := p.it
		if p.why != "" {
			it.Why = p.why
			rp.Skipped = append(rp.Skipped, it)
			continue
		}
		rp.Applied = append(rp.Applied, it)
	}
	if rp.State != "" {
		rp.Skipped = append(rp.Skipped, rp.Applied...)
		rp.Applied = []Item{}
		return rp
	}
	if len(rp.Applied) == 0 {
		rp.State, rp.Detail = "nothing", joinDetail(rp.Detail, "no file could be written at "+rp.Base)
		return rp
	}
	branch := Branch(round)
	if o.DryRun {
		rp.State, rp.Branch = "planned", branch
		return rp
	}
	repo, base := rp.Path, rp.Base
	// committed: the branch has its commit, so the repo is at least
	// "branch" and a later failure only goes into the detail.
	committed := false
	fail := func(err error) Repo {
		if committed {
			rp.Detail = joinDetail(rp.Detail, oneLine(err.Error()))
			return rp
		}
		rp.State, rp.Detail = "failed", err.Error()
		rp.Skipped, rp.Applied = append(rp.Skipped, rp.Applied...), []Item{}
		return rp
	}
	dir := o.WorktreeDir
	if dir == "" {
		dir = filepath.Join(tools.StateDir("sift"), "worktrees")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	wt, err := os.MkdirTemp(dir, filepath.Base(repo)+"-r"+strconv.FormatInt(round, 10)+"-")
	if err != nil {
		return fail(err)
	}
	_ = os.Remove(wt) // git worktree add makes it
	if _, err := git(ctx, repo, "worktree", "add", "-q", "-b", branch, wt, base); err != nil {
		return fail(err)
	}
	defer func() {
		bg := context.WithoutCancel(ctx)
		if _, err := git(bg, repo, "worktree", "remove", "--force", wt); err != nil {
			rp.Detail = strings.TrimSpace(rp.Detail + "; worktree left at " + wt)
		}
		if !committed {
			// Nothing was committed: the branch is the base, and would only
			// hold the next apply.
			_, _ = git(bg, repo, "branch", "-D", branch)
		}
	}()
	// Every write goes through one os.Root on the worktree (confine.go).
	root, err := os.OpenRoot(wt)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = root.Close() }()
	for _, p := range pr.plans {
		if p.why != "" {
			continue
		}
		if err := writeFile(root, p.a.Source.Path, p.a.Content); err != nil {
			return fail(err)
		}
	}
	if _, err := git(ctx, wt, "add", "-A"); err != nil {
		return fail(err)
	}
	msg, err := tempFile(commitMessage(round, pr))
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.Remove(msg) }()
	if _, err := git(ctx, wt, "commit", "-q", "-F", msg); err != nil {
		return fail(err)
	}
	committed = true
	rp.Branch, rp.State = branch, "branch"

	remote, _ := git(ctx, repo, "config", "--get", "remote.origin.url")
	slug, github := host.Slug(remote)
	switch {
	case o.Gh == nil:
		rp.Detail = joinDetail(rp.Detail, "no gh: the branch is committed and left local")
		return rp
	case !github:
		rp.Detail = joinDetail(rp.Detail, "origin is not on GitHub: the branch is committed and left local")
		return rp
	}
	if _, err := git(ctx, repo, "push", "-q", "-u", "origin", branch); err != nil {
		rp.Detail = joinDetail(rp.Detail, "push failed, the branch is left local: "+oneLine(err.Error()))
		return rp
	}
	body, err := tempFile(prBody(round, pr, rp))
	if err != nil {
		rp.Detail = joinDetail(rp.Detail, "the branch is pushed but the pull request's body could not be written: "+oneLine(err.Error()))
		return rp
	}
	defer func() { _ = os.Remove(body) }()
	args := []string{"pr", "create", "--repo", slug, "--head", branch, "--title", fmt.Sprintf("sift: round %d", round), "--body-file", body}
	if b, ok := strings.CutPrefix(base, "origin/"); ok {
		args = append(args, "--base", b)
	}
	out, err := o.Gh(ctx, wt, args...)
	if err != nil {
		rp.Detail = joinDetail(rp.Detail, "the branch is pushed but gh pr create failed: "+oneLine(err.Error()))
		return rp
	}
	rp.State, rp.PR = "pr", lastLine(string(out))
	return rp
}

func joinDetail(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// tempFile writes content to a new temporary file (tests replace it).
var tempFile = writeTemp

func writeTemp(content string) (string, error) {
	f, err := os.CreateTemp("", "sift-apply-*.md")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

func commitMessage(round int64, pr *prep) string {
	var b strings.Builder
	n := 0
	for _, p := range pr.plans {
		if p.why == "" {
			n++
		}
	}
	fmt.Fprintf(&b, "sift: round %d: %d file(s)\n\n", round, n)
	for _, p := range pr.plans {
		if p.why == "" {
			fmt.Fprintf(&b, "- %s (%s): %s\n", p.a.Source.Path, p.a.Decision.Action, oneLine(p.a.Rec.Summary))
		}
	}
	return b.String()
}

// prBody is the pull request's description, written to a file for
// --body-file: each file with its summary, what the rewrite did about each
// finding, and the user's note.
func prBody(round int64, pr *prep, rp Repo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sift round %d: the files approved on the review page, written whole at `%s`.\n", round, rp.Base)
	for _, p := range pr.plans {
		if p.why != "" {
			continue
		}
		how := "the recommendation, accepted"
		if p.a.Decision.Action == "edit" {
			how = "the recommendation, edited by the reviewer"
		}
		fmt.Fprintf(&b, "\n### `%s`\n\n%s (%s)\n\n", p.a.Source.Path, oneLine(p.a.Rec.Summary), how)
		for _, f := range p.a.Rec.Findings {
			fmt.Fprintf(&b, "- `%s` %s: %s\n", f.Row, f.Did, oneLine(f.How))
		}
		if p.a.Decision.Note != "" {
			fmt.Fprintf(&b, "\nNote: %s\n", oneLine(p.a.Decision.Note))
		}
	}
	if len(rp.Skipped) > 0 {
		b.WriteString("\nNot written:\n\n")
		for _, it := range rp.Skipped {
			fmt.Fprintf(&b, "- `%s`: %s\n", it.Where, it.Why)
		}
	}
	b.WriteString("\nEach file on this branch is the approved content exactly (`sift reconcile` checks it).\n")
	return b.String()
}
