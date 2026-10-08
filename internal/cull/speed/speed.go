// Package speed scans a project's test files and script checks for what makes
// a suite slow or silently weak: fixed waits, Go setup that blocks parallel
// tests, probe waits whose failure is swallowed, Playwright options passed in
// the wrong place, and screenshot calls. Findings are reported to the agent;
// they never change a verdict. A "cull: keep" comment on the line, or alone
// on the line above, silences one.
package speed

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding kinds, in the order they rank in the digest after seconds.
const (
	KindSetenv      = "setenv_blocks_parallel"
	KindMisplaced   = "misplaced_timeout"
	KindSwallowed   = "swallowed_wait"
	KindScreenshot  = "screenshot"
	KindFixedWait   = "fixed_wait"
	keepMarker      = "cull: keep"
	digestTop       = 20
	digestShare     = 5 // the most one kind takes before every kind has its share
	minSetenvTests  = 2
	setenvShareDeno = 2 // a helper counts when at least 1/2 of a package's tests reach it
)

// kindOrder is the digest's order: the findings that need a decision first,
// then the waits (by seconds), then screenshots.
var kindOrder = []string{KindSetenv, KindMisplaced, KindSwallowed, KindFixedWait, KindScreenshot}

// Finding is one thing the scan found.
type Finding struct {
	Kind    string   `json:"kind"`
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Seconds *float64 `json:"seconds,omitempty"`    // fixed waits with a literal duration
	Test    string   `json:"test,omitempty"`       // enclosing test id, test files only
	Held    int      `json:"tests_held,omitempty"` // setup findings: tests that can't run in parallel
	Package string   `json:"package,omitempty"`    // setup findings: dir (root-relative) of the package whose tests are held back
	Detail  string   `json:"detail"`

	offset int // byte offset in the file, for the enclosing test
}

// Span is one test's byte range in its file.
type Span struct {
	ID         string
	Start, End int
}

// Scan reads each file (root-relative, '/'-separated) and returns its
// findings, ordered by file then line. Files of other languages are ignored.
// spans, when given, name the test each finding falls in. Go setup that
// blocks parallel tests is found by reading the whole project's Go sources;
// only findings in the listed files are returned.
func Scan(root string, files []string, spans map[string][]Span) ([]Finding, error) {
	return ScanTo(root, files, spans, io.Discard)
}

