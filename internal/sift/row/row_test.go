package row

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestRowJSONGolden(t *testing.T) {
	r := Row{
		ID:      ID("/w/repo/CLAUDE.md", "negative-rule", "- Never push to main."),
		Check:   "negative-rule",
		Summary: "a rule phrased as a prohibition",
		Source:  Source{File: "/w/repo/CLAUDE.md", Repo: "/w/repo", Ref: "origin/main", Path: "CLAUDE.md", Start: 4, End: 4},
		Passage: "- Never push to main.",
		Evidence: []Fact{
			{Name: "matched", Value: "Never"},
		},
		Verdict:     "rewrite",
		Destination: "CLAUDE.md#Git",
		Text:        "- Push to a branch and open a pull request.",
		Decision:    &Decision{Value: "rewrite", Note: "fine"},
	}
	got, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "row.golden.json")
	if *update {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	var back Row
	if err := json.Unmarshal(got, &back); err != nil || back.ID != r.ID || back.Decision.Note != "fine" {
		t.Fatalf("decode %+v %v", back, err)
	}
}

// A row's id survives whitespace changes and changes when the words do, so a
// muted row stays muted until its passage really changes.
func TestIDStableAcrossWhitespace(t *testing.T) {
	a := ID("/f", "duplicate", "Run the tests  before\tyou push.\n")
	if b := ID("/f", "duplicate", "  Run the tests\nbefore you push."); a != b {
		t.Errorf("whitespace changed the id: %s %s", a, b)
	}
	for name, other := range map[string]string{
		"words": ID("/f", "duplicate", "Run the tests after you push."),
		"file":  ID("/g", "duplicate", "Run the tests before you push."),
		"check": ID("/f", "stale-status", "Run the tests before you push."),
	} {
		if other == a {
			t.Errorf("%s did not change the id", name)
		}
	}
	if len(a) != 16 {
		t.Errorf("id %q", a)
	}
}

func TestValidVerdict(t *testing.T) {
	for _, v := range []string{"keep", "delete", "rewrite", "move", "merge:ab12", "drop:obsolete", "issue", "global", "private"} {
		if !ValidVerdict(v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range []string{"", "Keep", "merge:", "drop:", "cut", "merge"} {
		if ValidVerdict(v) {
			t.Errorf("%q should be invalid", v)
		}
	}
}
