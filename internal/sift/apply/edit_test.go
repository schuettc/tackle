package apply

import (
	"strings"
	"testing"
)

const doc = `# Repo

## Git

- Never push to main.
- Keep commits small.

## Tests

- Run the tests.
`

func TestFindPassage(t *testing.T) {
	lines := split(doc)
	for name, c := range map[string]struct {
		passage string
		start   int
		want    int
		wantErr string
	}{
		"at its line":          {"- Never push to main.", 5, 4, ""},
		"moved since audit":    {"- Never push to main.", 2, 4, ""},
		"two lines":            {"- Never push to main.\n- Keep commits small.", 5, 4, ""},
		"gone":                 {"- Never force-push.", 5, -1, "not found"},
		"whitespace different": {"-  Never push to main.", 5, -1, "not found"},
	} {
		got, err := find(lines, c.passage, c.start)
		if got != c.want || (c.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
			t.Errorf("%s: %d %v", name, got, err)
		}
	}
	twice := split("- a\n- b\n- a\n")
	if i, err := find(twice, "- a", 3); i != 2 || err != nil {
		t.Errorf("at its own line among repeats: %d %v", i, err)
	}
	if _, err := find(twice, "- a", 2); err == nil || !strings.Contains(err.Error(), "2 places") {
		t.Errorf("ambiguous: %v", err)
	}
}

func TestEditFile(t *testing.T) {
	f := &fileEdit{lines: split(doc)}
	if err := f.replace(op{row: "a", passage: "- Never push to main.", start: 5}, "- Push to a branch and open a pull request."); err != nil {
		t.Fatal(err)
	}
	if err := f.replace(op{row: "b", passage: "- Run the tests.", start: 10}, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.replace(op{row: "c", passage: "- Never push to main.\n- Keep commits small.", start: 5}, ""); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("overlap: %v", err)
	}
	f.insert("Tests", "- Use the shared runner.")
	f.insert("Release", "- Tag from main.")
	want := `# Repo

## Git

- Push to a branch and open a pull request.
- Keep commits small.

## Tests

- Use the shared runner.

## Release

- Tag from main.
`
	if got := f.String(); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A section ends at the next heading of its level or higher, not at a
// subsection.
func TestInsertAtTheEndOfASectionWithSubsections(t *testing.T) {
	f := &fileEdit{lines: split("# A\n\n## B\n\n- b\n\n### B1\n\n- b1\n\n## C\n\n- c\n")}
	f.insert("b", "- new")
	want := "# A\n\n## B\n\n- b\n\n### B1\n\n- b1\n- new\n\n## C\n\n- c\n"
	if got := f.String(); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	g := &fileEdit{lines: split("")}
	g.insert("", "- first")
	if got := g.String(); got != "- first\n" {
		t.Fatalf("empty file: %q", got)
	}
}

func TestParseDestination(t *testing.T) {
	for in, want := range map[string][2]string{
		"CLAUDE.md#Git":          {"CLAUDE.md", "Git"},
		"docs/a.md # Releasing ": {"docs/a.md", "Releasing"},
		"AGENTS.md":              {"AGENTS.md", ""},
		"CLAUDE.md § Tests":      {"CLAUDE.md", "Tests"},
	} {
		p, s := parseDestination(in)
		if p != want[0] || s != want[1] {
			t.Errorf("%q: %q %q", in, p, s)
		}
	}
}
