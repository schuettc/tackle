// Package reconcile is `sift reconcile`: it holds each repo's apply branch
// against the files approved for it. Each file apply wrote must be on the
// branch exactly as approved (the recommendation the user accepted, or
// their edit); a file the branch changes that no approval covers is extra.
// A writer or reviewer may have changed the branch after apply; this is how
// the approved content is checked to have survived.
package reconcile

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/store"
)

// FileResult is one approved file against the branch.
type FileResult struct {
	Key  string `json:"key"`
	Path string `json:"path"`
	// State is ok, changed (not the approved content) or missing (not on
	// the branch).
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Report is one repo's comparison.
type Report struct {
	Repo   string       `json:"repo"`
	Branch string       `json:"branch"`
	Base   string       `json:"base"`
	Files  []FileResult `json:"files"`
	// Extra are the paths the branch changes that no approved file covers.
	Extra []string `json:"extra"`
}

// Problems counts changed and missing files and extra paths.
func (r Report) Problems() int {
	n := len(r.Extra)
	for _, f := range r.Files {
		if f.State != "ok" {
			n++
		}
	}
	return n
}

// Run reconciles every branch apply wrote for the round (0: the latest).
func Run(ctx context.Context, st *store.Store, round int64) (int64, []Report, error) {
	if round == 0 {
		id, _, err := st.Latest(ctx)
		if err != nil {
			return 0, nil, err
		}
		round = id
	}
	if _, _, err := st.Round(ctx, round); err != nil {
		return round, nil, err
	}
	items, err := st.Files(ctx, round)
	if err != nil {
		return round, nil, err
	}
	approved := map[string]apply.Approved{}
	for _, a := range apply.Select(items).Files {
		approved[a.Key] = a
	}
	applies, err := st.Applies(ctx, round)
	if err != nil {
		return round, nil, err
	}
	out := []Report{}
	for _, a := range applies {
		if !a.Succeeded() {
			continue
		}
		rep := Report{Repo: a.Repo, Branch: a.Branch, Base: a.Base, Files: []FileResult{}, Extra: []string{}}
		var paths []string
		for _, key := range a.Rows {
			f, ok := approved[key]
			if !ok {
				rep.Files = append(rep.Files, FileResult{Key: key, State: "changed", Detail: "apply wrote it, but it is no longer approved and sent"})
				continue
			}
			paths = append(paths, f.Source.Path)
			rep.Files = append(rep.Files, compare(ctx, a.Repo, a.Branch, f))
		}
		changed, err := discover.Git(ctx, a.Repo, "-c", "core.quotepath=off", "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z",
			a.Base+"..."+a.Branch).Output()
		if err != nil {
			return round, out, fmt.Errorf("%s: git diff %s...%s: %w", a.Repo, a.Base, a.Branch, err)
		}
		for _, p := range strings.Split(string(changed), "\x00") {
			if p != "" && !slices.Contains(paths, p) {
				rep.Extra = append(rep.Extra, p)
			}
		}
		out = append(out, rep)
	}
	return round, out, nil
}

// compare is one file at the branch against its approved content.
func compare(ctx context.Context, repo, branch string, f apply.Approved) FileResult {
	r := FileResult{Key: f.Key, Path: f.Source.Path, State: "ok"}
	got, err := discover.Git(ctx, repo, "show", branch+":"+f.Source.Path).Output()
	if err != nil {
		r.State, r.Detail = "missing", "not on the branch"
		return r
	}
	if string(got) == f.Content {
		return r
	}
	r.State, r.Detail = "changed", firstDiff(f.Content, string(got))
	return r
}

// firstDiff says where two contents first differ, by line.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b || i >= len(w) || i >= len(g) {
			return fmt.Sprintf("line %d is %q on the branch, approved %q", i+1, b, a)
		}
	}
	return "the line endings differ"
}