// ScanTo is Scan, saying on warn which unreadable files it skipped.
func ScanTo(root string, files []string, spans map[string][]Span, warn io.Writer) ([]Finding, error) {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	listed := map[string]bool{}
	var out []Finding
	hasGoTest := false
	for _, rel := range sorted {
		if listed[rel] {
			continue
		}
		listed[rel] = true
		abs := filepath.Join(root, filepath.FromSlash(rel))
		var fs []Finding
		switch ext := strings.ToLower(filepath.Ext(rel)); {
		case ext == ".go":
			if strings.HasSuffix(rel, "_test.go") {
				hasGoTest = true
			}
			b, ok := readFile(abs, rel, warn)
			if !ok {
				continue
			}
			fs = scanGoWaits(rel, b)
		case ext == ".py":
			b, ok := readFile(abs, rel, warn)
			if !ok {
				continue
			}
			fs = scanPython(rel, b)
		case isJS(ext):
			b, ok := readFile(abs, rel, warn)
			if !ok {
				continue
			}
			fs = scanJS(rel, b)
		}
		out = append(out, fs...)
	}
	if hasGoTest {
		out = append(out, scanGoSetenv(root, listed)...)
	}
	for i := range out {
		for _, sp := range spans[out[i].File] {
			if out[i].offset >= sp.Start && out[i].offset < sp.End {
				// the smallest enclosing span wins
				if out[i].Test == "" || sp.End-sp.Start < spanLen(spans[out[i].File], out[i].Test) {
					out[i].Test = sp.ID
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// readFile reads a file; one that is missing is skipped quietly and one that
// cannot be read is skipped with a warning.
func readFile(abs, rel string, warn io.Writer) ([]byte, bool) {
	b, err := os.ReadFile(abs)
	if err == nil {
		return b, true
	}
	if !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(warn, "cull: speed scan skipped %s: %v\n", rel, err)
	}
	return nil, false
}

func spanLen(sps []Span, id string) int {
	for _, s := range sps {
		if s.ID == id {
			return s.End - s.Start
		}
	}
	return 1 << 30
}

func isJS(ext string) bool {
	switch ext {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx":
		return true
	}
	return false
}

// keeper answers whether a "cull: keep" comment silences a line.
type keeper struct {
	marker    map[int]bool // lines holding a comment with the marker
	lineBlank func(n int) bool
}

func (k keeper) keeps(line int) bool {
	if k.marker[line] {
		return true
	}
	return k.marker[line-1] && k.lineBlank(line-1)
}

// addComment records a comment's text starting at line.
func (k keeper) addComment(line int, text string) {
	for i, l := range strings.Split(text, "\n") {
		if strings.Contains(l, keepMarker) {
			k.marker[line+i] = true
		}
	}
}

func lineOf(starts []int, off int) int {
	return sort.SearchInts(starts, off+1) // starts[i] is the offset of line i+1
}

func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func secs(v float64) *float64 { return &v }

// Counts and digest.

// DigestOut is the summary an agent reads: counts per kind, the literal wait
// total, and the costliest findings.
type DigestOut struct {
	Counts           map[string]int `json:"counts"`
	FixedWaitSeconds float64        `json:"fixed_wait_seconds"`
	Top              []Finding      `json:"top"`
}

// Digest summarises findings: counts, literal wait seconds, and the top 20: setenv,
// misplaced and swallowed findings first, then fixed waits by seconds
// descending, then screenshots.
func Digest(fs []Finding) DigestOut {
	d := DigestOut{Counts: map[string]int{}, Top: []Finding{}}
	for _, f := range fs {
		d.Counts[f.Kind]++
		if f.Kind == KindFixedWait && f.Seconds != nil {
			d.FixedWaitSeconds += *f.Seconds
		}
	}
	d.FixedWaitSeconds = math.Round(d.FixedWaitSeconds*1000) / 1000 // no float noise in JSON
	rank := map[string]int{}
	for i, k := range kindOrder {
		rank[k] = i
	}
	sec := func(f Finding) float64 {
		if f.Seconds == nil {
			return 0
		}
		return *f.Seconds
	}
	sorted := append([]Finding(nil), fs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		if a.Held != b.Held {
			return a.Held > b.Held // setup holding back the most tests first
		}
		if sec(a) != sec(b) {
			return sec(a) > sec(b) // the longest waits first
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	// Every kind present gets a share of the list (up to digestShare each),
	// in kind order; whatever is left goes to the rest in the same order.
	var top []Finding
	taken := map[int]bool{}
	per := map[string]int{}
	for i, f := range sorted {
		if len(top) < digestTop && per[f.Kind] < digestShare {
			top = append(top, f)
			taken[i] = true
			per[f.Kind]++
		}
	}
	for i, f := range sorted {
		if len(top) >= digestTop {
			break
		}
		if !taken[i] {
			top = append(top, f)
		}
	}
	sort.SliceStable(top, func(i, j int) bool { return rank[top[i].Kind] < rank[top[j].Kind] })
	d.Top = append(d.Top, top...)
	return d
}

// WriteText prints a digest: one line per kind with its count, then the top
// findings as file:line  kind  detail. Nothing is written when there are none.
func WriteText(w io.Writer, d DigestOut) {
	total := 0
	for _, n := range d.Counts {
		total += n
	}
	if total == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "speed\n")
	for _, k := range kindOrder {
		if n := d.Counts[k]; n > 0 {
			extra := ""
			if k == KindFixedWait && d.FixedWaitSeconds > 0 {
				extra = fmt.Sprintf("  (%.1fs of literal waits)", d.FixedWaitSeconds)
			}
			_, _ = fmt.Fprintf(w, "  %-24s %d%s\n", k, n, extra)
		}
	}
	for _, f := range d.Top {
		_, _ = fmt.Fprintf(w, "  %s:%d  %s  %s\n", f.File, f.Line, f.Kind, f.Detail)
	}
}
