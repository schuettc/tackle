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
		if end < len(out) && out[end] == '\n' {
			end++
		}
		out = append(out[:start], out[end:]...)
		out = collapseBlankRun(out, start)
	}
	return out
}

// collapseBlankRun, given the position where a deletion just joined two
// halves of out, trims any run of 3+ consecutive newlines spanning that
// position down to exactly 2 (one blank line).
func collapseBlankRun(out []byte, at int) []byte {
	i := at
	for i > 0 && out[i-1] == '\n' {
		i--
	}
	j := at
	for j < len(out) && out[j] == '\n' {
		j++
	}
	if j-i >= 3 {
		out = append(out[:i+2], out[j:]...)
	}
	return out
}
