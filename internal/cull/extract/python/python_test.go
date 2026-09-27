package python

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/extract"
)

const testdataDir = "testdata"

func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH, skipping")
	}
}

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
		"tests/test_calc.py":  true,
		"tests/calc_test.py":  true,
		"tests/conftest.py":   false,
		"tests/helpers.py":    false,
		"src/pkg/calc.py":     false,
		"tests/test_calc.txt": false,
	}
	for p, w := range want {
		if got := ex.Match(p); got != w {
			t.Errorf("Match(%q) = %v, want %v", p, got, w)
		}
	}
	if ex.Lang() != "python" {
		t.Errorf("Lang() = %q, want python", ex.Lang())
	}
}

func TestPlainFunctionParity(t *testing.T) {
	requirePython3(t)
	res := extractAll(t, testdataDir, 24000)
	tc := caseByID(t, res, "py:tests/test_calc.py:test_add")

	wantBody := "def test_add():\n    assert add(2, 3) == 5\n"
	if tc.Body != wantBody {
		t.Errorf("Body = %q, want %q", tc.Body, wantBody)
	}
	if tc.Context != "" {
		t.Errorf("Context = %q, want empty (test_add references no same-file helpers or fixtures)", tc.Context)
	}
	if len(tc.Callees) != 1 || tc.Callees[0].Symbol != "add" {
		t.Fatalf("Callees = %+v, want exactly [add]", tc.Callees)
	}
	wantCalleeSrc := "def add(a, b):\n    \"\"\"Return the sum of a and b.\"\"\"\n    return a + b\n"
	if tc.Callees[0].Source != wantCalleeSrc {
		t.Errorf("Callee source = %q, want %q", tc.Callees[0].Source, wantCalleeSrc)
	}
	if tc.Callees[0].File != "src/pkg/calc.py" {
		t.Errorf("Callee file = %q, want src/pkg/calc.py", tc.Callees[0].File)
	}
	if tc.Parent != "" {
		t.Errorf("Parent = %q, want empty for a plain top-level function", tc.Parent)
	}
	if tc.Hash != cases.HashBody(tc.Body) {
		t.Errorf("Hash %q != HashBody(Body)", tc.Hash)
	}
	if tc.Lang != "python" || tc.Framework != "pytest" || tc.File != "tests/test_calc.py" || tc.Name != "test_add" {
		t.Errorf("case metadata wrong: %+v", tc)
	}

	// Context: same-file helper def + conftest fixture, unchanged from
	// Phase 0's rules.
	helper := caseByID(t, res, "py:tests/test_calc.py:test_add_with_helper")
	wantCtx := "def helper_value():\n    return 41\n"
	if helper.Context != wantCtx {
		t.Errorf("test_add_with_helper Context = %q, want %q", helper.Context, wantCtx)
	}
	fixture := caseByID(t, res, "py:tests/test_calc.py:test_fixture_use")
	wantFixtureCtx := "@pytest.fixture\ndef calculator():\n    return {\"value\": 0}\n"
	if fixture.Context != wantFixtureCtx {
		t.Errorf("test_fixture_use Context = %q, want %q", fixture.Context, wantFixtureCtx)
	}
}

func TestClassMethodsHaveParent(t *testing.T) {
	requirePython3(t)
	res := extractAll(t, testdataDir, 24000)

	for _, c := range res.Cases {
		if c.ID == "py:tests/test_calc.py:TestCalculator" {
			t.Fatalf("container class TestCalculator must not be emitted as its own case")
		}
	}
	m1 := caseByID(t, res, "py:tests/test_calc.py:TestCalculator::test_multiply")
	m2 := caseByID(t, res, "py:tests/test_calc.py:TestCalculator::test_multiply_zero")
	wantParent := "py:tests/test_calc.py:TestCalculator"
	if m1.Parent != wantParent {
		t.Errorf("m1.Parent = %q, want %q", m1.Parent, wantParent)
	}
	if m2.Parent != wantParent {
		t.Errorf("m2.Parent = %q, want %q", m2.Parent, wantParent)
	}
	if m1.Name != "test_multiply" {
		t.Errorf("m1.Name = %q, want test_multiply (short, not qualified)", m1.Name)
	}
}

