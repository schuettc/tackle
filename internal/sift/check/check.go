// Package check is sift's audit: deterministic checks over the files
// discover found, each returning rows. A row is certain only where the spec
// says the check cannot be wrong; everything else is for the agent and the
// user to judge.
package check

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/host"
	"github.com/schuettc/tackle/internal/sift/row"
)

// Input is what every check sees.
type Input struct {
	Files  []*discover.File
	Chains []discover.Chain
	Repos  []*discover.Repo
	Config config.Config
	// Host answers PR and issue state; nil when there is none (no gh).
	Host host.Host
	Now  time.Time
}

// Check is one audit check.
type Check struct {
	Name string
	Run  func(ctx context.Context, in *Input) []row.Row
}

// All is every check, in the order the spec lists them.
var All = []Check{
	{"size", size},
	{"load-limit", loadLimit},
	{"duplicate", duplicate},
	{"dead-path", deadPath},
	{"stale-status", staleStatus},
	{"retired-store", retiredStore},
	{"misplaced", misplaced},
	{"secret", secret},
}

// Run runs every check and returns the rows ordered by file, line and check.
// A passage repeated in a file gets one row per copy, each with its own id
// (row.Nth, numbered in line order). A secret's value is redacted in every
// row about its file, after the ids are made from the original text.
func Run(ctx context.Context, in *Input) []row.Row {
	var rows []row.Row
	order := map[string]int{}
	for i, c := range All {
		order[c.Name] = i
		rows = append(rows, c.Run(ctx, in)...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Source.File != b.Source.File {
			return a.Source.File < b.Source.File
		}
		if a.Source.Start != b.Source.Start {
			return a.Source.Start < b.Source.Start
		}
		return order[a.Check] < order[b.Check]
	})
	seen := map[string]int{}
	for i := range rows {
		id := rows[i].ID
		rows[i].ID = row.Nth(id, seen[id])
		seen[id]++
	}
	redactRows(in.Files, rows)
	return rows
}

// newRow builds a row about lines start..end of f (0, 0: the whole file).
// idText is what the id hashes: the passage, or the file for whole-file rows.
func newRow(f *discover.File, check string, start, end int, passage, idText, summary string, certain bool, evidence ...row.Fact) row.Row {
	src := row.Source{File: f.Path, Start: start, End: end}
	if f.Repo != nil {
		src.Repo, src.Ref, src.Path = f.Repo.Root, f.Repo.Ref, f.Rel
	}
	if idText == "" {
		idText = passage
	}
	return row.Row{
		ID: row.ID(f.Path, check, idText), Check: check, Summary: summary,
		Source: src, Passage: passage, Evidence: evidence, Certain: certain,
	}
}

func fact(name, format string, a ...any) row.Fact {
	return row.Fact{Name: name, Value: fmt.Sprintf(format, a...)}
}

// line is one line of a file, 1-based.
type line struct {
	N    int
	Text string
	Code bool // in a fenced code block (fences included) or the frontmatter
}

var fenceRE = regexp.MustCompile("^\\s*(```|~~~)")

// split returns a file's lines, marking those in fenced code blocks and in
// YAML frontmatter (a skill's metadata) as code.
func split(content string) []line {
	var out []line
	in := ""
	front := strings.HasPrefix(content, "---\n")
	for i, t := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if front {
			out = append(out, line{N: i + 1, Text: t, Code: true})
			front = i == 0 || strings.TrimSpace(t) != "---"
			continue
		}
		code := in != ""
		if m := fenceRE.FindStringSubmatch(t); m != nil {
			code = true
			switch in {
			case "":
				in = m[1]
			case m[1]:
				in = ""
			}
		}
		out = append(out, line{N: i + 1, Text: t, Code: code})
	}
	return out
}

// prose returns the lines outside code blocks that carry text: not blank, not
// a table rule.
func prose(content string) []line {
	var out []line
	for _, l := range split(content) {
		t := strings.TrimSpace(l.Text)
		if l.Code || t == "" || strings.Trim(t, "|-: ") == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

func kb(n int) string { return fmt.Sprintf("%.1f KB", float64(n)/1000) }
