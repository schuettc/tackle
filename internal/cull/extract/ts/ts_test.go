package ts

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

// requireTS skips the test unless node is on PATH and a typescript package
// can be found ($CULL_TS or a findable node_modules/typescript). No
// machine-specific path is baked in here; run with CULL_TS set to exercise
// this test for real.
func requireTS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found in PATH, skipping")
	}
	if _, ok := findTypescript(testdataDir); !ok {
		t.Skip("typescript package not found ($CULL_TS unset and none found by walking up), skipping")
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
		"test/calc.test.ts":  true,
		"test/e2e.spec.ts":   true,
		"test/calc.test.tsx": true,
		"test/e2e.spec.tsx":  true,
		"src/calc.ts":        false,
		"test/helpers.ts":    false,
		"test/calc.test.js":  false,
	}
	for p, w := range want {
		if got := ex.Match(p); got != w {
			t.Errorf("Match(%q) = %v, want %v", p, got, w)
		}
	}
	if ex.Lang() != "typescript" {
		t.Errorf("Lang() = %q, want typescript", ex.Lang())
	}
}

func TestNestedDescribe(t *testing.T) {
	requireTS(t)
	res := extractAll(t, testdataDir, 24000)

	// The describe blocks themselves must not be emitted as cases.
	for _, id := range []string{
		"ts:test/calc.test.ts:multiply",
		"ts:test/calc.test.ts:multiply > nested edge cases",
	} {
		for _, c := range res.Cases {
			if c.ID == id {
				t.Fatalf("container %q must not be emitted as its own case", id)
			}
		}
	}

	top := caseByID(t, res, "ts:test/calc.test.ts:adds two numbers")
	if top.Parent != "" {
		t.Errorf("top.Parent = %q, want empty for a top-level test", top.Parent)
	}
	if top.Framework != "node:test" {
		t.Errorf("top.Framework = %q, want node:test", top.Framework)
	}
	if top.Lang != "typescript" {
		t.Errorf("top.Lang = %q, want typescript", top.Lang)
	}

	m1 := caseByID(t, res, "ts:test/calc.test.ts:multiply > multiplies positive numbers")
	if m1.Parent != "ts:test/calc.test.ts:multiply" {
		t.Errorf("m1.Parent = %q, want ts:test/calc.test.ts:multiply", m1.Parent)
	}

	m2 := caseByID(t, res, "ts:test/calc.test.ts:multiply > nested edge cases > multiplies by zero")
	if m2.Parent != "ts:test/calc.test.ts:multiply > nested edge cases" {
		t.Errorf("m2.Parent = %q, want ts:test/calc.test.ts:multiply > nested edge cases", m2.Parent)
	}

	// Same-file helper used only by the "uses a helper" test becomes
	// context; the import used by "adds two numbers" becomes a callee.
	if len(top.Callees) != 1 || top.Callees[0].Symbol != "add" {
		t.Fatalf("top.Callees = %+v, want exactly [add]", top.Callees)
	}
	if top.Callees[0].File != "src/calc.ts" {
		t.Errorf("top.Callees[0].File = %q, want src/calc.ts", top.Callees[0].File)
	}

	helperTest := caseByID(t, res, "ts:test/calc.test.ts:uses a helper")
	if !strings.Contains(helperTest.Context, "function helperValue") {
		t.Errorf("helperTest.Context = %q, want it to include helperValue's source", helperTest.Context)
	}

	if top.Hash != cases.HashBody(top.Body) {
		t.Errorf("top.Hash %q != HashBody(Body)", top.Hash)
	}
}

func TestPlaywrightSpec(t *testing.T) {
	requireTS(t)
	res := extractAll(t, testdataDir, 24000)

	nested := caseByID(t, res, "ts:test/e2e.spec.ts:home page > loads the title")
	if nested.Framework != "playwright" {
		t.Errorf("nested.Framework = %q, want playwright", nested.Framework)
	}
	if nested.Parent != "ts:test/e2e.spec.ts:home page" {
		t.Errorf("nested.Parent = %q, want ts:test/e2e.spec.ts:home page", nested.Parent)
	}

	standalone := caseByID(t, res, "ts:test/e2e.spec.ts:standalone check")
	if standalone.Parent != "" {
		t.Errorf("standalone.Parent = %q, want empty", standalone.Parent)
	}
	if !strings.Contains(standalone.Body, "async ({ page })") {
		t.Errorf("standalone.Body = %q, want it to include the async page fixture arg", standalone.Body)
	}
}

func TestSpansExactWithEmoji(t *testing.T) {
	requireTS(t)
	res := extractAll(t, testdataDir, 24000)
	b, err := os.ReadFile(filepath.Join(testdataDir, "test/emoji.test.ts"))
	if err != nil {
		t.Fatal(err)
	}

	tc := caseByID(t, res, "ts:test/emoji.test.ts:counts emoji length correctly")
	if got := string(b[tc.Span.Start:tc.Span.End]); got != tc.Body {
		t.Errorf("span bytes != Body\nspan: %q\nbody: %q", got, tc.Body)
	}
	if !strings.HasPrefix(tc.Body, `test("counts emoji length correctly"`) {
		t.Errorf("Body = %q, missing expected prefix", tc.Body)
	}
}

