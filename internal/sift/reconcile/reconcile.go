// Package reconcile is `sift reconcile`: it holds each repo's apply branch
// against the rows approved for it and reports, by content, a row whose
// change is missing, a row whose approved text is not in the diff verbatim
// or whose passage is only partly removed (narrowed), and hunks no approved
// row accounts for (extra). A writer or reviewer may have changed the branch
// after apply; this is how the approved meaning is checked to have survived.
package reconcile

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Hunk is one hunk of a unified diff (-U0): the lines it removes and adds.
type Hunk struct {
	Header  string
	Removed []string
	Added   []string
}

// File is one file of a diff.
type File struct {
	Path    string
	Deleted bool
	Hunks   []Hunk
}

// Parse reads a unified diff made with a/ and b/ prefixes.
func Parse(diff string) []File {
	var out []File
	var cur *File
	var h *Hunk
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "diff --git "):
			out = append(out, File{})
			cur, h = &out[len(out)-1], nil
			if _, b, ok := strings.Cut(l, " b/"); ok {
				cur.Path = b
			}
		case cur == nil:
		case strings.HasPrefix(l, "deleted file mode"):
			cur.Deleted = true
		case strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ ") && h == nil:
			if p, ok := strings.CutPrefix(l, "+++ b/"); ok {
				cur.Path = p
			}
		case strings.HasPrefix(l, "@@"):
			cur.Hunks = append(cur.Hunks, Hunk{Header: hunkHeader(l)})
			h = &cur.Hunks[len(cur.Hunks)-1]
		case h == nil:
		case strings.HasPrefix(l, "-"):
			h.Removed = append(h.Removed, l[1:])
		case strings.HasPrefix(l, "+"):
			h.Added = append(h.Added, l[1:])
		}
	}
	return out
}

// hunkHeader keeps "@@ -a,b +c,d @@" without the function context.
func hunkHeader(l string) string {
	if i := strings.Index(l[2:], "@@"); i >= 0 {
		return l[:i+4]
	}
	return l
}

