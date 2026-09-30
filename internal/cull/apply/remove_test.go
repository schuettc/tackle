package apply

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
)

// TestRemoveSpansBottomUpExact removes two spans (styled the way the Go
// extractor reports them: End is the offset right after the closing
// brace, not including its trailing newline) from one file and checks
// that every byte outside them is untouched, and that the file keeps its
// separator (and ends with one newline) where each span used to be.
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

	// Removing the last decl also drops the blank line before it: the
	// file ends with exactly one newline (I-1).
	want := "package a\n\nfunc keep() {}\n"
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
	want := "package a\r\n\r\nfunc before() {}\r\n"
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
	want := "package a\r\n\r\nfunc keep() {}\r\n"
	if got != want {
		t.Fatalf("RemoveSpans =\n%q\nwant\n%q", got, want)
	}
}

// span returns the Span of the first occurrence of part in src.
func span(t *testing.T, src, part string) cases.Span {
	t.Helper()
	i := strings.Index(src, part)
	if i < 0 {
		t.Fatalf("%q not in src", part)
	}
	return cases.Span{Start: i, End: i + len(part)}
}

// TestRemoveSpansKeepsFileSeparator (I-1): each file keeps its own
// separator between top-level decls (2 blank lines in Python, 1 in TS),
// removing the last decl leaves exactly one newline at EOF, and removing
// the first test after the imports leaves no stray blank lines. Python
// spans are whole lines (End after the "\n"); TS spans end at ")".
func TestRemoveSpansKeepsFileSeparator(t *testing.T) {
	py := "import os\n\n\ndef test_a():\n    assert 1\n\n\ndef test_b():\n    assert 2\n\n\ndef test_c():\n    assert 3\n"
	pyA := "def test_a():\n    assert 1\n"
	pyB := "def test_b():\n    assert 2\n"
	pyC := "def test_c():\n    assert 3\n"
	ts := "import { f } from \"./f\";\n\ntest(\"a\", () => {\n  f(1);\n})\n\ntest(\"b\", () => {\n  f(2);\n})\n\ntest(\"c\", () => {\n  f(3);\n})\n"
	tsB := "test(\"b\", () => {\n  f(2);\n})"
	tsC := "test(\"c\", () => {\n  f(3);\n})"
	tsA := "test(\"a\", () => {\n  f(1);\n})"
	for _, c := range []struct {
		name, src string
		parts     []string
		want      string
	}{
		{"python middle", py, []string{pyB},
			"import os\n\n\ndef test_a():\n    assert 1\n\n\ndef test_c():\n    assert 3\n"},
		{"python last", py, []string{pyC},
			"import os\n\n\ndef test_a():\n    assert 1\n\n\ndef test_b():\n    assert 2\n"},
		{"python first after imports", py, []string{pyA},
			"import os\n\n\ndef test_b():\n    assert 2\n\n\ndef test_c():\n    assert 3\n"},
		{"python last two", py, []string{pyB, pyC},
			"import os\n\n\ndef test_a():\n    assert 1\n"},
		{"ts middle", ts, []string{tsB},
			"import { f } from \"./f\";\n\ntest(\"a\", () => {\n  f(1);\n})\n\ntest(\"c\", () => {\n  f(3);\n})\n"},
		{"ts last", ts, []string{tsC},
			"import { f } from \"./f\";\n\ntest(\"a\", () => {\n  f(1);\n})\n\ntest(\"b\", () => {\n  f(2);\n})\n"},
		{"ts first after imports", ts, []string{tsA},
			"import { f } from \"./f\";\n\ntest(\"b\", () => {\n  f(2);\n})\n\ntest(\"c\", () => {\n  f(3);\n})\n"},
		{"python crlf last", strings.ReplaceAll(py, "\n", "\r\n"), []string{strings.ReplaceAll(pyC, "\n", "\r\n")},
			strings.ReplaceAll("import os\n\n\ndef test_a():\n    assert 1\n\n\ndef test_b():\n    assert 2\n", "\n", "\r\n")},
	} {
		t.Run(c.name, func(t *testing.T) {
			var spans []cases.Span
			for _, p := range c.parts {
				spans = append(spans, span(t, c.src, p))
			}
			if got := string(RemoveSpans([]byte(c.src), spans)); got != c.want {
				t.Errorf("RemoveSpans =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

// TestTSSemicolonSpans (I-1): a TS removal also takes a directly
// following ";", so no line holding only ";" is left.
func TestTSSemicolonSpans(t *testing.T) {
	src := "import { f } from \"./f\";\n\ntest(\"a\", () => {\n  f(1);\n});\n\ntest(\"b\", () => {\n  f(2);\n}) ;\n"
	a := span(t, src, "test(\"a\", () => {\n  f(1);\n})")
	b := span(t, src, "test(\"b\", () => {\n  f(2);\n})")
	got := string(RemoveSpans([]byte(src), withTSSemicolons([]byte(src), []cases.Span{a})))
	want := "import { f } from \"./f\";\n\ntest(\"b\", () => {\n  f(2);\n}) ;\n"
	if got != want {
		t.Errorf("remove a =\n%q\nwant\n%q", got, want)
	}
	got = string(RemoveSpans([]byte(src), withTSSemicolons([]byte(src), []cases.Span{b})))
	want = "import { f } from \"./f\";\n\ntest(\"a\", () => {\n  f(1);\n});\n"
	if got != want {
		t.Errorf("remove b =\n%q\nwant\n%q", got, want)
	}
}
