package policy

import (
	"reflect"
	"testing"

	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

func load(t *testing.T, name string) rubric.Rubric {
	t.Helper()
	r, err := rubric.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func f(v float64) *float64 { return &v }

// verdict builds a verdict answer whose act option has probability p.
func verdict(option string, p float64) jev.Answer {
	return jev.Answer{Type: "choice", Choice: option, Probabilities: map[string]float64{option: p}}
}

func TestDecideTest(t *testing.T) {
	r := load(t, rubric.DefaultTest)
	cases := []struct {
		name      string
		a         map[string]jev.Answer
		truncated bool
		want      Verdict
		rule      string
	}{
		{"truncated wins", map[string]jev.Answer{"verdict": verdict("cut", 0.99)}, true, Review, "truncated"},
		{"at act", map[string]jev.Answer{"verdict": verdict("cut", 0.6)}, false, Cut, "act"},
		{"just below act", map[string]jev.Answer{"verdict": verdict("cut", 0.59)}, false, Review, "review_band"},
		{"at review", map[string]jev.Answer{"verdict": verdict("cut", 0.3)}, false, Review, "review_band"},
		{"below review", map[string]jev.Answer{"verdict": verdict("cut", 0.29)}, false, Keep, "below_review"},
		// Review Focus 4: probabilities without the act option are never a cut.
		{"no cut probability", map[string]jev.Answer{"verdict": verdict("keep", 0.9)}, false, Keep, "below_review"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(r, c.a, c.truncated)
			if got.Verdict != c.want || got.Rule != c.rule {
				t.Fatalf("got %s/%s, want %s/%s", got.Verdict, got.Rule, c.want, c.rule)
			}
		})
	}
}

func TestDecideGroup(t *testing.T) {
	r := load(t, rubric.DefaultGroup)
	group := func(p, exact float64) map[string]jev.Answer {
		return map[string]jev.Answer{"verdict": verdict("consolidate", p), "exact_duplicate": {Type: "noul", Noul: f(exact)}}
	}
	cases := []struct {
		name  string
		p, ex float64
		want  Verdict
		exact bool
	}{
		{"consolidate", 0.93, 0.05, Consolidate, false},
		{"review band", 0.55, 0.05, Review, false},
		{"keep separate", 0.12, 0.05, KeepSeparate, false},
		{"exact duplicate flagged", 0.71, 0.71, Consolidate, true},
		{"exact duplicate below threshold", 0.71, 0.69, Consolidate, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(r, group(c.p, c.ex), false)
			if got.Verdict != c.want || got.ExactDuplicate != c.exact {
				t.Fatalf("got %s exact=%v, want %s exact=%v", got.Verdict, got.ExactDuplicate, c.want, c.exact)
			}
		})
	}
}

func TestDecideReasons(t *testing.T) {
	r := load(t, rubric.DefaultTest)
	got := Decide(r, map[string]jev.Answer{
		"verdict":      verdict("cut", 0.92),
		"mock_only":    {Type: "noul", Noul: f(0.91)},
		"tautological": {Type: "noul", Noul: f(0.10)},
	}, false)
	want := []string{"cut=0.92", "mock_only=0.91"}
	if !reflect.DeepEqual(got.Reasons, want) {
		t.Fatalf("Reasons = %q, want %q", got.Reasons, want)
	}
}

func TestParseVerdict(t *testing.T) {
	for _, s := range []string{"keep", "cut", "review", "consolidate", "keep_separate"} {
		if v, err := ParseVerdict(s); err != nil || string(v) != s {
			t.Errorf("ParseVerdict(%q) = %v, %v", s, v, err)
		}
	}
	if _, err := ParseVerdict("kep"); err == nil {
		t.Error(`ParseVerdict("kep") accepted`)
	}
}