// RowResult is one approved row against the diff.
type RowResult struct {
	Row     string `json:"row"`
	Verdict string `json:"verdict"`
	Where   string `json:"where"`
	// State is ok, missing or narrowed.
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Extra is a hunk (or its part) no approved row accounts for.
type Extra struct {
	File   string   `json:"file"`
	Header string   `json:"header"`
	Lines  []string `json:"lines"` // the unaccounted lines, "-" or "+" first
}

// Report is one repo's comparison.
type Report struct {
	Repo   string      `json:"repo"`
	Branch string      `json:"branch"`
	Base   string      `json:"base"`
	Rows   []RowResult `json:"rows"`
	Extra  []Extra     `json:"extra"`
}

// Problems counts missing and narrowed rows and extra hunks.
func (r Report) Problems() int {
	n := len(r.Extra)
	for _, x := range r.Rows {
		if x.State != "ok" {
			n++
		}
	}
	return n
}

// block is approved text a row adds to a file: it must be verbatim and
// contiguous in the file at the branch.
type block struct {
	path  string
	lines []string // exactly as approved
	// anchored: the text replaces the row's passage in path (a rewrite, or
	// a merge's text over its target), so it owns the replacement span in
	// the hunks that removed the passage.
	anchored bool
	// heading is a section heading apply adds with the text when the file
	// lacks the section; the row owns it if it is there.
	heading string
}

// expect is what an approved row should have done to the branch.
type expect struct {
	r         row.Row
	verdict   string
	removed   map[string][]string // path → passage lines (non-blank)
	blocks    []block
	wholeGone string // a file the row deletes whole
	problems  []string
}

func nonBlank(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// textLines splits approved text into its lines, as apply writes them.
func textLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// expectations works out each approved row's change, with apply's own
// selection (apply.Select: certain fixes and sent decisions only) and path
// rules. Rows apply does not write (keep, issue, …) are left out.
func expectations(rows []row.Row) []expect {
	byID := map[string]row.Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	sel := apply.Select(rows)
	repos := make([]string, 0, len(sel.ByRepo))
	for p := range sel.ByRepo {
		repos = append(repos, p)
	}
	sort.Strings(repos)
	var out []expect
	for _, repo := range repos {
		for _, p := range sel.ByRepo[repo] {
			if e, ok := expectation(repo, p, byID); ok {
				out = append(out, e)
			}
		}
	}
	return out
}

func expectation(repo string, p apply.Plan, byID map[string]row.Row) (expect, bool) {
	r, c := p.Row, p.Change
	e := expect{r: r, verdict: c.Verdict, removed: map[string][]string{}}
	src, err := apply.RepoPath(r.Source.Path)
	if err != nil {
		e.problems = append(e.problems, err.Error())
		return e, true
	}
	whole := r.Source.Start == 0 && r.Passage == ""
	passage := nonBlank(textLines(r.Passage))
	switch {
	case c.Verdict == "delete":
		if whole {
			e.wholeGone = src
		} else {
			e.removed[src] = passage
		}
	case c.Verdict == "rewrite":
		if !whole {
			e.removed[src] = passage
		}
		e.blocks = append(e.blocks, block{path: src, lines: textLines(c.Text), anchored: !whole})
	case c.Verdict == "move":
		e.removed[src] = passage
		dp, section := apply.ParseDestination(c.Destination)
		dest, err := apply.Destination(repo, dp)
		if err != nil {
			e.problems = append(e.problems, err.Error())
			return e, true
		}
		text := c.Text
		if text == "" {
			text = r.Passage
		}
		b := block{path: dest, lines: textLines(text)}
		if section != "" {
			b.heading = apply.SectionHeading(section)
		}
		e.blocks = append(e.blocks, b)
	case strings.HasPrefix(c.Verdict, "merge:"):
		e.removed[src] = passage
		t, ok := byID[strings.TrimPrefix(c.Verdict, "merge:")]
		if ok && c.Text != "" && t.Source.Path != "" {
			tp, err := apply.RepoPath(t.Source.Path)
			if err != nil {
				e.problems = append(e.problems, err.Error())
				return e, true
			}
			e.removed[tp] = append(e.removed[tp], nonBlank(textLines(t.Passage))...)
			e.blocks = append(e.blocks, block{path: tp, lines: textLines(c.Text), anchored: true})
		}
	default:
		return e, false
	}
	return e, true
}

// line is one removed or added line of the diff; each is consumed by at
// most one row.
type line struct {
	text string
	used bool
}

type hunkLines struct {
	header         string
	removed, added []*line
}

// diffLines indexes a diff's non-blank lines by file and hunk. Blank lines
// are layout: apply adds and removes them around a passage, and no row owns
// them.
func diffLines(diff []File) map[string][]*hunkLines {
	out := map[string][]*hunkLines{}
	for _, f := range diff {
		for _, h := range f.Hunks {
			hl := &hunkLines{header: h.Header}
			for _, l := range nonBlank(h.Removed) {
				hl.removed = append(hl.removed, &line{text: l})
			}
			for _, l := range nonBlank(h.Added) {
				hl.added = append(hl.added, &line{text: l})
			}
			out[f.Path] = append(out[f.Path], hl)
		}
	}
	return out
}

// Compare holds one repo's approved rows (as apply.Select chooses them)
// against its branch: the diff's lines, each occurrence consumed by at most
// one row, and read, the files at the branch, where approved text must be
// verbatim and contiguous.
func Compare(rows []row.Row, diff []File, read func(path string) (string, bool)) Report {
	deleted := map[string]bool{}
	for _, f := range diff {
		if f.Deleted {
			deleted[f.Path] = true
		}
	}
	hunks := diffLines(diff)
	exps := expectations(rows)
	got := make([]int, len(exps))  // diff lines each row accounts for
	want := make([]int, len(exps)) // lines each row should account for
	anchors := make([]map[*hunkLines]bool, len(exps))

	// 1. What each row removes: the first unused occurrence of each line.
	for i := range exps {
		e := &exps[i]
		anchors[i] = map[*hunkLines]bool{}
		if e.wholeGone != "" {
			want[i]++
			if deleted[e.wholeGone] {
				got[i]++
				for _, h := range hunks[e.wholeGone] {
					for _, l := range h.removed {
						l.used = true
					}
				}
			} else {
				e.problems = append(e.problems, e.wholeGone+" is not deleted")
			}
		}
		paths := make([]string, 0, len(e.removed))
		for p := range e.removed {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, path := range paths {
			n := 0
			for _, w := range e.removed[path] {
				if h := take(hunks[path], w); h != nil {
					anchors[i][h] = true
					n++
				}
			}
			want[i] += len(e.removed[path])
			got[i] += n
			if n < len(e.removed[path]) {
				e.problems = append(e.problems, fmt.Sprintf("%d of %d passage line(s) removed in %s", n, len(e.removed[path]), path))
			}
		}
	}
	// 2. Approved text found verbatim as a run of one hunk's added lines,
	// before any row takes lines one by one.
	found := make([][]bool, len(exps))
	for i := range exps {
		e := &exps[i]
		found[i] = make([]bool, len(e.blocks))
		for j, b := range e.blocks {
			nb := nonBlank(b.lines)
			want[i] += len(nb)
			if takeRun(hunks[b.path], anchors[i], b.anchored, nb) {
				found[i][j] = true
				got[i] += len(nb)
			}
		}
	}
	// 3. The rest: lines taken one by one, then, for text that replaces a
	// passage, the replacement span: as many of the passage's hunk's added
	// lines as the approved text has, so a reworded line is narrowed once,
	// not also extra. A heading apply adds is the row's when it is there.
	for i := range exps {
		e := &exps[i]
		for j, b := range e.blocks {
			if b.heading != "" {
				take(addedOf(hunks[b.path]), b.heading)
			}
			if found[i][j] {
				continue
			}
			nb := nonBlank(b.lines)
			n := 0
			for _, w := range nb {
				if take(addedOf(hunks[b.path]), w) != nil {
					n++
				}
			}
			got[i] += n
			if b.anchored {
				for _, h := range hunks[b.path] {
					if !anchors[i][h] {
						continue
					}
					for _, l := range h.added {
						if n < len(nb) && !l.used {
							l.used = true
							n++
						}
					}
				}
			}
		}
		// The approved text, verbatim and contiguous in the file at the
		// branch.
		for _, b := range e.blocks {
			content, ok := read(b.path)
			if !ok || !contiguous(textLines(content), b.lines) {
				e.problems = append(e.problems, "the approved text is not verbatim and contiguous in "+b.path+" at the branch")
			}
		}
	}
	rep := Report{Rows: []RowResult{}, Extra: []Extra{}}
	for i, e := range exps {
		res := RowResult{Row: e.r.ID, Verdict: e.verdict, Where: where(e.r), State: "ok"}
		switch {
		case got[i] == 0 && (want[i] > 0 || len(e.problems) > 0):
			res.State, res.Detail = "missing", "nothing of this row's change is on the branch"
			if len(e.problems) > 0 && want[i] == 0 {
				res.Detail = strings.Join(e.problems, "; ")
			}
		case len(e.problems) > 0:
			res.State, res.Detail = "narrowed", strings.Join(e.problems, "; ")
		}
		rep.Rows = append(rep.Rows, res)
	}
	for _, f := range diff {
		for _, h := range hunks[f.Path] {
			var loose []string
			for _, l := range h.removed {
				if !l.used {
					loose = append(loose, "-"+l.text)
				}
			}
			for _, l := range h.added {
				if !l.used {
					loose = append(loose, "+"+l.text)
				}
			}
			if len(loose) > 0 {
				rep.Extra = append(rep.Extra, Extra{File: f.Path, Header: h.header, Lines: loose})
			}
		}
		delete(hunks, f.Path) // a path listed twice is reported once
	}
	sort.SliceStable(rep.Extra, func(i, j int) bool { return rep.Extra[i].File < rep.Extra[j].File })
	return rep
}

// take marks the first unused removed line equal to w and returns its hunk
// (nil: none).
func take(hs []*hunkLines, w string) *hunkLines {
	for _, h := range hs {
		for _, l := range h.removed {
			if !l.used && l.text == w {
				l.used = true
				return h
			}
		}
	}
	return nil
}

// addedOf presents hunks' added lines as removed ones, so take can consume
// them.
func addedOf(hs []*hunkLines) []*hunkLines {
	out := make([]*hunkLines, len(hs))
	for i, h := range hs {
		out[i] = &hunkLines{header: h.header, removed: h.added}
	}
	return out
}

// takeRun marks the first run of unused added lines equal to want inside
// one hunk, trying the row's anchor hunks first when anchored.
func takeRun(hs []*hunkLines, anchors map[*hunkLines]bool, anchored bool, want []string) bool {
	if len(want) == 0 {
		return true
	}
	try := func(h *hunkLines) bool {
		for i := 0; i+len(want) <= len(h.added); i++ {
			ok := true
			for j, w := range want {
				if l := h.added[i+j]; l.used || l.text != w {
					ok = false
					break
				}
			}
			if ok {
				for j := range want {
					h.added[i+j].used = true
				}
				return true
			}
		}
		return false
	}
	if anchored {
		for _, h := range hs {
			if anchors[h] && try(h) {
				return true
			}
		}
	}
	for _, h := range hs {
		if try(h) {
			return true
		}
	}
	return false
}

// contiguous reports whether want appears as a run in have.
func contiguous(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i+len(want) <= len(have); i++ {
		ok := true
		for j := range want {
			if have[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func where(r row.Row) string {
	if r.Source.Start > 0 {
		return fmt.Sprintf("%s:%d", r.Source.Path, r.Source.Start)
	}
	return r.Source.Path
}

// Diff is the branch's diff against its merge base with base, as Parse reads
// it: no external diff driver, no renames, a/ and b/ prefixes whatever the
// user's config says.
func Diff(ctx context.Context, repo, base, branch string) (string, error) {
	cmd := discover.Git(ctx, repo, "-c", "core.quotepath=off", "diff", "--no-ext-diff", "--no-color", "--no-renames", "-U0",
		"--src-prefix=a/", "--dst-prefix=b/", base+"..."+branch)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff %s...%s: %w: %s", base, branch, err, strings.TrimSpace(errb.String()))
	}
	return string(out), nil
}

// Run reconciles every branch apply wrote for the round (0: the latest).
func Run(ctx context.Context, st *store.Store, round int64) (int64, []Report, error) {
	var rows []row.Row
	var err error
	if round == 0 {
		var rd store.Round
		rd, rows, err = st.LatestRound(ctx)
		round = rd.ID
	} else {
		_, rows, err = st.Round(ctx, round)
	}
	if err != nil {
		return round, nil, err
	}
	applies, err := st.Applies(ctx, round)
	if err != nil {
		return round, nil, err
	}
	var out []Report
	for _, a := range applies {
		if !a.Succeeded() {
			continue
		}
		var mine []row.Row
		for _, r := range rows {
			if r.Source.Repo == a.Repo {
				mine = append(mine, r)
			}
		}
		d, err := Diff(ctx, a.Repo, a.Base, a.Branch)
		if err != nil {
			return round, out, err
		}
		branch := a.Branch
		read := func(path string) (string, bool) {
			out, err := discover.Git(ctx, a.Repo, "show", branch+":"+path).Output()
			return string(out), err == nil
		}
		rep := Compare(mine, Parse(d), read)
		rep.Repo, rep.Branch, rep.Base = a.Repo, a.Branch, a.Base
		out = append(out, rep)
	}
	return round, out, nil
}
