package apply

import (
	"sort"

	"github.com/schuettc/tackle/internal/cull/cases"
)

// RemoveSpans deletes each span from src and returns the result. It works
// bottom-up (highest offset first) so earlier spans' offsets stay valid
// as later ones are applied. Along with each span it also swallows the
// single newline that ends it (if any), and then collapses any run of
// blank lines the deletion leaves behind to at most one blank line.
// Every byte outside the removed spans (and that trailing newline, and
// collapsed blank lines) is left exactly as it was.
func RemoveSpans(src []byte, spans []cases.Span) []byte {
	ordered := append([]cases.Span(nil), spans...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start > ordered[j].Start })

	out := append([]byte(nil), src...)
	for _, sp := range ordered {
		start, end := sp.Start, sp.End
		if start < 0 || end < start || end > len(out) {
			continue
		}
		if n := nextNewlineLen(out, end); n > 0 {
			end += n
		}
		out = append(out[:start], out[end:]...)
		out = collapseBlankRun(out, start)
	}
	return out
}

// nextNewlineLen returns the length (2 for "\r\n", 1 for "\n", 0 if
// neither) of the line ending starting at position j in out. Treating
// "\r\n" as a single unit keeps CRLF files from being partially
// converted to LF by span removal or blank-run collapsing.
func nextNewlineLen(out []byte, j int) int {
	if j+1 < len(out) && out[j] == '\r' && out[j+1] == '\n' {
		return 2
	}
	if j < len(out) && out[j] == '\n' {
		return 1
	}
	return 0
}

// lastNewlineLen returns the length of the line ending immediately
// before position i in out (the mirror of nextNewlineLen, used to walk
// backward over a run of line endings).
func lastNewlineLen(out []byte, i int) int {
	if i >= 2 && out[i-2] == '\r' && out[i-1] == '\n' {
		return 2
	}
	if i >= 1 && out[i-1] == '\n' {
		return 1
	}
	return 0
}

// collapseBlankRun, given the position where a deletion just joined two
// halves of out, trims any run of 3+ consecutive line endings spanning
// that position down to exactly 2 (one blank line), counting a "\r\n"
// pair as a single line ending so CRLF files keep their own ending style.
func collapseBlankRun(out []byte, at int) []byte {
	i := at
	for {
		n := lastNewlineLen(out, i)
		if n == 0 {
			break
		}
		i -= n
	}
	j := i
	units := 0
	keepEnd := i
	for {
		n := nextNewlineLen(out, j)
		if n == 0 {
			break
		}
		j += n
		units++
		if units == 2 {
			keepEnd = j
		}
	}
	if units >= 3 {
		out = append(out[:keepEnd], out[j:]...)
	}
	return out
}
