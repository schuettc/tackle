package golang

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/extract"
)

const testdataDir = "testdata"

func extractAll(t *testing.T, root string, maxContext int) extract.Result {
	t.Helper()
	ex := New()
	var files []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ex.Match(rel) {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	res, err := ex.Extract(root, files, maxContext)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return res
}

func caseByID(t *testing.T, res extract.Result, id string) cases.TestCase {
	t.Helper()
	for _, c := range res.Cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("case %s not found; have: %v", id, ids(res.Cases))
	return cases.TestCase{}
}

func ids(cs []cases.TestCase) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestMatch(t *testing.T) {
	ex := New()
	want := map[string]bool{
		"pkg/foo_test.go":        true,
		"foo_test.go":            true,
		"pkg/foo.go":             false,
		"testdata/x_test.go":     false,
		"pkg/testdata/y_test.go": false,
		"vendor/mod/x_test.go":   false,
	}
	for p, w := range want {
		if got := ex.Match(p); got != w {
			t.Errorf("Match(%q) = %v, want %v", p, got, w)
		}
	}
	if ex.Lang() != "go" {
		t.Errorf("Lang() = %q, want go", ex.Lang())
	}
}

func TestTopLevelParity(t *testing.T) {
	res := extractAll(t, testdataDir, 24000)
	tc := caseByID(t, res, "go:calc_test.go:TestAdd")

	wantBody := "// TestAdd checks addition.\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatalf(\"bad sum\")\n\t}\n}"
	if tc.Body != wantBody {
		t.Errorf("Body =\n%s\nwant\n%s", tc.Body, wantBody)
	}
	if tc.Context != "" {
		t.Errorf("Context = %q, want empty (TestAdd references no test-file helpers)", tc.Context)
	}
	if len(tc.Callees) != 1 || tc.Callees[0].Symbol != "Add" {
		t.Fatalf("Callees = %+v, want exactly [Add]", tc.Callees)
	}
	wantCalleeSrc := "// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn a + b\n}"
	if tc.Callees[0].Source != wantCalleeSrc {
		t.Errorf("Callee source =\n%s\nwant\n%s", tc.Callees[0].Source, wantCalleeSrc)
	}
	if tc.Callees[0].File != "calc.go" {
		t.Errorf("Callee file = %q, want calc.go", tc.Callees[0].File)
	}
	if tc.Parent != "" {
		t.Errorf("Parent = %q, want empty for a top-level test with no subtests", tc.Parent)
	}
	if tc.Hash != cases.HashBody(tc.Body) {
		t.Errorf("Hash %q != HashBody(Body)", tc.Hash)
	}
	if tc.Lang != "go" || tc.Framework != "testing" || tc.File != "calc_test.go" || tc.Name != "TestAdd" {
		t.Errorf("case metadata wrong: %+v", tc)
	}
}

func TestSpansAreExactBytes(t *testing.T) {
	res := extractAll(t, testdataDir, 24000)
	if len(res.Cases) == 0 {
		t.Fatal("no cases extracted")
	}
	fileBytes := map[string][]byte{}
	for _, c := range res.Cases {
		b, ok := fileBytes[c.File]
		if !ok {
			var err error
			b, err = os.ReadFile(filepath.Join(testdataDir, c.File))
			if err != nil {
				t.Fatalf("read %s: %v", c.File, err)
			}
			fileBytes[c.File] = b
		}
		if c.Span.Start < 0 || c.Span.End > len(b) || c.Span.Start > c.Span.End {
			t.Fatalf("case %s: span %+v out of range for %d bytes", c.ID, c.Span, len(b))
		}
		got := string(b[c.Span.Start:c.Span.End])
		if got != c.Body {
			t.Errorf("case %s: span bytes != Body\nspan: %q\nbody: %q", c.ID, got, c.Body)
		}
	}
}

func TestUnicodeBeforeTest(t *testing.T) {
	res := extractAll(t, testdataDir, 24000)
	tc := caseByID(t, res, "go:unicode_test.go:TestUnicodeSecond")
	b, err := os.ReadFile(filepath.Join(testdataDir, "unicode_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b[tc.Span.Start:tc.Span.End])
	if got != tc.Body {
		t.Errorf("span mismatch after multi-byte content: got %q want %q", got, tc.Body)
	}
	if !strings.Contains(tc.Body, "func TestUnicodeSecond") {
		t.Errorf("Body = %q, missing func signature", tc.Body)
	}

	first := caseByID(t, res, "go:unicode_test.go:TestUnicodeFirst")
	if !strings.Contains(first.Body, "héllo") {
		t.Errorf("TestUnicodeFirst body missing unicode content: %q", first.Body)
	}
}

func TestLiteralSubtests(t *testing.T) {
	res := extractAll(t, testdataDir, 24000)

	for _, c := range res.Cases {
		if c.ID == "go:calc_test.go:TestParse" {
			t.Fatalf("container TestParse must not be emitted as its own case")
		}
	}

	quoted := caseByID(t, res, "go:calc_test.go:TestParse/quoted_value")
	empty := caseByID(t, res, "go:calc_test.go:TestParse/empty")

	if quoted.Parent != "go:calc_test.go:TestParse" {
		t.Errorf("quoted.Parent = %q, want go:calc_test.go:TestParse", quoted.Parent)
	}
	if empty.Parent != "go:calc_test.go:TestParse" {
		t.Errorf("empty.Parent = %q, want go:calc_test.go:TestParse", empty.Parent)
	}
	if !strings.Contains(quoted.Context, "prepare()") {
		t.Errorf("quoted.Context = %q, want it to hold the parent's setup", quoted.Context)
	}
	if !strings.Contains(empty.Context, "prepare()") {
		t.Errorf("empty.Context = %q, want it to hold the parent's setup", empty.Context)
	}
	if !strings.Contains(quoted.Body, `Parse("a")`) {
		t.Errorf("quoted.Body = %q, want the quoted-value subtest body", quoted.Body)
	}
	if !strings.Contains(empty.Body, `Parse("")`) {
		t.Errorf("empty.Body = %q, want the empty subtest body", empty.Body)
	}
}

func TestLoopSubtest(t *testing.T) {
	res := extractAll(t, testdataDir, 24000)
	tc := caseByID(t, res, "go:table_test.go:TestTable/[tc.name]")
	if tc.Parent != "go:table_test.go:TestTable" {
		t.Errorf("Parent = %q, want go:table_test.go:TestTable", tc.Parent)
	}
	count := 0
	for _, c := range res.Cases {
		if strings.HasPrefix(c.ID, "go:table_test.go:TestTable/") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly one TestTable subtest case (loop generates one static case), got %d", count)
	}
	for _, c := range res.Cases {
		if c.ID == "go:table_test.go:TestTable" {
			t.Fatalf("container TestTable must not be emitted as its own case")
		}
	}
}

func TestUnparseableFileSkipped(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module brokenmod\n\ngo 1.21\n")
	write("ok_test.go", "package brokenmod\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
	// Deliberately invalid syntax; generated here rather than checked in so
	// gofmt/go vet over the whole repo don't choke on it.
	write("broken_test.go", "package brokenmod\n\nfunc TestBroken(t *testing.T) {\n\tif true {\n")

	ex := New()
	res, err := ex.Extract(dir, []string{"ok_test.go", "broken_test.go"}, 24000)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].File != "broken_test.go" {
		t.Fatalf("Skipped = %+v, want one entry for broken_test.go", res.Skipped)
	}
	if !strings.Contains(strings.ToLower(res.Skipped[0].Reason), "parse") {
		t.Errorf("Reason = %q, want it to mention parsing", res.Skipped[0].Reason)
	}
	found := false
	for _, c := range res.Cases {
		if c.ID == "go:ok_test.go:TestOK" {
			found = true
		}
	}
	if !found {
		t.Errorf("ok_test.go should still be extracted despite broken_test.go failing to parse")
	}
}

func TestTruncation(t *testing.T) {
	res := extractAll(t, testdataDir, 10)
	tc := caseByID(t, res, "go:calc_test.go:TestAdd")
	if !tc.Truncated {
		t.Errorf("want Truncated true with maxContext=10")
	}
	if len(tc.Callees) != 0 {
		t.Errorf("want callees cut with maxContext=10, got %+v", tc.Callees)
	}
}
