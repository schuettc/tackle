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

// expect is what an approved row should have done to the diff.
type expect struct {
	r         row.Row
	verdict   string
	removed   map[string][]string // path → passage lines (non-blank)
	added     map[string][]string // path → approved lines (non-blank)
	wholeGone string              // a file the row deletes whole
}

func nonBlank(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// expectations works out each approved edit row's change. Rows apply does
// not write (keep, issue, …) are left out.
func expectations(rows []row.Row) []expect {
	byID := map[string]row.Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	var out []expect
	for _, r := range rows {
		c, ok := r.Effective()
		if !ok || r.Source.Path == "" {
			continue
		}
		e := expect{r: r, verdict: c.Verdict, removed: map[string][]string{}, added: map[string][]string{}}
		src := r.Source.Path
		whole := r.Source.Start == 0 && r.Passage == ""
		switch {
		case c.Verdict == "delete":
			if whole {
				e.wholeGone = src
			} else {
				e.removed[src] = nonBlank(r.Passage)
			}
		case c.Verdict == "rewrite":
			if !whole {
				e.removed[src] = nonBlank(r.Passage)
			}
			e.added[src] = nonBlank(c.Text)
		case c.Verdict == "move":
			e.removed[src] = nonBlank(r.Passage)
			dest, _, _ := strings.Cut(c.Destination, "#")
			dest = strings.TrimSpace(dest)
			text := c.Text
			if text == "" {
				text = r.Passage
			}
			e.added[dest] = append(e.added[dest], nonBlank(text)...)
		case strings.HasPrefix(c.Verdict, "merge:"):
			e.removed[src] = nonBlank(r.Passage)
			if t, ok := byID[strings.TrimPrefix(c.Verdict, "merge:")]; ok && c.Text != "" && t.Source.Path != "" {
				e.removed[t.Source.Path] = append(e.removed[t.Source.Path], nonBlank(t.Passage)...)
				e.added[t.Source.Path] = append(e.added[t.Source.Path], nonBlank(c.Text)...)
			}
		default:
			continue
		}
		out = append(out, e)
	}
	return out
}

// Compare holds one repo's approved rows against its branch's diff.
func Compare(rows []row.Row, diff []File) Report {
	files := map[string]File{}
	for _, f := range diff {
		files[f.Path] = f
	}
	lines := func(path string, added bool) []string {
		var out []string
		for _, h := range files[path].Hunks {
			src := h.Removed
			if added {
				src = h.Added
			}
			for _, l := range src {
				if strings.TrimSpace(l) != "" {
					out = append(out, l)
				}
			}
		}
		return out
	}
	rep := Report{Rows: []RowResult{}, Extra: []Extra{}}
	claimed := map[string]map[string]bool{} // path → "-line" / "+line"
	claim := func(path, l string) {
		if claimed[path] == nil {
			claimed[path] = map[string]bool{}
		}
		claimed[path][l] = true
	}
	hunkOwned := map[hunkKey]int{}
	for _, e := range expectations(rows) {
		res := RowResult{Row: e.r.ID, Verdict: e.verdict, Where: where(e.r), State: "ok"}
		wantN, gotN := 0, 0
		var problems []string
		if e.wholeGone != "" {
			wantN++
			if files[e.wholeGone].Deleted {
				gotN++
				for _, h := range files[e.wholeGone].Hunks {
					for _, l := range h.Removed {
						claim(e.wholeGone, "-"+l)
					}
				}
			} else {
				problems = append(problems, e.wholeGone+" is not deleted")
			}
		}
		for path, want := range e.removed {
			have := count(lines(path, false))
			n := 0
			for _, l := range want {
				claim(path, "-"+l)
				if have[l] > 0 {
					have[l]--
					n++
				}
			}
			wantN += len(want)
			gotN += n
			if n < len(want) {
				problems = append(problems, fmt.Sprintf("%d of %d passage line(s) removed in %s", n, len(want), path))
			}
		}
		if e.verdict == "rewrite" {
			// A rewrite owns what replaced its passage, verbatim or not: as
			// many added lines as its approved text has.
			src := e.r.Source.Path
			for i, h := range files[src].Hunks {
				for _, l := range h.Removed {
					if contains(e.removed[src], l) {
						hunkOwned[hunkKey{src, i}] += len(e.added[src])
						break
					}
				}
			}
		}
		for path, want := range e.added {
			for _, l := range want {
				claim(path, "+"+l)
			}
			wantN += len(want)
			if contiguous(lines(path, true), want) {
				gotN += len(want)
				continue
			}
			have := count(lines(path, true))
			for _, l := range want {
				if have[l] > 0 {
					have[l]--
					gotN++
				}
			}
			problems = append(problems, "the approved text is not verbatim in "+path)
		}
		switch {
		case gotN == 0 && wantN > 0:
			res.State, res.Detail = "missing", "nothing of this row's change is on the branch"
		case len(problems) > 0:
			res.State, res.Detail = "narrowed", strings.Join(problems, "; ")
		}
		rep.Rows = append(rep.Rows, res)
	}
	for _, f := range diff {
		for i, h := range f.Hunks {
			owned := hunkOwned[hunkKey{f.Path, i}]
			var loose []string
			for _, l := range h.Removed {
				if strings.TrimSpace(l) != "" && !claimed[f.Path]["-"+l] {
					loose = append(loose, "-"+l)
				}
			}
			for _, l := range h.Added {
				switch {
				case strings.TrimSpace(l) == "" || claimed[f.Path]["+"+l]:
				case owned > 0:
					owned--
				default:
					loose = append(loose, "+"+l)
				}
			}
			if len(loose) > 0 {
				rep.Extra = append(rep.Extra, Extra{File: f.Path, Header: h.Header, Lines: loose})
			}
		}
	}
	sort.SliceStable(rep.Extra, func(i, j int) bool { return rep.Extra[i].File < rep.Extra[j].File })
	return rep
}

type hunkKey struct {
	path string
	i    int
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func count(xs []string) map[string]int {
	m := map[string]int{}
	for _, x := range xs {
		m[x]++
	}
	return m
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
		if a.State != "pr" && a.State != "branch" {
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
		rep := Compare(mine, Parse(d))
		rep.Repo, rep.Branch, rep.Base = a.Repo, a.Branch, a.Base
		out = append(out, rep)
	}
	return round, out, nil
}