func TestParametrizedIsOneCase(t *testing.T) {
	requirePython3(t)
	res := extractAll(t, testdataDir, 24000)
	count := 0
	for _, c := range res.Cases {
		if c.File == "tests/test_calc.py" && c.Name == "test_add_parametrized" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly one case for the parametrized function, got %d", count)
	}
	tc := caseByID(t, res, "py:tests/test_calc.py:test_add_parametrized")
	if !strings.HasPrefix(tc.Body, "@pytest.mark.parametrize(") {
		t.Errorf("Body = %q, want it to include the parametrize decorator", tc.Body)
	}
}

func TestSpansExactWithUnicode(t *testing.T) {
	requirePython3(t)
	res := extractAll(t, testdataDir, 24000)
	b, err := os.ReadFile(filepath.Join(testdataDir, "tests/test_unicode.py"))
	if err != nil {
		t.Fatal(err)
	}

	first := caseByID(t, res, "py:tests/test_unicode.py:test_unicode_first")
	if got := string(b[first.Span.Start:first.Span.End]); got != first.Body {
		t.Errorf("first: span bytes != Body\nspan: %q\nbody: %q", got, first.Body)
	}
	if !strings.Contains(first.Body, "héllo") {
		t.Errorf("test_unicode_first body missing unicode docstring content: %q", first.Body)
	}

	// The case after multi-byte content: if byte offsets were computed
	// from character counts instead of UTF-8 bytes, this span would be
	// shifted left of where "def test_unicode_second" actually starts.
	second := caseByID(t, res, "py:tests/test_unicode.py:test_unicode_second")
	if got := string(b[second.Span.Start:second.Span.End]); got != second.Body {
		t.Errorf("second: span bytes != Body\nspan: %q\nbody: %q", got, second.Body)
	}
	if !strings.HasPrefix(second.Body, "def test_unicode_second") {
		t.Errorf("second.Body = %q, missing func signature", second.Body)
	}
}

func TestBrokenFileSkipped(t *testing.T) {
	requirePython3(t)
	res := extractAll(t, testdataDir, 24000)

	var skip *extract.Skipped
	for i := range res.Skipped {
		if res.Skipped[i].File == "tests/test_broken.py" {
			skip = &res.Skipped[i]
		}
	}
	if skip == nil {
		t.Fatalf("Skipped = %+v, want an entry for tests/test_broken.py", res.Skipped)
	}
	if !strings.Contains(strings.ToLower(skip.Reason), "parse") {
		t.Errorf("Reason = %q, want it to mention parsing", skip.Reason)
	}
	// Other files must still be extracted despite test_broken.py failing.
	found := false
	for _, c := range res.Cases {
		if c.ID == "py:tests/test_calc.py:test_add" {
			found = true
		}
	}
	if !found {
		t.Errorf("test_calc.py should still be extracted despite test_broken.py failing to parse")
	}
}

func TestNoPython3Skips(t *testing.T) {
	emptyBin := t.TempDir()
	t.Setenv("PATH", emptyBin)

	ex := New()
	relpaths := []string{"tests/test_calc.py", "tests/test_unicode.py", "tests/test_broken.py"}
	res, err := ex.Extract(testdataDir, relpaths, 24000)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Cases) != 0 {
		t.Errorf("Cases = %+v, want none (python3 unavailable)", res.Cases)
	}
	if len(res.Skipped) != len(relpaths) {
		t.Fatalf("Skipped = %+v, want one entry per relpath", res.Skipped)
	}
	for _, s := range res.Skipped {
		if s.Reason != "python3 not found" {
			t.Errorf("Skipped[%s].Reason = %q, want %q", s.File, s.Reason, "python3 not found")
		}
	}
}
