package timing

import (
	"bufio"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// TestTime is one test's measured time. Name is the Go test name, or the
// pytest node id.
type TestTime struct {
	Package string  `json:"package,omitempty"` // Go: import path
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Status  string  `json:"status,omitempty"` // pass | fail | skip
}

// GoPackage is one package in a `go test -json` run.
type GoPackage struct {
	Name          string     `json:"package"`
	Seconds       float64    `json:"seconds"`
	Share         float64    `json:"share"`  // of the summed package time
	Status        string     `json:"status"` // pass | fail | skip
	NoTests       bool       `json:"no_tests,omitempty"`
	BuildFailed   bool       `json:"build_failed,omitempty"`
	Top           int        `json:"tests"` // top-level tests that ran (not skipped)
	Subtests      int        `json:"subtests,omitempty"`
	MedianSeconds float64    `json:"median_test_seconds"`
	Tests         []TestTime `json:"-"`
}

// GoResult is what ParseGo read.
type GoResult struct {
	Packages []GoPackage // by elapsed, longest first
	Tail     string      // the last lines of what the run printed, for a failure report
}

const tailKeep = 20

type goEvent struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

// ParseGo reads `go test -json` events. Lines that are not events (build
// output, toolchain notes, a half-written line) are ignored.
func ParseGo(r io.Reader) GoResult {
	byName := map[string]*GoPackage{}
	var order []string
	get := func(name string) *GoPackage {
		p := byName[name]
		if p == nil {
			p = &GoPackage{Name: name}
			byName[name] = p
			order = append(order, name)
		}
		return p
	}
	var ring []string
	say := func(text string) {
		for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			ring = append(ring, l)
			if len(ring) > tailKeep {
				ring = ring[1:]
			}
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		var ev goEvent
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &ev) != nil {
			say(sc.Text())
			continue
		}
		if ev.Action == "output" || ev.Action == "build-output" {
			say(ev.Output)
			continue
		}
		if ev.Package == "" {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
		case "start":
			get(ev.Package)
			continue
		default:
			continue
		}
		p := get(ev.Package)
		if ev.Test == "" {
			p.Status, p.Seconds = ev.Action, ev.Elapsed
			continue
		}
		p.Tests = append(p.Tests, TestTime{Package: ev.Package, Name: ev.Test, Seconds: ev.Elapsed, Status: ev.Action})
	}
	res := GoResult{Tail: strings.Join(ring, "\n")}
	for _, name := range order {
		p := byName[name]
		if p.Status == "" {
			continue // started and never finished (killed)
		}
		var tops []float64
		for _, t := range p.Tests {
			if strings.Contains(t.Name, "/") {
				p.Subtests++
				continue
			}
			if t.Status != "skip" {
				tops = append(tops, t.Seconds)
			}
		}
		p.Top = len(tops)
		p.MedianSeconds = median(tops)
		if p.Status == "skip" && len(p.Tests) == 0 {
			p.NoTests = true
		}
		if p.Status == "fail" && len(p.Tests) == 0 {
			p.BuildFailed = true
		}
		res.Packages = append(res.Packages, *p)
	}
	sort.SliceStable(res.Packages, func(i, j int) bool { return res.Packages[i].Seconds > res.Packages[j].Seconds })
	return res
}

// leafTests are the tests that have no subtests: the ones whose time is their
// own.
func (p GoPackage) leafTests() []TestTime {
	parents := map[string]bool{}
	for _, t := range p.Tests {
		if i := strings.LastIndex(t.Name, "/"); i >= 0 {
			parents[t.Name[:i]] = true
		}
	}
	var out []TestTime
	for _, t := range p.Tests {
		if !parents[t.Name] && t.Status != "skip" {
			out = append(out, t)
		}
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
