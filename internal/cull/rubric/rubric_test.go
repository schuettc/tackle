package rubric

import (
	"strings"
	"testing"
)

func mustLoad(t *testing.T) Rubric {
	t.Helper()
	r, err := Load("v1")
	if err != nil {
		t.Fatalf("Load(v1): %v", err)
	}
	return r
}

func TestLoadV1(t *testing.T) {
	r := mustLoad(t)
	if r.Version != "v1" {
		t.Errorf("Version = %q, want v1", r.Version)
	}
	if len(r.Questions) != 6 {
		t.Errorf("len(Questions) = %d, want 6", len(r.Questions))
	}
	if r.Policy.HazardThreshold != 0.7 {
		t.Errorf("HazardThreshold = %v, want 0.7", r.Policy.HazardThreshold)
	}
	if len(r.Policy.Hazards) != 4 {
		t.Errorf("len(Hazards) = %d, want 4", len(r.Policy.Hazards))
	}
}

func TestAPIQuestionsShape(t *testing.T) {
	q := mustLoad(t).APIQuestions()

	verdict := q["verdict"].(map[string]any)
	if verdict["type"] != "choice" {
		t.Errorf("verdict type = %v", verdict["type"])
	}
	opts := verdict["criteria"].(map[string]string)
	for _, k := range []string{"keep", "cut", "review"} {
		if opts[k] == "" {
			t.Errorf("verdict criteria missing %q", k)
		}
	}
	if len(opts) != 3 {
		t.Errorf("verdict criteria has %d options, want 3", len(opts))
	}

	levels := q["regression_value"].(map[string]any)["criteria"].([]string)
	if len(levels) != 4 {
		t.Errorf("regression_value levels = %d, want 4", len(levels))
	}

	mock := q["mock_only"].(map[string]any)
	crit := mock["criteria"].(map[string]string)
	if mock["type"] != "noul" || crit["true"] == "" || crit["false"] == "" {
		t.Errorf("mock_only = %#v", mock)
	}
}

func TestQuestionsHashIgnoresPolicy(t *testing.T) {
	a := mustLoad(t)
	b := mustLoad(t)
	b.Policy.HazardThreshold = 0.9
	b.Version = "v9"
	if a.QuestionsHash() != b.QuestionsHash() {
		t.Error("policy/version change altered QuestionsHash")
	}
	c := mustLoad(t)
	c.Questions[0].Instructions += " More."
	if a.QuestionsHash() == c.QuestionsHash() {
		t.Error("instruction change did not alter QuestionsHash")
	}
	if !strings.HasPrefix(a.QuestionsHash(), "sha256:") {
		t.Errorf("hash %q lacks sha256: prefix", a.QuestionsHash())
	}
}

const validBase = `
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
key = "regression_value"
type = "score"
instructions = "i"
levels = ["a", "b", "c", "d"]
[[question]]
key = "mock_only"
type = "noul"
instructions = "i"
yes = "y"
no = "n"
[[question]]
key = "pick"
type = "choice"
instructions = "i"
[question.options]
x = "x"
y = "y"
`

const validPolicy = `
[policy]
min_verdict_confidence = 0.5
keep_regression_value = 2.0
cut_max_regression_value = 1.0
hazard_threshold = 0.7
hazards = ["mock_only"]
`

func TestParseValidBase(t *testing.T) {
	if _, err := Parse([]byte(validBase + validPolicy)); err != nil {
		t.Fatalf("valid rubric rejected: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name, doc, want string
	}{
		{"missing version", strings.Replace(validBase, `version = "t"`, "", 1) + validPolicy, "version"},
		{"duplicate key", validBase + "[[question]]\nkey = \"mock_only\"\ntype = \"noul\"\ninstructions = \"i\"\n" + validPolicy, "duplicate question"},
		{"unknown type", validBase + "[[question]]\nkey = \"z\"\ntype = \"rank\"\ninstructions = \"i\"\n" + validPolicy, "unknown type"},
		{"verdict options", strings.Replace(validBase, "review = \"r\"\n", "", 1) + validPolicy, "verdict options"},
		{"levels", strings.Replace(validBase, `levels = ["a", "b", "c", "d"]`, `levels = ["a"]`, 1) + validPolicy, "levels"},
		{"hazard not noul", validBase + strings.Replace(validPolicy, `hazards = ["mock_only"]`, `hazards = ["pick"]`, 1), "hazard"},
		{"hazard threshold", validBase + strings.Replace(validPolicy, "hazard_threshold = 0.7", "hazard_threshold = 1.5", 1), "hazard_threshold"},
		{"keep regression", validBase + strings.Replace(validPolicy, "keep_regression_value = 2.0", "keep_regression_value = 5.0", 1), "keep_regression_value"},
		{"unknown key", validBase + validPolicy + "\nbogus = 1\n", "unknown"},
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

func TestParseRequiresPolicy(t *testing.T) {
	if _, err := Parse([]byte(validBase)); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("rubric without [policy] accepted: %v", err)
	}
	partial := strings.Replace(validPolicy, "hazard_threshold = 0.7\n", "", 1)
	if _, err := Parse([]byte(validBase + partial)); err == nil || !strings.Contains(err.Error(), "hazard_threshold") {
		t.Fatalf("rubric missing hazard_threshold accepted: %v", err)
	}
	none := strings.Replace(validPolicy, `hazards = ["mock_only"]`, `hazards = []`, 1)
	if _, err := Parse([]byte(validBase + none)); err == nil || !strings.Contains(err.Error(), "hazards") {
		t.Fatalf("rubric with no hazards accepted: %v", err)
	}
}
