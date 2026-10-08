package timing

import (
	"bufio"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PyFile is one test file's time in a pytest run.
type PyFile struct {
	File         string  `json:"file"`
	Seconds      float64 `json:"seconds"` // setup + call
	Share        float64 `json:"share"`   // of the summed file time
	SetupSeconds float64 `json:"setup_seconds"`
	SetupShare   float64 `json:"setup_share"` // of this file's seconds
	Tests        int     `json:"tests"`
}

// PyResult is what ParsePytest read.
type PyResult struct {
	Files                   []PyFile // by seconds, longest first
	Tests                   []TestTime
	Passed, Failed, Skipped int
	Errors                  int
	Seconds                 float64 // from the summary line
}

var (
	pyDuration = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)s\s+(setup|call|teardown)\s+(\S.*?)\s*$`)
	pySummary  = regexp.MustCompile(`^=+ (.*?) in (\d+(?:\.\d+)?)s(?: \([^)]*\))? =+$`)
	pyCount    = regexp.MustCompile(`(\d+) (passed|failed|skipped|errors?|xfailed|xpassed)`)
)

// ParsePytest reads pytest output run with --durations=0 (-vv): the
// "slowest durations" lines, which xdist also prints once at the end, and
// the closing summary line.
func ParsePytest(r io.Reader) PyResult {
	var res PyResult
	type acc struct{ setup, call float64 }
	perTest := map[string]*acc{}
	var order []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := pyDuration.FindStringSubmatch(line); m != nil {
			sec, _ := strconv.ParseFloat(m[1], 64)
			a := perTest[m[3]]
			if a == nil {
				a = &acc{}
				perTest[m[3]] = a
				order = append(order, m[3])
			}
			switch m[2] {
			case "setup":
				a.setup += sec
			case "call":
				a.call += sec
			}
			continue
		}
		if m := pySummary.FindStringSubmatch(line); m != nil {
			res.Seconds, _ = strconv.ParseFloat(m[2], 64)
			for _, c := range pyCount.FindAllStringSubmatch(m[1], -1) {
				n, _ := strconv.Atoi(c[1])
				switch c[2] {
				case "passed":
					res.Passed = n
				case "failed":
					res.Failed = n
				case "skipped":
					res.Skipped = n
				case "error", "errors":
					res.Errors = n
				}
			}
		}
	}
	files := map[string]*PyFile{}
	for _, id := range order {
		a := perTest[id]
		res.Tests = append(res.Tests, TestTime{Name: id, Seconds: a.setup + a.call})
		file := id
		if i := strings.Index(id, "::"); i >= 0 {
			file = id[:i]
		}
		f := files[file]
		if f == nil {
			f = &PyFile{File: file}
			files[file] = f
		}
		f.Seconds += a.setup + a.call
		f.SetupSeconds += a.setup
		f.Tests++
	}
	var total float64
	for _, f := range files {
		total += f.Seconds
	}
	for _, f := range files {
		if total > 0 {
			f.Share = f.Seconds / total
		}
		if f.Seconds > 0 {
			f.SetupShare = f.SetupSeconds / f.Seconds
		}
		res.Files = append(res.Files, *f)
	}
	sort.Slice(res.Files, func(i, j int) bool {
		if res.Files[i].Seconds != res.Files[j].Seconds {
			return res.Files[i].Seconds > res.Files[j].Seconds
		}
		return res.Files[i].File < res.Files[j].File
	})
	sort.SliceStable(res.Tests, func(i, j int) bool { return res.Tests[i].Seconds > res.Tests[j].Seconds })
	return res
}
