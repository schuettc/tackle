// Package apply is `sift apply`: it writes a round's certain fixes and the
// rows the user approved (delete, rewrite, move, merge) as one branch per
// repo, cut from the fetched base in a worktree of its own, so the primary
// clone's checkout is never touched. A repo whose primary clone holds
// uncommitted or unpushed work, or an untracked instruction file, is held:
// the change might belong on top of that work. Every path is confined to
// the repo, and written through an os.Root on the worktree (confine.go).
// With gh and a GitHub remote the branch is pushed and a pull
// request opened; otherwise the committed branch is left for the user.
// Every git command goes through discover.Git, so a hook's variables never
// point it at another repository.
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

// Item is one row in the result.
type Item struct {
	Row     string `json:"row"`
	Check   string `json:"check"`
	Where   string `json:"where"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
	Why     string `json:"why,omitempty"` // why it was skipped or left
}

// Repo is what apply did in one repo.
type Repo struct {
	Path   string `json:"path"`
	Base   string `json:"base"`
	Branch string `json:"branch,omitempty"`
	PR     string `json:"pr,omitempty"`
	// State is pr, branch, held, nothing (no row applied), failed, or
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
	// Left are approved rows apply does not write: outside a repo, or a
	// verdict for the agent (issue, global, private) or with nothing to edit.
	Left []Item `json:"left"`
	// Unsent counts rows decided on the page but not sent: not approved yet,
	// so not applied.
	Unsent int `json:"unsent"`
}

// Branch is the branch apply writes a round to.
func Branch(round int64) string { return "sift/round-" + strconv.FormatInt(round, 10) }

// Plan is one approved row on its way into a repo: the row and the change
// the user approved.
type Plan struct {
	Row    row.Row
	Change row.Change
}

// Selection is what a round's rows approve: the plans to write per repo,
// the approved rows apply leaves to the agent or the user, and how many
// decisions are not sent yet.
type Selection struct {
	ByRepo map[string][]Plan
	Left   []Item
	Unsent int
}

// Select works out what the rows approve, in their order. It is the one
// place that says what counts as approved: a certain row's own fix, or a
// decision the user sent (reconcile uses it too).
func Select(rows []row.Row) Selection {
	sel := Selection{ByRepo: map[string][]Plan{}, Left: []Item{}}
	for _, r := range rows {
		// A decision counts once the user sent it. Until then the row is
		// left alone; an unsent undo of a certain fix needs no send to leave
		// the file as it is, so it is not counted.
		if d := r.Decision; d != nil && !d.Sent {
			if !r.FixOnly() || d.Action != "reject" {
				sel.Unsent++
			}
			continue
		}
		c, ok := r.Effective()
		if !ok {
			continue
		}
		it := item(r, c.Verdict)
		switch {
		case c.Verdict == "keep" || c.Verdict == "ask":
			continue
		case strings.HasPrefix(c.Verdict, "drop:") || strings.HasPrefix(c.Verdict, "close:"):
			it.Why = "nothing to edit in a repo"
			sel.Left = append(sel.Left, it)
			continue
		case c.Verdict == "issue" || c.Verdict == "global" || c.Verdict == "private":
			it.Why = "left for the agent: apply does not file issues or write global and private destinations"
			sel.Left = append(sel.Left, it)
			continue
		case r.Source.Repo == "":
			it.Why = "not in a git repo (" + r.Source.File + "): edit it by hand"
			sel.Left = append(sel.Left, it)
			continue
		}
		sel.ByRepo[r.Source.Repo] = append(sel.ByRepo[r.Source.Repo], Plan{Row: r, Change: c})
	}
	return sel
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
	byID := map[string]row.Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	sel := Select(rows)
	res := Result{Round: rd.ID, Repos: []Repo{}, Left: sel.Left, Unsent: sel.Unsent}
	repos := make([]string, 0, len(sel.ByRepo))
	for p := range sel.ByRepo {
		repos = append(repos, p)
	}
	sort.Strings(repos)
	for _, p := range repos {
		rp := applyRepo(ctx, o, rd.ID, p, sel.ByRepo[p], byID)
		if !o.DryRun {
			ids := make([]string, 0, len(rp.Applied))
			for _, it := range rp.Applied {
				ids = append(ids, it.Row)
			}
			if err := o.Store.RecordApply(ctx, store.Apply{Round: rd.ID, Repo: rp.Path, Base: rp.Base, Branch: rp.Branch,
				PR: rp.PR, State: rp.State, Detail: rp.Detail, Rows: ids}); err != nil {
				return res, err
			}
		}
		res.Repos = append(res.Repos, rp)
	}
	return res, nil
}

func item(r row.Row, verdict string) Item {
	where := r.Source.File
	if where == "" {
		where = r.Source.Entry
	}
	if r.Source.Start > 0 {
		where += ":" + strconv.Itoa(r.Source.Start)
	}
	it := Item{Row: r.ID, Check: r.Check, Where: where, Verdict: verdict}
	if r.Decision != nil {
		it.Note = r.Decision.Note
	}
	return it
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

// files is the repo's files at the base, read once each, with their edits.
type files struct {
	ctx      context.Context
	repo     string
	base     string
	m        map[string]*fileEdit
	existing map[string]bool
}

// get reads rel (checked by RepoPath) at the base. A path that is a
// symlink at the base, or runs through one, is refused: what it points at
// is not the repo's to write.
func (fs *files) get(p string) (*fileEdit, error) {
	rel, err := RepoPath(p)
	if err != nil {
		return nil, err
	}
	if f, ok := fs.m[rel]; ok {
		return f, nil
	}
	if err := fs.noSymlink(rel); err != nil {
		return nil, err
	}
	out, err := git(fs.ctx, fs.repo, "show", fs.base+":"+rel)
	f := &fileEdit{}
	if err == nil {
		f.lines = split(out + "\n")
		fs.existing[rel] = true
	} else if _, terr := git(fs.ctx, fs.repo, "cat-file", "-e", fs.base+":"+rel); terr == nil {
		return nil, err // there, but unreadable
	}
	fs.m[rel] = f
	return f, nil
}

// noSymlink refuses rel when it, or a directory on the way to it, is a
// symlink at the base.
func (fs *files) noSymlink(rel string) error {
	parts := strings.Split(rel, "/")
	args := []string{"ls-tree", "-z", fs.base, "--"}
	for i := range parts {
		args = append(args, strings.Join(parts[:i+1], "/"))
	}
	out, err := git(fs.ctx, fs.repo, args...)
	if err != nil {
		return err
	}
	for _, e := range strings.Split(out, "\x00") {
		if mode, rest, ok := strings.Cut(e, " "); ok && mode == "120000" {
			_, name, _ := strings.Cut(rest, "\t")
			return fmt.Errorf("%s is a symlink at %s: apply does not write through one", name, fs.base)
		}
	}
	return nil
}

// snapshot and restore make one row's edits all or nothing.
func (fs *files) snapshot() map[string]fileEdit {
	s := make(map[string]fileEdit, len(fs.m))
	for k, f := range fs.m {
		s[k] = *f
	}
	return s
}

func (fs *files) restore(s map[string]fileEdit) {
	for k := range fs.m {
		if old, ok := s[k]; ok {
			*fs.m[k] = old
		} else {
			delete(fs.m, k)
		}
	}
}

// edit applies one row's change to the repo's files.
func (fs *files) edit(p Plan, byID map[string]row.Row) error {
	r, c := p.Row, p.Change
	if r.Source.Path == "" {
		return fmt.Errorf("the row has no repo path")
	}
	src, err := fs.get(r.Source.Path)
	if err != nil {
		return err
	}
	whole := r.Source.Start == 0 && r.Passage == ""
	o := op{row: r.ID, passage: r.Passage, start: r.Source.Start}
	switch {
	case c.Verdict == "delete":
		if whole {
			return src.replaceWhole("", true)
		}
		return src.replace(o, "")
	case c.Verdict == "rewrite":
		if c.Text == "" {
			return fmt.Errorf("a rewrite with no text: edit the row's text, or delete it")
		}
		if whole {
			return src.replaceWhole(c.Text, false)
		}
		return src.replace(o, c.Text)
	case c.Verdict == "move":
		if whole {
			return fmt.Errorf("a whole-file move: move the file by hand")
		}
		dp, section := ParseDestination(c.Destination)
		dest, err := fs.destination(dp)
		if err != nil {
			return err
		}
		text := c.Text
		if text == "" {
			text = r.Passage
		}
		if err := src.replace(o, ""); err != nil {
			return err
		}
		d, err := fs.get(dest)
		if err != nil {
			return err
		}
		if d.whole {
			return fmt.Errorf("the destination %s is replaced by another row", dest)
		}
		d.insert(section, text)
		return nil
	case strings.HasPrefix(c.Verdict, "merge:"):
		target, ok := byID[strings.TrimPrefix(c.Verdict, "merge:")]
		if !ok {
			return fmt.Errorf("the merge target %s is not in the round", strings.TrimPrefix(c.Verdict, "merge:"))
		}
		if whole {
			return fmt.Errorf("a whole-file merge: merge the files by hand")
		}
		if err := src.replace(o, ""); err != nil {
			return err
		}
		if c.Text == "" {
			return nil
		}
		if target.Source.Repo != r.Source.Repo || target.Source.Path == "" {
			return fmt.Errorf("the merge target is in another repo: merge it by hand")
		}
		t, err := fs.get(target.Source.Path)
		if err != nil {
			return err
		}
		return t.replace(op{row: target.ID, passage: target.Passage, start: target.Source.Start}, c.Text)
	}
	return fmt.Errorf("apply does not write %q", c.Verdict)
}

// destination resolves a move's destination path to a repo-relative one.
func (fs *files) destination(p string) (string, error) {
	return Destination(fs.repo, p)
}

// Destination resolves a move's destination path (without its section) to
// a clean repo-relative one: relative to the repo root, or an absolute (or
// ~/) path inside the repo. Anything else is refused (RepoPath).
func Destination(repo, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("a move with no destination")
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		return RepoPath(p)
	}
	p = filepath.Clean(p)
	root, _ := filepath.EvalSymlinks(repo)
	abs, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		abs = filepath.Dir(p)
	}
	for _, r := range []string{repo, root} {
		for _, cand := range []string{p, filepath.Join(abs, filepath.Base(p))} {
			if rel, err := filepath.Rel(r, cand); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return RepoPath(rel)
			}
		}
	}
	return "", fmt.Errorf("the destination %s is outside the repo: move it by hand", p)
}

func applyRepo(ctx context.Context, o Options, round int64, repo string, plans []Plan, byID map[string]row.Row) (rp Repo) {
	base := plans[0].Row.Source.Ref
	if base == "" {
		base = "HEAD"
	}
	rp = Repo{Path: repo, Base: base, Applied: []Item{}, Skipped: []Item{}}
	// committed: the branch has its commit, so the repo is at least
	// "branch" and a later failure only goes into the detail.
	committed := false
	fail := func(err error) Repo {
		if committed {
			rp.Detail = joinDetail(rp.Detail, oneLine(err.Error()))
			return rp
		}
		rp.State, rp.Detail = "failed", err.Error()
		return rp
	}
	instr := o.Instructions
	if instr == nil {
		instr = InstructionNames(profile.Builtins())
	}
	why, err := hold(ctx, repo, base, instr)
	if err != nil {
		return fail(err)
	}
	if why != "" {
		rp.State, rp.Detail = "held", why
		return rp
	}
	if remoteBranch, ok := strings.CutPrefix(base, "origin/"); ok && !o.DryRun {
		if _, err := git(ctx, repo, "fetch", "-q", "origin", remoteBranch); err != nil {
			rp.Detail = "fetch failed, applied at the last fetched base: " + oneLine(err.Error())
		}
	}
	branch := Branch(round)
	if _, err := git(ctx, repo, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err == nil {
		rp.State, rp.Detail = "held", "branch "+branch+" already exists: this round was applied here before"
		return rp
	}

	fs := &files{ctx: ctx, repo: repo, base: base, m: map[string]*fileEdit{}, existing: map[string]bool{}}
	for _, p := range plans {
		it := item(p.Row, p.Change.Verdict)
		snap := fs.snapshot()
		if err := fs.edit(p, byID); err != nil {
			fs.restore(snap)
			it.Why = err.Error()
			rp.Skipped = append(rp.Skipped, it)
			continue
		}
		rp.Applied = append(rp.Applied, it)
	}
	if len(rp.Applied) == 0 {
		rp.State, rp.Detail = "nothing", "no row could be applied at "+base
		return rp
	}
	if o.DryRun {
		rp.State, rp.Branch = "planned", branch
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
	names := make([]string, 0, len(fs.m))
	for rel := range fs.m {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		f := fs.m[rel]
		if f.gone {
			if err := removeFile(root, rel); err != nil {
				return fail(err)
			}
			continue
		}
		if err := writeFile(root, rel, f.String()); err != nil {
			return fail(err)
		}
	}
	if _, err := git(ctx, wt, "add", "-A"); err != nil {
		return fail(err)
	}
	msg, err := tempFile(commitMessage(round, rp))
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
	body, err := tempFile(prBody(round, rp))
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

func commitMessage(round int64, rp Repo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sift: round %d: %d row(s)\n\n", round, len(rp.Applied))
	for _, it := range rp.Applied {
		fmt.Fprintf(&b, "- %s %s %s (%s)\n", it.Row, it.Verdict, it.Where, it.Check)
	}
	return b.String()
}

// prBody is the pull request's description, written to a file for
// --body-file.
func prBody(round int64, rp Repo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sift round %d: the rows approved on the review page and the certain fixes, applied at `%s`.\n\n", round, rp.Base)
	b.WriteString("| row | check | where | change | note |\n|---|---|---|---|---|\n")
	cell := func(s string) string { return strings.ReplaceAll(oneLine(s), "|", `\|`) }
	for _, it := range rp.Applied {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s |\n", it.Row, it.Check, cell(it.Where), cell(it.Verdict), cell(it.Note))
	}
	if len(rp.Skipped) > 0 {
		b.WriteString("\nNot applied:\n\n")
		for _, it := range rp.Skipped {
			fmt.Fprintf(&b, "- `%s` %s `%s`: %s\n", it.Row, it.Verdict, it.Where, it.Why)
		}
	}
	b.WriteString("\nReview each change against its row before merging (`sift reconcile` compares them).\n")
	return b.String()
}
