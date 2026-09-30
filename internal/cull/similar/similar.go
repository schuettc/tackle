// Package similar groups near-duplicate tests within a file so Jev can
// judge them together for table-test consolidation. Ported from Phase 0's
// harness/group.py: same normalization rules, same difflib.SequenceMatcher
// similarity (including its autojunk heuristic), same no-chaining
// clustering.
package similar

import (
	"regexp"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/cull/cases"
)

const (
	threshold = 0.85
	minLen    = 40
)

var (
	pyDocstring   = regexp.MustCompile(`(?s)""".*?"""|'''.*?'''`)
	pyLineComment = regexp.MustCompile(`#.*`)
	pyString      = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'`)

	goBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	goLineComment  = regexp.MustCompile(`//.*`)
	goString       = regexp.MustCompile("\"[^\"\\n]*\"|'[^'\\n]*'|`[^`]*`")

	number     = regexp.MustCompile(`\b\d+(\.\d+)?\b`)
	whitespace = regexp.MustCompile(`\s+`)
)

// Norm normalizes a test body for similarity comparison: drops the first
// (signature) line, strips comments (python: docstrings and # comments;
// go/ts: // and /* */ comments), maps string and numeric literals to S/N,
// and collapses whitespace.
func Norm(lang, body string) string {
	var b string
	switch lang {
	case "python":
		b = pyDocstring.ReplaceAllString(body, "")
		b = pyLineComment.ReplaceAllString(b, "")
		b = dropFirstLine(b)
		b = pyString.ReplaceAllString(b, "S")
	default: // "go", "typescript", and anything else use the go/ts rules
		b = goBlockComment.ReplaceAllString(body, "")
		b = goLineComment.ReplaceAllString(b, "")
		b = dropFirstLine(b)
		b = goString.ReplaceAllString(b, "S")
	}
	b = number.ReplaceAllString(b, "N")
	return strings.TrimSpace(whitespace.ReplaceAllString(b, " "))
}

func dropFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Ratio returns the similarity of a and b using the same algorithm as
// Python's difflib.SequenceMatcher(None, a, b).ratio() with the default
// autojunk=True: 2*M/T where M is the total length of matching blocks and
// T is len(a)+len(b) (in Unicode code points).
func Ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	length := len(ra) + len(rb)
	if length == 0 {
		return 1.0
	}
	matches := matchCount(ra, rb)
	return 2.0 * float64(matches) / float64(length)
}

type quad struct{ alo, ahi, blo, bhi int }

type block struct{ i, j, k int }

func chainB(b []rune) map[rune][]int {
	b2j := make(map[rune][]int)
	for i, elt := range b {
		b2j[elt] = append(b2j[elt], i)
	}
	n := len(b)
	if n >= 200 {
		ntest := n/100 + 1
		for elt, idxs := range b2j {
			if len(idxs) > ntest {
				delete(b2j, elt)
			}
		}
	}
	return b2j
}

// findLongestMatch mirrors difflib.SequenceMatcher.find_longest_match with
// isjunk=None (so the "junk" extension steps are no-ops: bjunk is always
// empty).
func findLongestMatch(a, b []rune, b2j map[rune][]int, alo, ahi, blo, bhi int) (besti, bestj, bestsize int) {
	besti, bestj, bestsize = alo, blo, 0
	j2len := make(map[int]int)
	for i := alo; i < ahi; i++ {
		newj2len := make(map[int]int)
		for _, j := range b2j[a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}

	for besti > alo && bestj > blo && a[besti-1] == b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && a[besti+bestsize] == b[bestj+bestsize] {
		bestsize++
	}
	return besti, bestj, bestsize
}

func matchingBlocks(a, b []rune) []block {
	b2j := chainB(b)
	la, lb := len(a), len(b)
	queue := []quad{{0, la, 0, lb}}
	var raw []block
	for len(queue) > 0 {
		q := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := findLongestMatch(a, b, b2j, q.alo, q.ahi, q.blo, q.bhi)
		if k != 0 {
			raw = append(raw, block{i, j, k})
			if q.alo < i && q.blo < j {
				queue = append(queue, quad{q.alo, i, q.blo, j})
			}
			if i+k < q.ahi && j+k < q.bhi {
				queue = append(queue, quad{i + k, q.ahi, j + k, q.bhi})
			}
		}
	}
	sort.Slice(raw, func(x, y int) bool {
		if raw[x].i != raw[y].i {
			return raw[x].i < raw[y].i
		}
		if raw[x].j != raw[y].j {
			return raw[x].j < raw[y].j
		}
		return raw[x].k < raw[y].k
	})

	var out []block
	i1, j1, k1 := 0, 0, 0
	for _, c := range raw {
		if i1+k1 == c.i && j1+k1 == c.j {
			k1 += c.k
		} else {
			if k1 != 0 {
				out = append(out, block{i1, j1, k1})
			}
			i1, j1, k1 = c.i, c.j, c.k
		}
	}
	if k1 != 0 {
		out = append(out, block{i1, j1, k1})
	}
	out = append(out, block{la, lb, 0})
	return out
}

func matchCount(a, b []rune) int {
	total := 0
	for _, blk := range matchingBlocks(a, b) {
		total += blk.k
	}
	return total
}

// Groups clusters same-file tests whose normalized bodies are near-duplicates.
// A test joins the first cluster it is >= 0.85 similar to every member of
// (per Ratio, over Norm'd bodies of length >= 40); otherwise it starts a new
// cluster. Only clusters with 2+ members are returned, one per file, sorted
// by member id.
func Groups(cs []cases.TestCase) []cases.Group {
	var fileOrder []string
	byFile := make(map[string][]cases.TestCase)
	for _, c := range cs {
		if _, ok := byFile[c.File]; !ok {
			fileOrder = append(fileOrder, c.File)
		}
		byFile[c.File] = append(byFile[c.File], c)
	}

	var out []cases.Group
	for _, file := range fileOrder {
		ts := byFile[file]
		norms := make(map[string]string, len(ts))
		for _, t := range ts {
			norms[t.ID] = Norm(t.Lang, t.Body)
		}
		sim := func(x, y cases.TestCase) bool {
			nx, ny := norms[x.ID], norms[y.ID]
			if len([]rune(nx)) < minLen || len([]rune(ny)) < minLen {
				return false
			}
			return Ratio(nx, ny) >= threshold
		}

		var clusters [][]cases.TestCase
		for _, t := range ts {
			placed := false
			for ci, cl := range clusters {
				all := true
				for _, m := range cl {
					if !sim(t, m) {
						all = false
						break
					}
				}
				if all {
					clusters[ci] = append(clusters[ci], t)
					placed = true
					break
				}
			}
			if !placed {
				clusters = append(clusters, []cases.TestCase{t})
			}
		}

		for _, cl := range clusters {
			if len(cl) < 2 {
				continue
			}
			sort.Slice(cl, func(i, j int) bool { return cl[i].ID < cl[j].ID })
			ids := make([]string, len(cl))
			for i, t := range cl {
				ids[i] = t.ID
			}
			out = append(out, cases.Group{
				ID:        cases.GroupID(ids),
				Lang:      cl[0].Lang,
				Framework: cl[0].Framework,
				File:      cl[0].File,
				Tests:     cl,
			})
		}
	}
	return out
}
