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
