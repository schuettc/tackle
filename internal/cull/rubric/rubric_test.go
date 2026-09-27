package rubric

import (
	"strings"
	"testing"
)

func mustLoad(t *testing.T, name string) Rubric {
	t.Helper()
	r, err := Load(name)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return r
}

func TestLoadEmbedded(t *testing.T) {
	tr := mustLoad(t, DefaultTest)
	if tr.Kind != KindTest || tr.Version != "test-v2" || len(tr.Questions) != 7 {
		t.Errorf("test-v2 = kind %q version %q, %d questions", tr.Kind, tr.Version, len(tr.Questions))
	}
	if tr.Policy != (Policy{Act: 0.6, Review: 0.3}) {
		t.Errorf("test-v2 policy = %+v", tr.Policy)
	}
	gr := mustLoad(t, DefaultGroup)
	if gr.Kind != KindGroup || gr.Version != "group-v2" || len(gr.Questions) != 4 {
		t.Errorf("group-v2 = kind %q version %q, %d questions", gr.Kind, gr.Version, len(gr.Questions))
	}
	if gr.Policy != (Policy{Act: 0.6, Review: 0.3, ExactDuplicate: 0.7}) {
		t.Errorf("group-v2 policy = %+v", gr.Policy)
	}
}

func TestActOption(t *testing.T) {
	if got := mustLoad(t, DefaultTest).ActOption(); got != "cut" {
		t.Errorf("test ActOption = %q", got)
	}
	if got := mustLoad(t, DefaultGroup).ActOption(); got != "consolidate" {
		t.Errorf("group ActOption = %q", got)
	}
}

func TestQuestionsHashIgnoresPolicy(t *testing.T) {
	a, b := mustLoad(t, DefaultTest), mustLoad(t, DefaultTest)
	b.Policy.Act, b.Version = 0.9, "test-v9"
	if a.QuestionsHash() != b.QuestionsHash() {
		t.Error("policy/version change altered QuestionsHash")
	}
	c := mustLoad(t, DefaultTest)
	c.Questions[0].Instructions += " More."
	if a.QuestionsHash() == c.QuestionsHash() {
		t.Error("instruction change did not alter QuestionsHash")
	}
}

const testBase = `
kind = "test"
version = "t"
[[question]]
key = "verdict"
type = "choice"
instructions = "i"
[question.options]
keep = "k"
cut = "c"
review = "r"
[[question]]
key = "mock_only"
type = "noul"
instructions = "i"
`

const groupBase = `
kind = "group"
version = "g"
[[question]]
key = "verdict"
type = "choice"
instructions = "i"
[question.options]
consolidate = "c"
keep_separate = "k"
review = "r"
`

const testPolicy = "\n[policy]\nact = 0.6\nreview = 0.3\n"
const groupPolicy = "\n[policy]\nact = 0.6\nreview = 0.3\nexact_duplicate = 0.7\n"

func TestParseValidBases(t *testing.T) {
	if _, err := Parse([]byte(testBase + testPolicy)); err != nil {
		t.Errorf("valid test rubric: %v", err)
	}
	if _, err := Parse([]byte(groupBase + groupPolicy)); err != nil {
		t.Errorf("valid group rubric: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"missing kind", strings.Replace(testBase, `kind = "test"`, "", 1) + testPolicy, "kind"},
		{"bad kind", strings.Replace(testBase, `kind = "test"`, `kind = "suite"`, 1) + testPolicy, "kind"},
		{"test verdict options", strings.Replace(testBase, "review = \"r\"\n", "", 1) + testPolicy, "verdict options"},
		{"group verdict options", strings.Replace(groupBase, "keep_separate = \"k\"\n", "", 1) + groupPolicy, "verdict options"},
		{"act out of range", testBase + strings.Replace(testPolicy, "act = 0.6", "act = 1.2", 1), "act"},
		{"review not below act", testBase + strings.Replace(testPolicy, "review = 0.3", "review = 0.7", 1), "review"},
		{"exact_duplicate on test", testBase + testPolicy + "exact_duplicate = 0.7\n", "exact_duplicate"},
		{"exact_duplicate missing on group", groupBase + testPolicy, "exact_duplicate"},
		{"missing policy", testBase, "policy"},
		{"missing act", testBase + "\n[policy]\nreview = 0.3\n", "act"},
		{"duplicate key", testBase + "[[question]]\nkey = \"mock_only\"\ntype = \"noul\"\ninstructions = \"i\"\n" + testPolicy, "duplicate question"},
		{"unknown key", testBase + testPolicy + "bogus = 1\n", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.doc))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want containing %q", err, c.want)
			}
		})
	}
}
