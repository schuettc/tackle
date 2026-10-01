// Package policy turns Jev's answers into a verdict. It is a pure function of
// the answers and the rubric's thresholds on the probability of the act option
// ("cut" for a test, "consolidate" for a group). Anything uncertain is review.
package policy

import (
	"fmt"

	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

// Verdict is a judgment on one test or one group.
type Verdict string

const (
	Keep         Verdict = "keep"
	Cut          Verdict = "cut"
	Review       Verdict = "review"
	Consolidate  Verdict = "consolidate"
	KeepSeparate Verdict = "keep_separate"
)

// ParseVerdict accepts exactly one of the five verdicts.
func ParseVerdict(s string) (Verdict, error) {
	switch v := Verdict(s); v {
	case Keep, Cut, Review, Consolidate, KeepSeparate:
		return v, nil
	}
	return "", fmt.Errorf("%q is not a verdict (keep, cut, review, consolidate, keep_separate)", s)
}

// Result is a verdict, the rule that produced it, and the evidence behind it.
// ExactDuplicate (groups only) means at least one member is fully redundant.
type Result struct {
	Verdict        Verdict  `json:"verdict"`
	Rule           string   `json:"rule"`
	Reasons        []string `json:"reasons"`
	ExactDuplicate bool     `json:"exact_duplicate,omitempty"`
}

// Decide applies spec §4.3 (tests) or §5.3 (groups). A missing probability for
// the act option counts as 0, so it can never produce a cut or consolidate.
func Decide(r rubric.Rubric, answers map[string]jev.Answer, truncated bool) Result {
	act := r.ActOption()
	p := answers["verdict"].Probabilities[act]
	reasons := []string{fmt.Sprintf("%s=%.2f", act, p)}
	for _, q := range r.Questions {
		if a, ok := answers[q.Key]; ok && q.Type == "noul" && a.Noul != nil && *a.Noul >= 0.5 {
			reasons = append(reasons, fmt.Sprintf("%s=%.2f", q.Key, *a.Noul))
		}
	}
	res := Result{Reasons: reasons}
	if r.Kind == rubric.KindGroup {
		if n := answers["exact_duplicate"].Noul; n != nil && *n >= r.Policy.ExactDuplicate {
			res.ExactDuplicate = true
		}
	}

	onAct, below := Cut, Keep
	if r.Kind == rubric.KindGroup {
		onAct, below = Consolidate, KeepSeparate
	}
	switch {
	case truncated:
		res.Verdict, res.Rule = Review, "truncated"
	case p >= r.Policy.Act:
		res.Verdict, res.Rule = onAct, "act"
	case p >= r.Policy.Review:
		res.Verdict, res.Rule = Review, "review_band"
	default:
		res.Verdict, res.Rule = below, "below_review"
	}
	return res
}

// SettingReason is the reason GuardSetting adds.
const SettingReason = "checks a setting's value"

// GuardSetting sends a test that only checks a project setting's value to
// Court: a cut verdict on a test with pins set becomes review (rule
// pins_setting), keeping Jev's reasons. Any other verdict is unchanged. Only
// Court knows whether such a value is a deliberate tripwire.
func GuardSetting(res Result, pins bool) Result {
	if !pins || res.Verdict != Cut {
		return res
	}
	res.Verdict, res.Rule = Review, "pins_setting"
	res.Reasons = append(append([]string(nil), res.Reasons...), SettingReason)
	return res
}
