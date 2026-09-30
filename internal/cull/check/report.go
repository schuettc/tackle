package check

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/extract"
	tools "github.com/schuettc/tools-common"
)

// TestResult is one judged test. Its JSON keeps the case's identifying
// fields (including hash and span, for apply) but drops body/context/callees:
// those are large and check/apply never needs them back.
type TestResult struct {
	cases.TestCase
	Verdict string   `json:"verdict,omitempty"`
	Rule    string   `json:"rule,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	Model   string   `json:"model,omitempty"`
	Err     string   `json:"err,omitempty"`
}

// MarshalJSON embeds the case's fields except Body, Context and Callees.
func (r TestResult) MarshalJSON() ([]byte, error) {
	type out struct {
		ID        string     `json:"id"`
		Hash      string     `json:"hash"`
		Lang      string     `json:"lang"`
		Framework string     `json:"framework"`
		File      string     `json:"file"`
		Name      string     `json:"name"`
		Parent    string     `json:"parent,omitempty"`
		Repo      string     `json:"repo,omitempty"`
		Span      cases.Span `json:"span"`
		Truncated bool       `json:"truncated"`
		Verdict   string     `json:"verdict,omitempty"`
		Rule      string     `json:"rule,omitempty"`
		Reasons   []string   `json:"reasons,omitempty"`
		Model     string     `json:"model,omitempty"`
		Err       string     `json:"err,omitempty"`
	}
	return json.Marshal(out{
		ID: r.ID, Hash: r.Hash, Lang: r.Lang, Framework: r.Framework, File: r.File,
		Name: r.Name, Parent: r.Parent, Repo: r.Repo, Span: r.Span, Truncated: r.Truncated,
		Verdict: r.Verdict, Rule: r.Rule, Reasons: r.Reasons, Model: r.Model, Err: r.Err,
	})
}

// GroupResult is one judged near-duplicate group.
type GroupResult struct {
	ID             string   `json:"id"`
	File           string   `json:"file"`
	Members        []string `json:"members"`
	Verdict        string   `json:"verdict,omitempty"`
	Rule           string   `json:"rule,omitempty"`
	Reasons        []string `json:"reasons,omitempty"`
	ExactDuplicate bool     `json:"exact_duplicate,omitempty"`
	Model          string   `json:"model,omitempty"`
	Err            string   `json:"err,omitempty"`
}

// Report is everything one `cull check` run produced.
type Report struct {
	Root    string            `json:"root"`
	Mode    string            `json:"mode"` // "suite" or "diff"
	Base    string            `json:"base,omitempty"`
	Tests   []TestResult      `json:"tests"`
	Groups  []GroupResult     `json:"groups"`
	Skipped []extract.Skipped `json:"skipped,omitempty"`
	Summary map[string]int    `json:"summary"`
}

// HasActions reports whether the report has any test to cut or group to
// consolidate: the process should exit 1.
func (r Report) HasActions() bool {
	return r.Summary["cut"] > 0 || r.Summary["consolidate"] > 0
}

// summarize counts verdicts and errors. Every key is present, even at 0.
func summarize(tests []TestResult, groups []GroupResult, skipped []extract.Skipped) map[string]int {
	s := map[string]int{
		"cut": 0, "review": 0, "keep": 0,
		"consolidate": 0, "group_review": 0, "keep_separate": 0,
		"skipped": len(skipped), "errors": 0,
	}
	for _, t := range tests {
		if t.Err != "" {
			s["errors"]++
			continue
		}
		switch t.Verdict {
		case "cut":
			s["cut"]++
		case "keep":
			s["keep"]++
		case "review":
			s["review"]++
		}
	}
	for _, g := range groups {
		if g.Err != "" {
			s["errors"]++
			continue
		}
		switch g.Verdict {
		case "consolidate":
			s["consolidate"]++
		case "keep_separate":
			s["keep_separate"]++
		case "review":
			s["group_review"]++
		}
	}
	return s
}

// WriteTable renders a human table: one section per file, tests then
// groups, then a "skipped" section listing every file that could not be
// extracted and why.
func WriteTable(w io.Writer, r Report) {
	byFile := map[string][]TestResult{}
	groupsByFile := map[string][]GroupResult{}
	seen := map[string]bool{}
	var files []string
	for _, t := range r.Tests {
		byFile[t.File] = append(byFile[t.File], t)
		if !seen[t.File] {
			seen[t.File] = true
			files = append(files, t.File)
		}
	}
	for _, g := range r.Groups {
		groupsByFile[g.File] = append(groupsByFile[g.File], g)
		if !seen[g.File] {
			seen[g.File] = true
			files = append(files, g.File)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		_, _ = fmt.Fprintf(w, "%s\n", f)
		ts := byFile[f]
		sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
		for _, t := range ts {
			if t.Err != "" {
				_, _ = fmt.Fprintf(w, "  error  %s  %s\n", t.ID, t.Err)
				continue
			}
			_, _ = fmt.Fprintf(w, "  %s  %s  %s\n", t.Verdict, t.ID, strings.Join(t.Reasons, ", "))
		}
		gs := groupsByFile[f]
		sort.Slice(gs, func(i, j int) bool { return gs[i].ID < gs[j].ID })
		for _, g := range gs {
			if g.Err != "" {
				_, _ = fmt.Fprintf(w, "  error  %s  %s\n", g.ID, g.Err)
				continue
			}
			_, _ = fmt.Fprintf(w, "  %s  %d tests: %s\n", g.Verdict, len(g.Members), strings.Join(g.Members, ", "))
		}
	}
	if len(r.Skipped) > 0 {
		sk := append([]extract.Skipped(nil), r.Skipped...)
		sort.Slice(sk, func(i, j int) bool { return sk[i].File < sk[j].File })
		_, _ = fmt.Fprintf(w, "skipped\n")
		for _, s := range sk {
			_, _ = fmt.Fprintf(w, "  %s  %s\n", s.File, s.Reason)
		}
	}
}

// writeLastJSON writes <root>/.cull/last.json (dir 0700, file 0600).
func writeLastJSON(root string, r Report) error {
	dir := filepath.Join(root, ".cull")
	if err := tools.EnsureDir(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteFileAtomic(filepath.Join(dir, "last.json"), data, 0o600)
}
