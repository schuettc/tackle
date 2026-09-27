package eval

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/corpus"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/policy"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

const (
	K = policy.Keep
	C = policy.Cut
	R = policy.Review
)

func row(label, pred policy.Verdict) Row {
	return Row{ID: string(label) + "/" + string(pred), Label: label, Pred: pred, Model: "jev-1"}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScore(t *testing.T) {
	rows := []Row{
		row(K, K), row(K, K), row(K, K), row(K, R), row(K, C),
		row(C, C), row(C, C), row(C, R),
		row(R, R), row(R, K),
		{ID: "err", Label: K, Err: "boom"},
	}
	m := Score(rows)
	if m.N != 10 || m.Errors != 1 {
		t.Fatalf("N=%d Errors=%d, want 10, 1", m.N, m.Errors)
	}
	if m.KeepN != 5 || m.FalseCuts != 1 || !near(m.FalseCutRate, 0.2) {
		t.Errorf("false cuts %d/%d rate %v, want 1/5 0.2", m.FalseCuts, m.KeepN, m.FalseCutRate)
	}
	if !near(m.CutPrecision, 2.0/3) || !near(m.CutRecall, 2.0/3) {
		t.Errorf("cut precision %v recall %v, want 2/3 each", m.CutPrecision, m.CutRecall)
	}
	if !near(m.ReviewAgreement, 0.5) {
		t.Errorf("review agreement %v, want 0.5", m.ReviewAgreement)
	}
	if m.Confusion[K][C] != 1 || m.Confusion[C][C] != 2 {
		t.Errorf("confusion = %v", m.Confusion)
	}
	if len(m.Models) != 1 || m.Models[0] != "jev-1" {
		t.Errorf("models = %v", m.Models)
	}
}

func TestScoreEmptyDenominators(t *testing.T) {
	m := Score([]Row{row(C, C)})
	if m.FalseCutRate != 0 || m.ReviewAgreement != 0 {
		t.Errorf("empty denominators gave %v / %v", m.FalseCutRate, m.ReviewAgreement)
	}
}

func TestSignalMeans(t *testing.T) {
	a, b, s := 0.8, 0.6, 1.0
	rows := []Row{
		{Label: C, Pred: C, Answers: map[string]jev.Answer{"mock_only": {Type: "noul", Noul: &a}, "regression_value": {Type: "score", Score: &s}}},
		{Label: C, Pred: C, Answers: map[string]jev.Answer{"mock_only": {Type: "noul", Noul: &b}, "regression_value": {Type: "score", Score: &s}}},
	}
	m := Score(rows)
	if !near(m.SignalMeans[C]["mock_only"], 0.7) || !near(m.SignalMeans[C]["regression_value"], 1) {
		t.Errorf("signal means = %v", m.SignalMeans)
	}
}

func TestCompare(t *testing.T) {
	a := []Row{{ID: "x", Label: K, Pred: K}, {ID: "y", Label: C, Pred: R}}
	b := []Row{{ID: "x", Label: K, Pred: K}, {ID: "y", Label: C, Pred: C}}
	got := Compare(a, b)
	if len(got) != 1 || got[0] != (Change{ID: "y", Label: C, From: R, To: C}) {
		t.Fatalf("Compare = %+v", got)
	}
}

type fakeEv struct{ calls int }

func (f *fakeEv) Evaluate(_ context.Context, _ string, _ any, questions map[string]any) (jev.Response, error) {
	f.calls++
	lo, conf := 0.1, 0.9
	ans := map[string]jev.Answer{}
	for k, q := range questions {
		switch q.(map[string]any)["type"] {
		case "noul":
			ans[k] = jev.Answer{Type: "noul", Noul: &lo}
		case "score":
			ans[k] = jev.Answer{Type: "score", Score: &lo}
		case "choice":
			ans[k] = jev.Answer{Type: "choice", Choice: "keep", Probabilities: map[string]float64{"keep": 1}, Confidence: &conf}
		}
	}
	return jev.Response{Model: "jev-1", Answers: ans}, nil
}

func TestRunSkipsUnlabeled(t *testing.T) {
	s, _ := corpus.Open(filepath.Join(t.TempDir(), "c.jsonl"))
	s.Import([]cases.TestCase{{ID: "a", Body: "1"}, {ID: "b", Body: "2"}, {ID: "c", Body: "3"}})
	s.Label("a", K, "court", "", time.Now())
	s.Label("c", C, "court", "", time.Now())
	r, _ := rubric.Load("v1")
	f := &fakeEv{}
	rows, err := Run(context.Background(), f, s.Entries(), judge.Options{Model: "jev-latest", Rubric: r})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || f.calls != 2 {
		t.Fatalf("rows=%d calls=%d, want 2, 2", len(rows), f.calls)
	}
	if rows[0].Pred != K || rows[0].Rule != "default_keep" {
		t.Errorf("row 0 = %+v", rows[0])
	}
}
