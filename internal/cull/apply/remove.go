package apply

import (
	"sort"

	"github.com/schuettc/tackle/internal/cull/cases"
)

// RemoveSpans deletes each span from src and returns the result. It works
// bottom-up (highest offset first) so earlier spans' offsets stay valid
// as later ones are applied. Along with each span it takes the
// indentation before it and the line ending after it (Python spans are
// whole lines and already end in one), then settles the blank lines
// around the hole so the file keeps its own separator: the larger of the
// blank runs before and after it stays (between tests they are equal, so
// the run before is kept); at the start or end of the file none stays,
// so the file never starts with blank lines and ends with exactly one
// newline. Every other byte is left exactly as it was, and "\r\n" is one
// line ending throughout.
func RemoveSpans(src []byte, spans []cases.Span) []byte {
	ordered := append([]cases.Span(nil), spans...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start > ordered[j].Start })

	out := append([]byte(nil), src...)
	for _, sp := range ordered {
		start, end := sp.Start, sp.End
		if start < 0 || end < start || end > len(out) {
			continue
		}
		i := start
		for i > 0 && (out[i-1] == ' ' || out[i-1] == '\t') {
			i--
		}
		atLineStart := i == 0 || out[i-1] == '\n'
		if atLineStart {
			start = i
		}
		if end == start || out[end-1] != '\n' {
			j := end
			for j < len(out) && (out[j] == ' ' || out[j] == '\t') {
				j++
			}
			if n := nextNewlineLen(out, j); n > 0 {
				end = j + n
			} else if j == len(out) {
				end = j
			}
		}
		out = append(out[:start], out[end:]...)
		if atLineStart {
			out = settleBlankLines(out, start)
		}
	}
	return out
}

// blankLineAt returns the length of the blank line (spaces/tabs, then a
// line ending) starting at j, or 0 if the line there is not blank.
func blankLineAt(out []byte, j int) int {
	k := j
	for k < len(out) && (out[k] == ' ' || out[k] == '\t') {
		k++
	}
	if n := nextNewlineLen(out, k); n > 0 {
		return k + n - j
	}
	return 0
}

// settleBlankLines, given p (a line start where a removal just joined
// out), keeps the larger of the blank-line runs before and after p, or
// none of them when p is at the start or end of the file (only blank
// lines between it and the file's edge).
func settleBlankLines(out []byte, p int) []byte {
	afterEnd, nAfter := p, 0
	for {
		n := blankLineAt(out, afterEnd)
		if n == 0 {
			break
		}
		afterEnd += n
		nAfter++
	}
	beforeStart, nBefore := p, 0
	for beforeStart > 0 {
		// The line ending just before beforeStart closes the previous
		// line; that line is blank if it holds only spaces/tabs.
		n := lastNewlineLen(out, beforeStart)
		if n == 0 {
			break
		}
		k := beforeStart - n
		for k > 0 && (out[k-1] == ' ' || out[k-1] == '\t') {
			k--
		}
		if k != 0 && out[k-1] != '\n' {
			break // a content line
		}
		if k == 0 && blankLineAt(out, 0) == 0 {
			break
		}
		beforeStart = k
		nBefore++
	}
	switch {
	case afterEnd == len(out) || beforeStart == 0:
		return append(out[:beforeStart], out[afterEnd:]...)
	case nAfter > nBefore:
		return append(out[:beforeStart], out[p:]...)
	default:
		return append(out[:p], out[afterEnd:]...)
	}
}

// withTSSemicolons extends each span over a directly following `;` (after
// optional spaces/tabs): the TS extractor's span is the test call, not
// its statement, and removing only the call would leave a line holding a
// lone ";". The extractor's spans are left as they are (hashes/bodies
// stay calibrated); only the removal grows.
func withTSSemicolons(src []byte, spans []cases.Span) []cases.Span {
	out := make([]cases.Span, len(spans))
	for i, sp := range spans {
		j := sp.End
		for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
			j++
		}
		if j < len(src) && src[j] == ';' {
			sp.End = j + 1
		}
		out[i] = sp
	}
	return out
}

// nextNewlineLen returns the length (2 for "\r\n", 1 for "\n", 0 if
// neither) of the line ending starting at position j in out.
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
// before position i in out (the mirror of nextNewlineLen).
func lastNewlineLen(out []byte, i int) int {
	if i >= 2 && out[i-2] == '\r' && out[i-1] == '\n' {
		return 2
	}
	if i >= 1 && out[i-1] == '\n' {
		return 1
	}
	return 0
}
