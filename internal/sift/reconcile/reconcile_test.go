package reconcile

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

func at(id, path, passage string) row.Row {
	return row.Row{ID: id, Check: "negative-rule", Passage: passage, Source: row.Source{File: "/w/app/" + path, Repo: "/w/app", Path: path, Start: 5, End: 5},
		Decision: &row.Decision{Action: "accept", Sent: true}}
}

// compare runs Compare on a diff, with each file at the branch made of its
// added lines, each at its line in the new file (from the hunk header), and
// "(unchanged)" on every other line.
func compare(rs []row.Row, d string) Report {
	diff := Parse(d)
	read := func(path string) (string, bool) {
		for _, f := range diff {
			if f.Path != path || f.Deleted {
				continue
			}
			var lines []string
			for _, h := range f.Hunks {
				for i, l := range h.Added {
					for len(lines) < h.NewStart+i {
						lines = append(lines, "(unchanged)")
					}
					lines[h.NewStart+i-1] = l
				}
			}
			return strings.Join(lines, "\n") + "\n", true
		}
		return "", false
	}
	return Compare(rs, diff, read)
}

func with(r row.Row, verdict, dest, text string) row.Row {
	r.Verdict, r.Destination, r.Text = verdict, dest, text
	return r
}

const diffOK = `diff --git a/CLAUDE.md b/CLAUDE.md
index 1111111..2222222 100644
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -5,2 +5 @@
-- Never push to main.
-- Never force-push.
+- Push to a branch and open a pull request.
@@ -10 +8,0 @@
-- Run the slow suite before a release.
diff --git a/docs/other.md b/docs/other.md
index 3333333..4444444 100644
--- a/docs/other.md
+++ b/docs/other.md
@@ -5,0 +6 @@
+- Run the slow suite before a release (app).
`

func rowsOK() []row.Row {
	return []row.Row{
		with(at("neg1", "CLAUDE.md", "- Never push to main."), "rewrite", "", "- Push to a branch and open a pull request."),
		with(at("neg2", "CLAUDE.md", "- Never force-push."), "delete", "", ""),
		with(at("slow", "CLAUDE.md", "- Run the slow suite before a release."), "move", "docs/other.md#Notes", "- Run the slow suite before a release (app)."),
		with(at("judged", "CLAUDE.md", "## Tests"), "delete", "", ""),
	}
}

func TestEveryApprovedRowIsInTheDiff(t *testing.T) {
	rs := rowsOK()
	rs[3].Decision = &row.Decision{Action: "reject", Sent: true}
	got := compare(rs, diffOK)
	if len(got.Rows) != 3 || len(got.Extra) != 0 || got.Problems() != 0 {
		t.Fatalf("%+v", got)
	}
	for _, r := range got.Rows {
		if r.State != "ok" {
			t.Errorf("%+v", r)
		}
	}
}

func TestMissing(t *testing.T) {
	got := compare(rowsOK(), diffOK)
	var judged *RowResult
	for i := range got.Rows {
		if got.Rows[i].Row == "judged" {
			judged = &got.Rows[i]
		}
	}
	if judged == nil || judged.State != "missing" || got.Problems() != 1 {
		t.Fatalf("%+v", got)
	}
}

// The writer kept the meaning but not the approved words.
func TestNarrowed(t *testing.T) {
	d := strings.Replace(diffOK, "+- Push to a branch and open a pull request.", "+- Push to a branch.", 1)
	got := compare(rowsOK()[:3], d)
	if got.Rows[0].State != "narrowed" || !strings.Contains(got.Rows[0].Detail, "not verbatim") || got.Problems() != 1 {
		t.Fatalf("%+v", got.Rows)
	}
	// A passage only partly removed is narrowed too.
	two := with(at("both", "CLAUDE.md", "- Never push to main.\n- Never force-push."), "delete", "", "")
	d2 := strings.Replace(diffOK, "-- Never force-push.\n", "", 1)
	got = compare([]row.Row{two}, d2)
	if got.Rows[0].State != "narrowed" {
		t.Fatalf("%+v", got.Rows)
	}
}

func TestExtraHunks(t *testing.T) {
	d := diffOK + `diff --git a/README.md b/README.md
index 5555555..6666666 100644
--- a/README.md
+++ b/README.md
@@ -1 +1 @@
-# old
+# new
`
	d = strings.Replace(d, "+- Run the slow suite before a release (app).\n", "+- Run the slow suite before a release (app).\n+- Something nobody approved.\n", 1)
	got := compare(rowsOK()[:3], d)
	if len(got.Extra) != 2 || got.Extra[0].File != "README.md" || got.Extra[1].File != "docs/other.md" ||
		!strings.Contains(strings.Join(got.Extra[1].Lines, "\n"), "nobody approved") {
		t.Fatalf("extra %+v", got.Extra)
	}
}

func TestParse(t *testing.T) {
	fs := Parse(diffOK + "diff --git a/old.md b/old.md\ndeleted file mode 100644\nindex 7..0\n--- a/old.md\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n")
	if len(fs) != 3 || fs[0].Path != "CLAUDE.md" || len(fs[0].Hunks) != 2 || len(fs[0].Hunks[0].Removed) != 2 || fs[0].Hunks[0].Header != "@@ -5,2 +5 @@" || fs[0].Hunks[0].NewStart != 5 || fs[1].Hunks[0].NewStart != 6 {
		t.Fatalf("%+v", fs)
	}
	if fs[2].Path != "old.md" || !fs[2].Deleted {
		t.Fatalf("deleted file %+v", fs[2])
	}
}
