// Package eval measures a rubric against the labeled corpus: it judges every
// labeled entry, applies the policy, and scores predictions against labels.
package eval

import (
	"context"
	"sort"

	"github.com/schuettc/tackle/internal/cull/corpus"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/policy"
)

// Row is one labeled entry with the rubric's prediction.
type Row struct {
	ID      string                `json:"id"`
	Label   policy.Verdict        `json:"label"`
	Pred    policy.Verdict        `json:"pred,omitempty"`
	Rule    string                `json:"rule,omitempty"`
	Reasons []string              `json:"reasons,omitempty"`
	Model   string                `json:"model,omitempty"`
	Answers map[string]jev.Answer `json:"answers,omitempty"`
	Err     string                `json:"err,omitempty"`
}

// Metrics scores predictions against labels. Rates are 0 when their
// denominator is 0; the counts beside them say so.
type Metrics struct {
	N               int                                       `json:"n"`
	Errors          int                                       `json:"errors"`
	KeepN           int                                       `json:"keep_n"`
	FalseCuts       int                                       `json:"false_cuts"`
	CutN            int                                       `json:"cut_n"`
	PredCutN        int                                       `json:"pred_cut_n"`
	TruePosCut      int                                       `json:"true_pos_cut"`
	ReviewN         int                                       `json:"review_n"`
	ReviewAgree     int                                       `json:"review_agree"`
	FalseCutRate    float64                                   `json:"false_cut_rate"`
	CutPrecision    float64                                   `json:"cut_precision"`
	CutRecall       float64                                   `json:"cut_recall"`
	ReviewAgreement float64                                   `json:"review_agreement"`
	Confusion       map[policy.Verdict]map[policy.Verdict]int `json:"confusion"` // [label][pred]
	SignalMeans     map[policy.Verdict]map[string]float64     `json:"signal_means"`
	Models          []string                                  `json:"models"`
}

// Run judges the labeled entries and applies opt.Rubric's policy. Rows come
// back in entry order. An error from JudgeAll (rejected key, cancellation)
// is returned with whatever rows completed.
func Run(ctx context.Context, ev judge.Evaluator, entries []corpus.Entry, opt judge.Options) ([]Row, error) {
	var labeled []corpus.Entry
	var states []judge.State
	for _, e := range entries {
		if e.Label != "" {
			labeled = append(labeled, e)
			states = append(states, e.State)
		}
	}
	judged, err := judge.JudgeAll(ctx, ev, states, opt)
	rows := make([]Row, len(labeled))
	for i, e := range labeled {
		j := judged[i]
		rows[i] = Row{ID: e.ID, Label: e.Label, Model: j.Model, Answers: j.Answers, Err: j.Err}
		if j.Err == "" {
			res := policy.Decide(opt.Rubric.Policy, j.Answers, e.State.Truncated)
			rows[i].Pred, rows[i].Rule, rows[i].Reasons = res.Verdict, res.Rule, res.Reasons
		}
	}
	return rows, err
}

func rate(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// Score computes metrics. Rows with an error count only in Errors.
func Score(rows []Row) Metrics {
	m := Metrics{Confusion: map[policy.Verdict]map[policy.Verdict]int{}, SignalMeans: map[policy.Verdict]map[string]float64{}}
	sums := map[policy.Verdict]map[string]float64{}
	counts := map[policy.Verdict]map[string]int{}
	models := map[string]bool{}
	for _, r := range rows {
		if r.Err != "" {
			m.Errors++
			continue
		}
		m.N++
		if m.Confusion[r.Label] == nil {
			m.Confusion[r.Label] = map[policy.Verdict]int{}
			sums[r.Label], counts[r.Label] = map[string]float64{}, map[string]int{}
		}
		m.Confusion[r.Label][r.Pred]++
		if r.Model != "" {
			models[r.Model] = true
		}
		switch r.Label {
		case policy.Keep:
			m.KeepN++
			if r.Pred == policy.Cut {
				m.FalseCuts++
			}
		case policy.Cut:
			m.CutN++
			if r.Pred == policy.Cut {
				m.TruePosCut++
			}
		case policy.Review:
			m.ReviewN++
			if r.Pred == policy.Review {
				m.ReviewAgree++
			}
		}
		if r.Pred == policy.Cut {
			m.PredCutN++
		}
		for k, a := range r.Answers {
			var v *float64
			switch a.Type {
			case "noul":
				v = a.Noul
			case "score":
				v = a.Score
			}
			if v != nil {
				sums[r.Label][k] += *v
				counts[r.Label][k]++
			}
		}
	}
	m.FalseCutRate = rate(m.FalseCuts, m.KeepN)
	m.CutPrecision = rate(m.TruePosCut, m.PredCutN)
	m.CutRecall = rate(m.TruePosCut, m.CutN)
	m.ReviewAgreement = rate(m.ReviewAgree, m.ReviewN)
	for label, ks := range sums {
		m.SignalMeans[label] = map[string]float64{}
		for k, s := range ks {
			m.SignalMeans[label][k] = s / float64(counts[label][k])
		}
	}
	for k := range models {
		m.Models = append(m.Models, k)
	}
	sort.Strings(m.Models)
	return m
}

// Change is an entry whose prediction differs between two runs.
type Change struct {
	ID    string         `json:"id"`
	Label policy.Verdict `json:"label"`
	From  policy.Verdict `json:"from"`
	To    policy.Verdict `json:"to"`
}

// Compare lists entries, matched by ID, whose prediction differs from a to b.
func Compare(a, b []Row) []Change {
	before := make(map[string]Row, len(a))
	for _, r := range a {
		before[r.ID] = r
	}
	var out []Change
	for _, r := range b {
		if o, ok := before[r.ID]; ok && o.Pred != r.Pred {
			out = append(out, Change{ID: r.ID, Label: r.Label, From: o.Pred, To: r.Pred})
		}
	}
	return out
}
