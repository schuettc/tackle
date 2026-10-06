package rec

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

// A round of two files: g (global, two findings, one certain) and m (a repo
// file, one finding).
func round() (map[string]File, map[string]row.Row) {
	g := NewFile(row.Source{File: "/h/.agent/AGENTS.md"}, "global", 8000, "# G\n\n- Never do x.\n- See `gone.md`.\n- In m, run y.\n")
	g.Rows = []string{"neg", "dead", "mis"}
	m := NewFile(row.Source{File: "/w/m/AGENTS.md", Repo: "/w/m", Ref: "origin/main", Path: "AGENTS.md"}, "repo", 6000, "# M\n\n- Don't skip z.\n")
	m.Rows = []string{"neg2"}
	rows := map[string]row.Row{
		"neg":  {ID: "neg", Check: "stale-status"},
		"dead": {ID: "dead", Check: "dead-path", Certain: true},
		"mis":  {ID: "mis", Check: "misplaced"},
		"neg2": {ID: "neg2", Check: "stale-status"},
	}
	return map[string]File{g.Key: g, m.Key: m}, rows
}

func good(files map[string]File) (Rec, Rec) {
	var g, m File
	for _, f := range files {
		if f.Class == "global" {
			g = f
		} else {
			m = f
		}
	}
	rg := Rec{File: g.Key, Base: g.Base, Content: "# G\n\n- Do x the safe way.\n",
		Findings: []Account{{"neg", "fixed", "rewritten as guidance"}, {"dead", "fixed", "line removed"}, {"mis", "fixed", "moved to m"}},
		Links:    []string{m.Key}, Summary: "Drops a finished migration note, drops a dead path and moves a repo-only rule to m."}
	rm := Rec{File: m.Key, Base: m.Base, Content: "# M\n\n- Run z every time.\n- Run y.\n",
		Findings: []Account{{"neg2", "fixed", "rewritten as guidance"}},
		Links:    []string{g.Key}, Summary: "Takes the rule from the global file."}
	return rg, rm
}

func TestAGoodPairIsValid(t *testing.T) {
	files, rows := round()
	rg, rm := good(files)
	if err := Check(files, rows, nil, []Rec{rg, rm}); err != nil {
		t.Fatal(err)
	}
}

// Each rule propose enforces, one break at a time.
func TestCheckRefuses(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(g, m *Rec)
		want   string
	}{
		{"a stale base", func(g, _ *Rec) { g.Base = Hash("older\n") }, "base"},
		{"a file not in the round", func(g, _ *Rec) { g.File = "nope" }, "not in the round"},
		{"a finding left out", func(g, _ *Rec) { g.Findings = g.Findings[:2] }, "mis"},
		{"a finding of another file", func(g, _ *Rec) { g.Findings = append(g.Findings, Account{"neg2", "fixed", "x"}) }, "neg2"},
		{"a finding twice", func(g, _ *Rec) { g.Findings[1] = g.Findings[0] }, "twice"},
		{"fixed with no how", func(g, _ *Rec) { g.Findings[0].How = " " }, "how"},
		{"kept with no why", func(g, _ *Rec) { g.Findings[0] = Account{"neg", "kept", ""} }, "why"},
		{"an unknown outcome", func(g, _ *Rec) { g.Findings[0].Did = "ignored" }, "fixed or kept"},
		{"a certain finding kept", func(g, _ *Rec) { g.Findings[1] = Account{"dead", "kept", "I like it"} }, "certain"},
		{"a link outside the round", func(g, _ *Rec) { g.Links = []string{"nope"} }, "not in the round"},
		{"a link to itself", func(g, _ *Rec) { g.Links = []string{g.File} }, "itself"},
		{"a link one way", func(_, m *Rec) { m.Links = nil }, "name each other"},
		{"the base unchanged", func(_, m *Rec) { m.Content = "# M\n\n- Don't skip z.\n" }, "same as"},
		{"no summary", func(g, _ *Rec) { g.Summary = "" }, "summary"},
		{"no content", func(g, _ *Rec) { g.Content = "" }, "content"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files, rows := round()
			rg, rm := good(files)
			c.break_(&rg, &rm)
			err := Check(files, rows, nil, []Rec{rg, rm})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error about %q", err, c.want)
			}
		})
	}
}

// The base may stay as it is when every finding is kept, each with why.
func TestEveryFindingKeptMayLeaveTheFile(t *testing.T) {
	files, rows := round()
	_, rm := good(files)
	rm.Content = files[rm.File].Content
	rm.Links = nil
	rm.Findings = []Account{{"neg2", "kept", "it is a hard safety rule"}}
	if err := Check(files, rows, nil, []Rec{rm}); err != nil {
		t.Fatal(err)
	}
}

