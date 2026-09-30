package check

import (
	"bytes"
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/extract"
)

func TestWriteTableFormat(t *testing.T) {
	r := Report{
		Tests: []TestResult{
			{TestCase: cases.TestCase{ID: "go:b_test.go:TestB", File: "b_test.go"}, Verdict: "keep", Reasons: []string{"pins behaviour"}},
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestA2", File: "a_test.go"}, Err: "jev: 422"},
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestA1", File: "a_test.go"}, Verdict: "cut", Reasons: []string{"tautology", "no assertion"}},
		},
		Groups: []GroupResult{
			{ID: "g2", File: "a_test.go", Members: []string{"go:a_test.go:TestA1", "go:a_test.go:TestA2"}, Verdict: "consolidate"},
			{ID: "g1", File: "c_test.go", Err: "jev: 500"},
		},
		Skipped: []extract.Skipped{
			{File: "z_test.go", Reason: "parse: z_test.go:3:1: expected ')'"},
			{File: "web/a.test.ts", Reason: "typescript not found under /r"},
		},
	}
	var buf bytes.Buffer
	WriteTable(&buf, r)
	want := "a_test.go\n" +
		"  cut  go:a_test.go:TestA1  tautology, no assertion\n" +
		"  error  go:a_test.go:TestA2  jev: 422\n" +
		"  consolidate  2 tests: go:a_test.go:TestA1, go:a_test.go:TestA2\n" +
		"b_test.go\n" +
		"  keep  go:b_test.go:TestB  pins behaviour\n" +
		"c_test.go\n" +
		"  error  g1  jev: 500\n" +
		"skipped\n" +
		"  web/a.test.ts  typescript not found under /r\n" +
		"  z_test.go  parse: z_test.go:3:1: expected ')'\n"
	if got := buf.String(); got != want {
		t.Errorf("WriteTable =\n%s\nwant\n%s", got, want)
	}

	buf.Reset()
	WriteTable(&buf, Report{})
	if buf.String() != "" {
		t.Errorf("empty report table = %q, want empty", buf.String())
	}
}
