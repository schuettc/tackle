package speed

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// maskPython blanks comments and string contents, keeping offsets.
func maskPython(src []byte) ([]byte, []comment) {
	out := append([]byte(nil), src...)
	var comments []comment
	n := len(src)
	line := 1
	blank := func(from, to int) {
		for i := from; i < to && i < n; i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	for i := 0; i < n; {
		c := src[i]
		switch c {
		case '\n':
			line++
			i++
		case '#':
			j := i
			for j < n && src[j] != '\n' {
				j++
			}
			comments = append(comments, comment{line, string(src[i:j])})
			blank(i, j)
			i = j
		case '\'', '"':
			if i+2 < n && src[i+1] == c && src[i+2] == c {
				j := i + 3
				for j+2 < n && (src[j] != c || src[j+1] != c || src[j+2] != c) {
					if src[j] == '\\' {
						j++
					}
					j++
				}
				if j+2 >= n {
					j = n
				}
				line += strings.Count(string(src[i:min(j+3, n)]), "\n")
				blank(i+3, j)
				i = j + 3
				continue
			}
			j := i + 1
			for j < n && src[j] != c && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j > n {
				j = n
			}
			blank(i+1, j)
			i = j + 1
		default:
			i++
		}
	}
	return out, comments
}

var (
	rePySleep    = regexp.MustCompile(`\b(?:time|asyncio)\s*\.\s*sleep\s*\(`)
	rePyBare     = regexp.MustCompile(`(?:^|[^\w.])sleep\s*\(`)
	rePyImport   = regexp.MustCompile(`(?m)^\s*from\s+(?:time|asyncio)\s+import\s+[^\n]*\bsleep\b`)
	rePyNumber   = regexp.MustCompile(`^(?:\d[\d_]*(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?$`)
	rePyScreenSh = regexp.MustCompile(`\.\s*screenshot\s*\(`)
)

func scanPython(rel string, src []byte) []Finding {
	masked, comments := maskPython(src)
	m := string(masked)
	starts := lineStarts(src)
	k := keeper{marker: map[int]bool{}}
	for _, c := range comments {
		k.addComment(c.line, c.text)
	}
	mlines := strings.Split(m, "\n")
	k.lineBlank = func(n int) bool { return n >= 1 && n <= len(mlines) && strings.TrimSpace(mlines[n-1]) == "" }

	var out []Finding
	add := func(kind string, off int, s *float64, detail string) {
		line := lineOf(starts, off)
		if k.keeps(line) {
			return
		}
		out = append(out, Finding{Kind: kind, File: rel, Line: line, Seconds: s, Detail: detail, offset: off})
	}
	sleeps := rePySleep.FindAllStringIndex(m, -1)
	if rePyImport.MatchString(m) {
		for _, loc := range rePyBare.FindAllStringIndex(m, -1) {
			sleeps = append(sleeps, []int{loc[0] + strings.Index(m[loc[0]:loc[1]], "sleep"), loc[1]})
		}
	}
	for _, loc := range sleeps {
		args := callArgs(m, loc[1]-1)
		var s *float64
		detail := "sleep"
		if len(args) == 1 && rePyNumber.MatchString(args[0]) {
			if v, err := strconv.ParseFloat(strings.ReplaceAll(args[0], "_", ""), 64); err == nil {
				s = secs(v)
				detail = fmt.Sprintf("sleep(%s) is a fixed wait of %gs", args[0], v)
			}
		}
		add(KindFixedWait, loc[0], s, detail)
	}
	for _, loc := range rePyScreenSh.FindAllStringIndex(m, -1) {
		add(KindScreenshot, loc[0], nil, "screenshot call")
	}
	sortByOffset(out)
	return out
}

func sortByOffset(fs []Finding) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && fs[j].offset < fs[j-1].offset; j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}