func TestPlainTestParity(t *testing.T) {
	requireTS(t)
	res := extractAll(t, testdataDir, 24000)
	tc := caseByID(t, res, "ts:test/comment.test.ts:uses add with a leading comment")

	wantBody := "// a comment right before the test\ntest(\"uses add with a leading comment\", () => {\n  expect(add(1, 1)).toBe(2);\n})"
	if tc.Body != wantBody {
		t.Errorf("Body = %q, want %q", tc.Body, wantBody)
	}
	if tc.Context != "" {
		t.Errorf("Context = %q, want empty", tc.Context)
	}
	if len(tc.Callees) != 1 || tc.Callees[0].Symbol != "add" {
		t.Fatalf("Callees = %+v, want exactly [add]", tc.Callees)
	}

	b, err := os.ReadFile(filepath.Join(testdataDir, "test/comment.test.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b[tc.Span.Start:tc.Span.End]); got != tc.Body {
		t.Errorf("span bytes != Body\nspan: %q\nbody: %q", got, tc.Body)
	}
}

func TestBrokenFileSkipped(t *testing.T) {
	requireTS(t)
	res := extractAll(t, testdataDir, 24000)

	var skip *extract.Skipped
	for i := range res.Skipped {
		if res.Skipped[i].File == "test/broken.test.ts" {
			skip = &res.Skipped[i]
		}
	}
	if skip == nil {
		t.Fatalf("Skipped = %+v, want an entry for test/broken.test.ts", res.Skipped)
	}
	if !strings.Contains(strings.ToLower(skip.Reason), "parse") {
		t.Errorf("Reason = %q, want it to mention parsing", skip.Reason)
	}

	found := false
	for _, c := range res.Cases {
		if c.ID == "ts:test/calc.test.ts:adds two numbers" {
			found = true
		}
	}
	if !found {
		t.Errorf("calc.test.ts should still be extracted despite broken.test.ts failing to parse")
	}
}

func TestTsTidyDropsUnusedSpecifier(t *testing.T) {
	requireTS(t)
	src := "import { add, sub } from \"./calc\";\n\ntest(\"add\", () => {\n  expect(add(1, 2)).toBe(3);\n});\n"
	out, removed, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := "import { add } from \"./calc\";\n\ntest(\"add\", () => {\n  expect(add(1, 2)).toBe(3);\n});\n"
	if string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if len(removed) != 1 || removed[0] != `"./calc".sub` {
		t.Errorf("removed = %v, want [\"./calc\".sub]", removed)
	}
}

func TestTsTidyKeepsTypeOnlyUse(t *testing.T) {
	requireTS(t)
	src := "import { Calc } from \"./calc\";\n\nfunction make(): Calc {\n  return {} as Calc;\n}\n\ntest(\"x\", () => {\n  make();\n});\n"
	out, removed, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("out = %q, want unchanged %q", out, src)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none", removed)
	}
}

func TestTsTidyKeepsSideEffectImport(t *testing.T) {
	requireTS(t)
	src := "import \"./setup\";\n\ntest(\"x\", () => {\n  expect(1).toBe(1);\n});\n"
	out, removed, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("out = %q, want unchanged %q", out, src)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none", removed)
	}
}

func TestTsTidySkipsImportWithTrailingComment(t *testing.T) {
	requireTS(t)
	// Fully unused, with a trailing comment.
	src := "import \"./setup\"; // side effects only\n\ntest(\"x\", () => {\n  expect(1).toBe(1);\n});\n"
	out, removed, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("out = %q, want unchanged %q", out, src)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none", removed)
	}

	// Partially unused, with a trailing comment.
	src2 := "import { add, sub } from \"./calc\"; // math ops\n\ntest(\"add\", () => {\n  expect(add(1, 2)).toBe(3);\n});\n"
	out2, removed2, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src2))
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != src2 {
		t.Errorf("out = %q, want unchanged %q", out2, src2)
	}
	if len(removed2) != 0 {
		t.Errorf("removed = %v, want none", removed2)
	}
}

func TestTsTidyRevertsOnPostCheckFailure(t *testing.T) {
	requireTS(t)
	t.Setenv("CULL_TIDY_TEST_FORCE_BROKEN", "1")
	src := "import { add, sub } from \"./calc\";\n\ntest(\"add\", () => {\n  expect(add(1, 2)).toBe(3);\n});\n"
	out, removed, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("out = %q, want unchanged original %q", out, src)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none (post-check failure must not report removals)", removed)
	}
}

func TestNoTypescriptSkips(t *testing.T) {
	t.Setenv("CULL_TS", "")
	root := t.TempDir()

	ex := New()
	relpaths := []string{"test/calc.test.ts"}
	res, err := ex.Extract(root, relpaths, 24000)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(res.Cases) != 0 {
		t.Errorf("Cases = %+v, want none (typescript unavailable)", res.Cases)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("Skipped = %+v, want one entry", res.Skipped)
	}
	want := "typescript not found under " + root
	if res.Skipped[0].Reason != want {
		t.Errorf("Reason = %q, want %q", res.Skipped[0].Reason, want)
	}
}

// TestTsTidySkipsImportSharingALine: two imports on one line; deleting
// the unused one's line would delete the used one too (I-3).
func TestTsTidySkipsImportSharingALine(t *testing.T) {
	requireTS(t)
	src := "import { a } from \"./a\"; import { b } from \"./b\";\n\ntest(\"x\", () => {\n  b();\n});\n"
	out, removed, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src || len(removed) != 0 {
		t.Errorf("out = %q removed = %v, want unchanged", out, removed)
	}
}

// TestTsTidyRefusesNonUTF8: a file that is not valid UTF-8 is returned
// byte-for-byte (M-2).
func TestTsTidyRefusesNonUTF8(t *testing.T) {
	requireTS(t)
	src := "import { a } from \"./a\";\n\ntest(\"x\", () => {\n  expect(\"\xe9\").toBe(\"\xe9\");\n});\n"
	out, removed, err := Tidy(t.TempDir(), "test/calc.test.ts", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src || len(removed) != 0 {
		t.Errorf("out = %q removed = %v, want unchanged", out, removed)
	}
}
