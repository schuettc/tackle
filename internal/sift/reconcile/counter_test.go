package reconcile

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

// The reviewer's counterexamples: each change nobody approved is reported.

// 1. The approved replacement plus one extra added line in the same hunk:
// the extra line is extra.
func TestExtraLineInTheRewritesHunk(t *testing.T) {
	d := strings.Replace(diffOK, "+- Push to a branch and open a pull request.\n",
		"+- Push to a branch and open a pull request.\n+- Merge on Fridays without a review.\n", 1)
	got := compare(rowsOK()[:3], d)
	if !hasExtra(got, "+- Merge on Fridays without a review.") {
		t.Fatalf("extra %+v rows %+v", got.Extra, got.Rows)
	}
}

// 2. A second identical added line elsewhere in the file: one copy is the
// approved text, the other is extra.
func TestASecondIdenticalAddedLineIsExtra(t *testing.T) {
	d := strings.Replace(diffOK, "diff --git a/docs/other.md", `@@ -20,0 +19 @@
+- Push to a branch and open a pull request.
diff --git a/docs/other.md`, 1)
	got := compare(rowsOK()[:3], d)
	if len(got.Extra) != 1 || got.Extra[0].File != "CLAUDE.md" || got.Extra[0].Header != "@@ -20,0 +19 @@" {
		t.Fatalf("extra %+v", got.Extra)
	}
}

// 3. Approved adjacent lines that end up separated by unchanged content are
// narrowed: the text is not contiguous in the file at the branch.
func TestApprovedLinesSeparatedAreNarrowed(t *testing.T) {
	rw := with(at("neg1", "CLAUDE.md", "- Never push to main."), "rewrite", "", "- Push to a branch.\n- Open a pull request.")
	d := `diff --git a/CLAUDE.md b/CLAUDE.md
index 1111111..2222222 100644
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -5 +5 @@
-- Never push to main.
+- Push to a branch.
@@ -7,0 +8 @@
+- Open a pull request.
`
	got := compare([]row.Row{rw}, d)
	if got.Rows[0].State != "narrowed" {
		t.Fatalf("%+v", got.Rows)
	}
}

func hasExtra(r Report, line string) bool {
	for _, e := range r.Extra {
		for _, l := range e.Lines {
			if l == line {
				return true
			}
		}
	}
	return false
}

// files is a read over fixed file contents at the branch.
func files(m map[string]string) func(string) (string, bool) {
	return func(p string) (string, bool) {
		s, ok := m[p]
		return s, ok
	}
}

// 4. The approved replacement is already in the file, unchanged, elsewhere,
// and the branch only deletes the passage: the text must come from lines
// the diff adds, so the rewrite is not ok.
func TestUnchangedTextElsewhereIsNotTheRewrite(t *testing.T) {
	rw := with(at("neg1", "CLAUDE.md", "- Never push to main."), "rewrite", "", "- Push to a branch.")
	d := `diff --git a/CLAUDE.md b/CLAUDE.md
index 1111111..2222222 100644
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -5 +4,0 @@
-- Never push to main.
`
	got := Compare([]row.Row{rw}, Parse(d), files(map[string]string{
		"CLAUDE.md": "# App\n\n## Git\n\n\n## Notes\n\n- Push to a branch.\n",
	}))
	if got.Rows[0].State == "ok" {
		t.Fatalf("%+v", got.Rows)
	}
}

// 5. The same for a move: the destination already has the approved text,
// and the branch only deletes the passage.
func TestUnchangedTextElsewhereIsNotTheMove(t *testing.T) {
	mv := with(at("slow", "CLAUDE.md", "- Run the slow suite."), "move", "docs/other.md#Notes", "- one")
	d := `diff --git a/CLAUDE.md b/CLAUDE.md
index 1111111..2222222 100644
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -5 +4,0 @@
-- Run the slow suite.
`
	got := Compare([]row.Row{mv}, Parse(d), files(map[string]string{
		"CLAUDE.md":     "# App\n\n## Tests\n\n",
		"docs/other.md": "# Other\n\n## Notes\n\n- one\n",
	}))
	if got.Rows[0].State == "ok" {
		t.Fatalf("%+v", got.Rows)
	}
}
