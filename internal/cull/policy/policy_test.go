package policy

import (
	"reflect"
	"testing"

	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

var pol = rubric.Policy{
	MinVerdictConfidence:  0.5,
	KeepRegressionValue:   2,
	CutMaxRegressionValue: 1,
	HazardThreshold:       0.7,
	Hazards:               []string{"tautological", "mock_only"},
}

func f(v float64) *float64 { return &v }

// answers builds a Jev answer set: verdict choice and confidence, regression
// value, and the two hazard probabilities.
func answers(choice string, conf, rv, taut, mock float64) map[string]jev.Answer {
	return map[string]jev.Answer{
		"verdict":          {Type: "choice", Choice: choice, Probabilities: map[string]float64{choice: 1}, Confidence: f(conf)},
		"regression_value": {Type: "score", Score: f(rv)},
		"tautological":     {Type: "noul", Noul: f(taut)},
		"mock_only":        {Type: "noul", Noul: f(mock)},
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name      string
		a         map[string]jev.Answer
		truncated bool
		verdict   Verdict
		rule      string
	}{
		{"truncated wins", answers("cut", 0.9, 0, 0.9, 0.9), true, Review, "truncated"},
		{"low confidence", answers("cut", 0.49, 0, 0.9, 0), false, Review, "low_confidence"},
		{"confidence 0.5 is enough", answers("cut", 0.5, 0, 0.9, 0), false, Cut, "cut_corroborated"},
		{"high regression value keeps even a cut", answers("cut", 0.9, 2.0, 0.9, 0.9), false, Keep, "high_regression_value"},
		{"corroborated cut", answers("cut", 0.8, 0.3, 0, 0.91), false, Cut, "cut_corroborated"},
		{"hazard exactly at threshold counts", answers("cut", 0.8, 0.3, 0.7, 0), false, Cut, "cut_corroborated"},
		{"cut without hazard", answers("cut", 0.8, 0.3, 0.2, 0.2), false, Review, "cut_uncorroborated"},
		{"cut with rv exactly 1.0", answers("cut", 0.8, 1.0, 0.9, 0), false, Review, "cut_uncorroborated"},
		{"jev says review", answers("review", 0.8, 1.2, 0.1, 0.1), false, Review, "jev_review"},
		{"hazard on keep", answers("keep", 0.8, 1.5, 0.75, 0), false, Review, "hazard_on_keep"},
		{"default keep", answers("keep", 0.8, 1.5, 0.1, 0.1), false, Keep, "default_keep"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(pol, c.a, c.truncated)
			if got.Verdict != c.verdict || got.Rule != c.rule {
				t.Fatalf("got %s/%s, want %s/%s", got.Verdict, got.Rule, c.verdict, c.rule)
			}
		})
	}
}

func TestDecideReasons(t *testing.T) {
	got := Decide(pol, answers("cut", 0.8, 0.3, 0.1, 0.91), false)
	want := []string{"verdict=cut (0.80)", "regression_value=0.30", "mock_only=0.91"}
	if !reflect.DeepEqual(got.Reasons, want) {
		t.Fatalf("Reasons = %q, want %q", got.Reasons, want)
	}
}

func TestParseVerdict(t *testing.T) {
	for _, s := range []string{"keep", "cut", "review"} {
		if v, err := ParseVerdict(s); err != nil || string(v) != s {
			t.Errorf("ParseVerdict(%q) = %v, %v", s, v, err)
		}
	}
	if _, err := ParseVerdict("kep"); err == nil {
		t.Error(`ParseVerdict("kep") accepted`)
	}
}