// A link to a file whose stored recommendation names this one back is
// valid on its own; one whose stored recommendation does not is refused,
// and so is leaving out a stored file that links here.
func TestLinksAgainstStoredRecommendations(t *testing.T) {
	files, rows := round()
	rg, rm := good(files)
	stored := map[string]Rec{rm.File: rm}
	if err := Check(files, rows, stored, []Rec{rg}); err != nil {
		t.Fatalf("against the stored side: %v", err)
	}
	rg.Links = nil
	if err := Check(files, rows, stored, []Rec{rg}); err == nil || !strings.Contains(err.Error(), "name each other") {
		t.Fatalf("a stored link here left out: %v", err)
	}
	// Unlinking both sides in one batch is fine.
	rm.Links = nil
	rm.Findings = []Account{{"neg2", "fixed", "rewritten"}}
	if err := Check(files, rows, stored, []Rec{rg, rm}); err != nil {
		t.Fatalf("both unlinked together: %v", err)
	}
}

func TestOneFileOncePerBatch(t *testing.T) {
	files, rows := round()
	rg, _ := good(files)
	rg.Links = nil
	if err := Check(files, rows, nil, []Rec{rg, rg}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatal(err)
	}
}

// The print changes with the content, the base, the findings, the summary,
// the links, a linked file's content or base, and the edit in force on the
// file or a linked one: the page shows the edit, and an accept keeps it.
// An accept or reject leaves it as it is.
func TestPrint(t *testing.T) {
	files, _ := round()
	rg, rm := good(files)
	p := Print(rg, []Rec{rm}, nil)
	for name, edits := range map[string]map[string]string{
		"an edit":            {rg.File: "# mine\n"},
		"a linked edit":      {rm.File: "# mine\n"},
		"another edit":       {rg.File: "# mine, again\n"},
		"an edit to neither": {"other": "# mine\n"},
	} {
		got := Print(rg, []Rec{rm}, edits)
		if (got == p) != (name == "an edit to neither") {
			t.Errorf("%s: print %s, with none %s", name, got, p)
		}
	}
	if Print(rg, []Rec{rm}, map[string]string{rg.File: "# mine\n"}) == Print(rg, []Rec{rm}, map[string]string{rg.File: "# mine, again\n"}) {
		t.Error("two edits print alike")
	}
	for name, mut := range map[string]func(g, m *Rec){
		"content":        func(g, _ *Rec) { g.Content += "x" },
		"base":           func(g, _ *Rec) { g.Base = Hash("x") },
		"finding":        func(g, _ *Rec) { g.Findings[0].How = "other" },
		"summary":        func(g, _ *Rec) { g.Summary += "." },
		"links":          func(g, _ *Rec) { g.Links = nil },
		"linked content": func(_, m *Rec) { m.Content += "x" },
		"linked base":    func(_, m *Rec) { m.Base = Hash("y") },
	} {
		g, m := rg, rm
		g.Findings = append([]Account{}, rg.Findings...)
		mut(&g, &m)
		if Print(g, []Rec{m}, nil) == p {
			t.Errorf("%s: print unchanged", name)
		}
	}
	if Print(rg, []Rec{rm}, nil) != p {
		t.Error("not stable")
	}
}

func TestGroups(t *testing.T) {
	recs := map[string]Rec{
		"a": {File: "a", Links: []string{"b"}},
		"b": {File: "b", Links: []string{"a", "c"}},
		"c": {File: "c", Links: []string{"b"}},
		"d": {File: "d"},
	}
	if g := Group(recs, "a"); strings.Join(g, ",") != "a,b,c" {
		t.Errorf("a: %v", g)
	}
	if g := Group(recs, "d"); strings.Join(g, ",") != "d" {
		t.Errorf("d: %v", g)
	}
	if g := Group(recs, "x"); strings.Join(g, ",") != "x" {
		t.Errorf("no rec: %v", g)
	}
}

func TestDecisionValidate(t *testing.T) {
	for _, d := range []Decision{{Action: "accept"}, {Action: "reject", Note: "n"}, {Action: "edit", Content: "x\n"}} {
		if err := d.Validate(); err != nil {
			t.Errorf("%+v: %v", d, err)
		}
	}
	for _, d := range []Decision{{Action: "edit"}, {Action: "accept", Content: "x"}, {Action: "keep"}} {
		if err := d.Validate(); err == nil {
			t.Errorf("%+v: valid", d)
		}
	}
}
