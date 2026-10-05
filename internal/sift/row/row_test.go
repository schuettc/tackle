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
		ID:      ID("/w/repo/CLAUDE.md", "stale-status", "- Never push to main."),
		Check:   "stale-status",
		Summary: "status that may be stale",
		Source:  Source{File: "/w/repo/CLAUDE.md", Repo: "/w/repo", Ref: "origin/main", Path: "CLAUDE.md", Start: 4, End: 4},
		Passage: "- Never push to main.",
		Evidence: []Fact{
			{Name: "matched", Value: "Never"},
		},
		Verdict:     "rewrite",
		Destination: "CLAUDE.md#Git",
		Text:        "- Push to a branch and open a pull request.",
		Reason:      "says what to do instead of what to avoid",
		Decision:    &Decision{Action: "edit", Verdict: "rewrite", Text: "- Push to a branch; open a pull request.", Note: "fine"},
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
	if err := json.Unmarshal(got, &back); err != nil || back.ID != r.ID || back.Decision.Note != "fine" || back.Decision.Action != "edit" {
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

// An issue row (backlog intake) carries the issue's title, and its
// destination is the repo the issue goes to.
func TestIssueRowJSONGolden(t *testing.T) {
	r := Row{
		ID:          ID("store/todo", "intake", "fix the flaky probe"),
		Check:       "intake",
		Summary:     "a to-do from the memory store",
		Source:      Source{File: "/m/MEMORY.md", Entry: "todo-3"},
		Passage:     "fix the flaky probe",
		Verdict:     "issue",
		Title:       "Probe is flaky on a cold start",
		Destination: "owner/tool",
		Text:        "The probe times out on a cold start.",
		Decision:    &Decision{Action: "accept"},
	}
	got, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "issue-row.golden.json")
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
}

func TestValidVerdict(t *testing.T) {
	for _, v := range []string{"keep", "delete", "rewrite", "move", "merge:ab12", "drop:obsolete", "issue", "global", "private",
		"close:done", "close:obsolete", "close:tracked:owner/name#12", "close:tracked:name#3", "ask"} {
		if !ValidVerdict(v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range []string{"", "Keep", "merge:", "drop:", "cut", "merge", "close", "close:", "close:later", "close:tracked:", "close:tracked:name", "close:tracked:#3", "ask:x"} {
		if ValidVerdict(v) {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func TestDecisionValidate(t *testing.T) {
	for _, d := range []Decision{
		{Action: "accept"},
		{Action: "reject", Note: "not now"},
		{Action: "edit", Verdict: "close:done"},
		{Action: "edit", Title: "a better title"},
		{Action: "edit", Text: "new text"},
		{Action: "edit", Cleared: []string{"text"}},
		{Action: "edit", Verdict: "issue", Cleared: []string{"title", "text"}},
	} {
		if err := d.Validate(); err != nil {
			t.Errorf("%+v: %v", d, err)
		}
	}
	for _, d := range []Decision{
		{},
		{Action: "approve"},
		{Action: "edit"},
		{Action: "edit", Verdict: "cut"},
		{Action: "accept", Verdict: "keep"},
		{Action: "reject", Text: "x"},
		{Action: "accept", Cleared: []string{"text"}},
		{Action: "edit", Cleared: []string{"verdict"}},
		{Action: "edit", Text: "x", Cleared: []string{"text"}},
		{Action: "edit", Cleared: []string{"text", "text"}},
	} {
		if d.Validate() == nil {
			t.Errorf("%+v: want an error", d)
		}
	}
}

// The first occurrence of a passage keeps the plain id; a later identical
// one (whitespace aside) is told apart by its ordinal, and the same ordinal
// always gives the same id.
func TestNthTellsRepeatsApart(t *testing.T) {
	id := ID("/f", "stale-status", "Never  push.")
	if Nth(id, 0) != id {
		t.Error("the first occurrence must keep the plain id")
	}
	if Nth(id, 1) == id || Nth(id, 1) == Nth(id, 2) {
		t.Error("repeats share an id")
	}
	if Nth(ID("/f", "stale-status", "Never push."), 1) != Nth(id, 1) || len(Nth(id, 1)) != 16 {
		t.Error("an ordinal id is not stable")
	}
}

// Effective is what the user approved of a row: the proposal, an edit's
// changes over it, nothing when it is rejected or undecided. A certain row
// is no exception: it is decided like any other.
func TestEffective(t *testing.T) {
	base := Row{Verdict: "rewrite", Title: "t", Destination: "CLAUDE.md#Git", Text: "new"}
	with := func(r Row, d *Decision) Row { r.Decision = d; return r }
	for name, c := range map[string]struct {
		r    Row
		want Change
		ok   bool
	}{
		"undecided":            {base, Change{}, false},
		"accepted":             {with(base, &Decision{Action: "accept"}), Change{Verdict: "rewrite", Title: "t", Destination: "CLAUDE.md#Git", Text: "new"}, true},
		"edited text":          {with(base, &Decision{Action: "edit", Text: "mine"}), Change{Verdict: "rewrite", Title: "t", Destination: "CLAUDE.md#Git", Text: "mine"}, true},
		"edited verdict":       {with(base, &Decision{Action: "edit", Verdict: "delete"}), Change{Verdict: "delete", Title: "t", Destination: "CLAUDE.md#Git", Text: "new"}, true},
		"rejected":             {with(base, &Decision{Action: "reject"}), Change{}, false},
		"accepted, no verdict": {with(Row{}, &Decision{Action: "accept"}), Change{}, false},
		"certain, undecided":   {Row{Certain: true, Verdict: "delete"}, Change{}, false},
		"certain, accepted":    {with(Row{Certain: true, Verdict: "delete"}, &Decision{Action: "accept"}), Change{Verdict: "delete"}, true},
	} {
		got, ok := c.r.Effective()
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v %v, want %+v %v", name, got, ok, c.want, c.ok)
		}
	}
}

// Print changes with every field that says what applying the row does, and
// with a merge target's source and passage; not with the reason or the
// decision.
func TestPrint(t *testing.T) {
	base := Row{ID: "a", Check: "duplicate", Verdict: "merge:b", Text: "t", Passage: "p", Source: Source{File: "/r/x.md", Repo: "/r", Path: "x.md", Start: 1, End: 1}}
	target := Row{ID: "b", Passage: "q", Source: Source{Path: "y.md", Start: 2}}
	want := base.Print(&target)
	for name, change := range map[string]func(r, t *Row){
		"verdict":        func(r, _ *Row) { r.Verdict = "delete" },
		"title":          func(r, _ *Row) { r.Title = "x" },
		"destination":    func(r, _ *Row) { r.Destination = "d.md#S" },
		"text":           func(r, _ *Row) { r.Text = "u" },
		"path":           func(r, _ *Row) { r.Source.Path = "z.md" },
		"lines":          func(r, _ *Row) { r.Source.Start = 9 },
		"passage":        func(r, _ *Row) { r.Passage = "p2" },
		"target path":    func(_, t *Row) { t.Source.Path = "w.md" },
		"target passage": func(_, t *Row) { t.Passage = "q2" },
	} {
		r, tg := base, target
		change(&r, &tg)
		if r.Print(&tg) == want {
			t.Errorf("%s: print unchanged", name)
		}
	}
	r := base
	r.Reason, r.Decision, r.Fingerprint = "why", &Decision{Action: "accept"}, "x"
	if r.Print(&target) != want {
		t.Error("the reason or decision changed the print")
	}
	if base.Print(nil) == want {
		t.Error("the target is not in the print")
	}
}
