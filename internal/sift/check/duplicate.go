package check

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
)

const (
	// minWords is the shortest passage compared: shorter ones repeat by
	// chance ("Keep pull requests small.").
	minWords = 12
	// shingle is the word-shingle length.
	shingle = 3
	// minOverlap is the share of the smaller passage's shingles the other
	// must hold for the two to be copies.
	minOverlap = 0.7
	// commonShingle: a shingle in more passages than this is boilerplate and
	// is not used to find candidates.
	commonShingle = 50
)

// passage is a paragraph or list item outside code blocks.
type passage struct {
	file       *discover.File
	start, end int
	text       string
	shingles   map[uint64]bool
}

var listItemRE = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
var headingRE = regexp.MustCompile(`^\s*#{1,6}\s`)
var wordRE = regexp.MustCompile(`[a-z0-9]+(?:'[a-z]+)?`)

// passages splits a file into paragraphs and list items; headings, blank
// lines and code blocks end one.
func passages(f *discover.File) []*passage {
	var out []*passage
	var cur *passage
	flush := func() {
		if cur != nil {
			out = append(out, cur)
			cur = nil
		}
	}
	for _, l := range split(f.Content) {
		t := strings.TrimSpace(l.Text)
		if l.Code || t == "" || headingRE.MatchString(l.Text) || strings.HasPrefix(t, "|") {
			flush()
			continue
		}
		if listItemRE.MatchString(l.Text) {
			flush()
		}
		if cur == nil {
			cur = &passage{file: f, start: l.N}
		} else {
			cur.text += "\n"
		}
		cur.text += l.Text
		cur.end = l.N
	}
	flush()
	return out
}

func shingles(text string) map[uint64]bool {
	words := wordRE.FindAllString(strings.ToLower(text), -1)
	if len(words) < minWords {
		return nil
	}
	out := map[uint64]bool{}
	for i := 0; i+shingle <= len(words); i++ {
		h := fnv.New64a()
		_, _ = h.Write([]byte(strings.Join(words[i:i+shingle], " ")))
		out[h.Sum64()] = true
	}
	return out
}

// duplicate flags passages that say the same thing in nearly the same words,
// within a file or across files: one row per copy, naming the others.
func duplicate(_ context.Context, in *Input) []row.Row {
	var ps []*passage
	for _, f := range in.Files {
		for _, p := range passages(f) {
			if p.shingles = shingles(p.text); p.shingles != nil {
				ps = append(ps, p)
			}
		}
	}
	index := map[uint64][]int{}
	for i, p := range ps {
		for s := range p.shingles {
			index[s] = append(index[s], i)
		}
	}
	parent := make([]int, len(ps))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	for i, p := range ps {
		shared := map[int]int{}
		for s := range p.shingles {
			if len(index[s]) > commonShingle {
				continue
			}
			for _, j := range index[s] {
				if j > i {
					shared[j]++
				}
			}
		}
		for j, n := range shared {
			if float64(n) >= minOverlap*float64(min(len(p.shingles), len(ps[j].shingles))) {
				parent[find(j)] = find(i)
			}
		}
	}
	groups := map[int][]int{}
	for i := range ps {
		groups[find(i)] = append(groups[find(i)], i)
	}
	var rows []row.Row
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.Ints(g)
		for _, i := range g {
			p := ps[i]
			var ev []row.Fact
			for _, j := range g {
				if j != i {
					ev = append(ev, fact("other copy", "%s:%d", ps[j].file.Path, ps[j].start))
				}
			}
			summary := "the same passage is in another file"
			if sameFile(p, ps, g) {
				summary = "the same passage appears twice in this file"
			}
			if len(g) > 2 {
				summary = fmt.Sprintf("the same passage appears %d times", len(g))
			}
			rows = append(rows, newRow(p.file, "duplicate", p.start, p.end, p.text, "", summary, false, ev...))
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Source, rows[j].Source
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Start < b.Start
	})
	return rows
}

func sameFile(p *passage, ps []*passage, g []int) bool {
	for _, j := range g {
		if ps[j] != p && ps[j].file != p.file {
			return false
		}
	}
	return true
}
