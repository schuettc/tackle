package corpus

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/schuettc/tackle/internal/cull/policy"
)

// SheetLabel is one filled-in label from a labeling sheet.
type SheetLabel struct {
	ID    string
	Label policy.Verdict
	Note  string
}

// fence returns a backtick fence longer than any backtick run in s.
func fence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// WriteSheet writes a markdown labeling sheet: per entry a heading with its
// ID, blank label/note lines to fill in, the test source, and a collapsed
// block with setup and code under test.
func WriteSheet(w io.Writer, entries []Entry) error {
	var b strings.Builder
	for _, e := range entries {
		s := e.State
		fmt.Fprintf(&b, "## %s\nlabel: %s\nnote: %s\n\n", e.ID, e.Label, e.Note)
		f := fence(s.TestSource)
		fmt.Fprintf(&b, "%s%s\n%s\n%s\n", f, s.Language, s.TestSource, f)
		var more strings.Builder
		if s.SetupContext != "" {
			more.WriteString(s.SetupContext + "\n\n")
		}
		for _, c := range s.CodeUnderTest {
			fmt.Fprintf(&more, "// %s (%s)\n%s\n\n", c.Symbol, c.File, c.Source)
		}
		if more.Len() > 0 {
			body := strings.TrimRight(more.String(), "\n")
			f := fence(body)
			fmt.Fprintf(&b, "<details><summary>setup + code under test</summary>\n\n%s%s\n%s\n%s\n</details>\n", f, s.Language, body, f)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// ReadSheet returns the filled-in labels. Lines inside code fences are
// ignored; a blank label skips the entry; an invalid label is an error that
// names its line.
func ReadSheet(r io.Reader) ([]SheetLabel, error) {
	var out []SheetLabel
	var cur *SheetLabel
	var open string // current fence, "" when outside
	flush := func() {
		if cur != nil && cur.Label != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if open != "" {
			if strings.HasPrefix(trim, open) && strings.Trim(trim, "`") == "" {
				open = ""
			}
			continue
		}
		if strings.HasPrefix(trim, "```") {
			open = trim[:len(trim)-len(strings.TrimLeft(trim, "`"))]
			continue
		}
		switch {
		case strings.HasPrefix(line, "## "):
			flush()
			cur = &SheetLabel{ID: strings.TrimSpace(line[3:])}
		case cur != nil && strings.HasPrefix(line, "label:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "label:"))
			if v == "" {
				continue
			}
			verdict, err := policy.ParseVerdict(v)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			cur.Label = verdict
		case cur != nil && strings.HasPrefix(line, "note:"):
			cur.Note = strings.TrimSpace(strings.TrimPrefix(line, "note:"))
		}
	}
	flush()
	return out, sc.Err()
}
