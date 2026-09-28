package apply

import (
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
)

// TestRemoveSpansBottomUpExact removes two spans (styled the way the Go
// extractor reports them: End is the offset right after the closing
// brace, not including its trailing newline) from one file and checks
// that every byte outside them is untouched, and that no more than one
// blank line remains where each span used to be.
func TestRemoveSpansBottomUpExact(t *testing.T) {
	prefix := "package a\n\n"
	testOne := "func TestOne(t *testing.T) {\n\tif 1 != 1 {\n\t\tt.Fail()\n\t}\n}"
	middle := "\n\nfunc keep() {}\n\n"
	testTwo := "func TestTwo(t *testing.T) {\n\tif 2 != 2 {\n\t\tt.Fail()\n\t}\n}"
	suffix := "\n"

	src := prefix + testOne + middle + testTwo + suffix

	oneStart := len(prefix)
	oneEnd := oneStart + len(testOne)
	twoStart := len(prefix) + len(testOne) + len(middle)
	twoEnd := twoStart + len(testTwo)

	if src[oneStart:oneEnd] != testOne {
		t.Fatalf("oneStart/oneEnd slice mismatch: %q", src[oneStart:oneEnd])
	}
	if src[twoStart:twoEnd] != testTwo {
		t.Fatalf("twoStart/twoEnd slice mismatch: %q", src[twoStart:twoEnd])
	}

	spans := []cases.Span{
		{Start: oneStart, End: oneEnd},
		{Start: twoStart, End: twoEnd},
	}

	got := string(RemoveSpans([]byte(src), spans))

	// The blank line that used to separate func keep from TestTwo is
	// now trailing whitespace at EOF: one blank line, which the "no
	// more than one" rule permits (RemoveSpans doesn't otherwise trim
	// EOF whitespace; that's gofmt's job, done later by TidyGo).
	want := "package a\n\nfunc keep() {}\n\n"
	if got != want {
		t.Fatalf("RemoveSpans =\n%q\nwant\n%q", got, want)
	}
}

// TestRemoveSpansPreservesBytesOutsideSpan checks that content before and
// after a single removed span is byte-for-byte unchanged.
func TestRemoveSpansPreservesBytesOutsideSpan(t *testing.T) {
	prefix := "package a\n\nfunc before() {}\n\n"
	target := "func TestA() {}"
	suffix := "\n\nfunc after() {}\n"
	src := prefix + target + suffix
	start := len(prefix)
	end := start + len(target)

	got := string(RemoveSpans([]byte(src), []cases.Span{{Start: start, End: end}}))
	want := "package a\n\nfunc before() {}\n\nfunc after() {}\n"
	if got != want {
		t.Fatalf("RemoveSpans =\n%q\nwant\n%q", got, want)
	}
}

func TestRemoveSpansNoSpans(t *testing.T) {
	src := "package a\n"
	got := string(RemoveSpans([]byte(src), nil))
	if got != src {
		t.Fatalf("RemoveSpans with no spans = %q, want unchanged %q", got, src)
	}
}

// TestRemoveSpansCRLFMiddle removes a span in the middle of a CRLF file
// and checks the result keeps CRLF endings and collapses the blank run
// to exactly one blank line ("\r\n\r\n"), never mixing in bare "\n".
func TestRemoveSpansCRLFMiddle(t *testing.T) {
	prefix := "package a\r\n\r\nfunc before() {}\r\n\r\n"
	target := "func TestA() {\r\n\tt.Fail()\r\n}"
	suffix := "\r\n\r\nfunc after() {}\r\n"
	src := prefix + target + suffix
	start := len(prefix)
	end := start + len(target)

	got := string(RemoveSpans([]byte(src), []cases.Span{{Start: start, End: end}}))
	want := "package a\r\n\r\nfunc before() {}\r\n\r\nfunc after() {}\r\n"
	if got != want {
		t.Fatalf("RemoveSpans =\n%q\nwant\n%q", got, want)
	}
}

// TestRemoveSpansCRLFEndOfFileNoTrailingNewline removes a span that is
// the last bytes of a CRLF file with no trailing newline at all.
func TestRemoveSpansCRLFEndOfFileNoTrailingNewline(t *testing.T) {
	prefix := "package a\r\n\r\nfunc before() {}\r\n\r\n"
	target := "func TestA() {\r\n\tt.Fail()\r\n}"
	src := prefix + target
	start := len(prefix)
	end := start + len(target)

	got := string(RemoveSpans([]byte(src), []cases.Span{{Start: start, End: end}}))
	want := "package a\r\n\r\nfunc before() {}\r\n\r\n"
	if got != want {
		t.Fatalf("RemoveSpans =\n%q\nwant\n%q", got, want)
	}
}

// TestRemoveSpansCRLFTwoSpans removes two spans from a CRLF file and
// checks every byte outside them is untouched and CRLF is preserved
// throughout, including in the collapsed blank run.
func TestRemoveSpansCRLFTwoSpans(t *testing.T) {
	prefix := "package a\r\n\r\n"
	testOne := "func TestOne(t *testing.T) {\r\n\tif 1 != 1 {\r\n\t\tt.Fail()\r\n\t}\r\n}"
	middle := "\r\n\r\nfunc keep() {}\r\n\r\n"
	testTwo := "func TestTwo(t *testing.T) {\r\n\tif 2 != 2 {\r\n\t\tt.Fail()\r\n\t}\r\n}"
	suffix := "\r\n"

	src := prefix + testOne + middle + testTwo + suffix

	oneStart := len(prefix)
	oneEnd := oneStart + len(testOne)
	twoStart := len(prefix) + len(testOne) + len(middle)
	twoEnd := twoStart + len(testTwo)

	spans := []cases.Span{
		{Start: oneStart, End: oneEnd},
		{Start: twoStart, End: twoEnd},
	}

	got := string(RemoveSpans([]byte(src), spans))
	want := "package a\r\n\r\nfunc keep() {}\r\n\r\n"
	if got != want {
		t.Fatalf("RemoveSpans =\n%q\nwant\n%q", got, want)
	}
}
