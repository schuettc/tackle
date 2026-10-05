package apply

// The text half of apply: finding a row's passage in a file as it is at the
// base now (it may have moved since the audit), replacing or deleting it,
// and inserting text at the end of a section. Pure functions over lines.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// split returns a file's lines without the final newline's empty line.
func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// find returns the 0-based index where passage's lines are in lines: at its
// audited line (start, 1-based) when it is still there, else its one place
// in the file. Lines must match exactly.
func find(lines []string, passage string, start int) (int, error) {
	want := split(passage)
	if len(want) == 0 {
		return -1, fmt.Errorf("the row has no passage")
	}
	at := func(i int) bool {
		if i < 0 || i+len(want) > len(lines) {
			return false
		}
		for j, w := range want {
			if lines[i+j] != w {
				return false
			}
		}
		return true
	}
	if at(start - 1) {
		return start - 1, nil
	}
	var hits []int
	for i := range lines {
		if at(i) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return -1, fmt.Errorf("passage not found at the base (the file changed since the audit)")
	case 1:
		return hits[0], nil
	}
	return -1, fmt.Errorf("passage is in %d places at the base and none is the audited line", len(hits))
}

// op places a row's passage in a file.
type op struct {
	row     string
	passage string
	start   int
}

type splice struct {
	row      string
	at, n    int
	newLines []string
}

type insertion struct {
	section, text string
}

// fileEdit is one file's edits: splices over the base's lines (never
// overlapping), then insertions at the ends of sections.
type fileEdit struct {
	lines   []string
	splices []splice
	inserts []insertion
	// whole: the whole file is replaced (rewrite) or removed (delete).
	whole   bool
	gone    bool
	content string
}

// replace puts text (empty: nothing) where the row's passage is.
func (f *fileEdit) replace(o op, text string) error {
	if f.whole {
		return fmt.Errorf("the whole file is already changed by another row")
	}
	i, err := find(f.lines, o.passage, o.start)
	if err != nil {
		return err
	}
	n := len(split(o.passage))
	for _, s := range f.splices {
		if i < s.at+s.n && s.at < i+n {
			return fmt.Errorf("overlaps the passage of row %s", s.row)
		}
	}
	f.splices = append(f.splices, splice{row: o.row, at: i, n: n, newLines: split(text)})
	return nil
}

// replaceWhole replaces the whole file with text, or removes it (gone).
func (f *fileEdit) replaceWhole(text string, gone bool) error {
	if f.whole || len(f.splices) > 0 || len(f.inserts) > 0 {
		return fmt.Errorf("another row already changes this file")
	}
	f.whole, f.gone, f.content = true, gone, text
	return nil
}

// insert adds text at the end of the section headed section (any level,
// case-insensitive), or at the end of the file when section is "". A
// section the file lacks is added at its end.
func (f *fileEdit) insert(section, text string) {
	f.inserts = append(f.inserts, insertion{section: section, text: text})
}

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

func heading(l string) (int, string) {
	m := headingRE.FindStringSubmatch(l)
	if m == nil {
		return 0, ""
	}
	return len(m[1]), m[2]
}

func blank(l string) bool { return strings.TrimSpace(l) == "" }

// String is the file after its edits.
func (f *fileEdit) String() string {
	if f.whole {
		return ensureNL(f.content)
	}
	lines := append([]string{}, f.lines...)
	sp := append([]splice{}, f.splices...)
	sort.Slice(sp, func(i, j int) bool { return sp[i].at > sp[j].at })
	for _, s := range sp {
		out := append(append(append([]string{}, lines[:s.at]...), s.newLines...), lines[s.at+s.n:]...)
		// A removed paragraph leaves one blank line, not two.
		if len(s.newLines) == 0 && s.at > 0 && blank(out[s.at-1]) && (s.at == len(out) || blank(out[s.at])) {
			out = append(out[:s.at-1], out[s.at:]...)
		}
		lines = out
	}
	for _, in := range f.inserts {
		lines = insertAt(lines, in)
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func insertAt(lines []string, in insertion) []string {
	text := split(in.text)
	if in.section != "" {
		for h, l := range lines {
			lvl, title := heading(l)
			if lvl == 0 || !strings.EqualFold(title, in.section) {
				continue
			}
			end := len(lines)
			for k := h + 1; k < len(lines); k++ {
				if l2, _ := heading(lines[k]); l2 > 0 && l2 <= lvl {
					end = k
					break
				}
			}
			last := h
			for k := end - 1; k > h; k-- {
				if !blank(lines[k]) {
					last = k
					break
				}
			}
			add := text
			if last == h {
				add = append([]string{""}, text...)
			}
			return append(append(append([]string{}, lines[:last+1]...), add...), lines[last+1:]...)
		}
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	var add []string
	if len(lines) > 0 {
		add = append(add, "")
	}
	if in.section != "" {
		add = append(add, "## "+in.section, "")
	}
	return append(append(lines, add...), text...)
}

func ensureNL(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// parseDestination splits "path#Section" (or "path § Section") into the
// path and the section's heading.
func parseDestination(d string) (string, string) {
	for _, sep := range []string{"#", "§"} {
		if p, s, ok := strings.Cut(d, sep); ok {
			return strings.TrimSpace(p), strings.TrimSpace(s)
		}
	}
	return strings.TrimSpace(d), ""
}
