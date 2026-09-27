// Package policy turns Jev's answers about one test into a verdict. It is a
// pure function of the answers and the rubric's thresholds, biased against
// cutting: anything uncertain becomes review.
package policy

import (
	"fmt"

	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

// Verdict is a judgment on one test, and also the label a person gives it.
type Verdict string

const (
	Keep   Verdict = "keep"
	Cut    Verdict = "cut"
	Review Verdict = "review"
)

// ParseVerdict accepts exactly keep, cut or review.
func ParseVerdict(s string) (Verdict, error) {
	switch v := Verdict(s); v {
	case Keep, Cut, Review:
		return v, nil
	}
	return "", fmt.Errorf("%q is not a verdict (keep, cut, review)", s)
}

// Result is a verdict, the rule that produced it, and the evidence behind it.
type Result struct {
	Verdict Verdict  `json:"verdict"`
	Rule    string   `json:"rule"`
	Reasons []string `json:"reasons"`
}

func val(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// Decide applies the spec §4.3 rules in order. Missing numbers count as 0,
// so a missing verdict confidence lands on low_confidence.
func Decide(p rubric.Policy, answers map[string]jev.Answer, truncated bool) Result {
	v := answers["verdict"]
	conf := val(v.Confidence)
	rv := val(answers["regression_value"].Score)

	reasons := []string{fmt.Sprintf("verdict=%s (%.2f)", v.Choice, conf), fmt.Sprintf("regression_value=%.2f", rv)}
	hz := 0.0
	for _, h := range p.Hazards {
		n := val(answers[h].Noul)
		hz = max(hz, n)
		if n >= p.HazardThreshold {
			reasons = append(reasons, fmt.Sprintf("%s=%.2f", h, n))
		}
	}
	res := func(verdict Verdict, rule string) Result {
		return Result{Verdict: verdict, Rule: rule, Reasons: reasons}
	}

	hazard := hz >= p.HazardThreshold
	switch {
	case truncated:
		return res(Review, "truncated")
	case conf < p.MinVerdictConfidence:
		return res(Review, "low_confidence")
	case rv >= p.KeepRegressionValue:
		return res(Keep, "high_regression_value")
	case v.Choice == string(Cut) && hazard && rv < p.CutMaxRegressionValue:
		return res(Cut, "cut_corroborated")
	case v.Choice == string(Cut):
		return res(Review, "cut_uncorroborated")
	case v.Choice == string(Review):
		return res(Review, "jev_review")
	case hazard:
		return res(Review, "hazard_on_keep")
	}
	return res(Keep, "default_keep")
}
