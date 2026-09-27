package judge

import (
	"context"
	"errors"
	"sync"

	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

// Evaluator is the Jev call; *jev.Client satisfies it.
type Evaluator interface {
	Evaluate(ctx context.Context, model string, state any, questions map[string]any) (jev.Response, error)
}

// Options configures a judging run. A nil Cache disables caching.
type Options struct {
	Model       string
	Rubric      rubric.Rubric
	Concurrency int
	Refresh     bool // skip cache reads; still write
	Cache       *Cache
}

// Judged is Jev's answer set for one state, or the error that prevented it.
type Judged struct {
	StateHash string                `json:"state_hash"`
	Model     string                `json:"model"` // resolved model version
	Answers   map[string]jev.Answer `json:"answers,omitempty"`
	Cached    bool                  `json:"cached"`
	Err       string                `json:"err,omitempty"`
}

// JudgeAll judges states in parallel and returns results in input order. A
// failure on one state is recorded on that result and the run continues. A
// rejected key or a cancelled context stops the run and is returned.
func JudgeAll(ctx context.Context, ev Evaluator, states []State, opt Options) ([]Judged, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	n := opt.Concurrency
	if n <= 0 {
		n = 6
	}
	questions := opt.Rubric.APIQuestions()
	qhash := opt.Rubric.QuestionsHash()
	out := make([]Judged, len(states))
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup

	for i, s := range states {
		sh := StateHash(s)
		out[i].StateHash = sh
		key := CacheKey(sh, opt.Model, qhash)
		if opt.Cache != nil && !opt.Refresh {
			if r, ok := opt.Cache.Get(key); ok {
				out[i] = Judged{StateHash: sh, Model: r.Model, Answers: r.Answers, Cached: true}
				continue
			}
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			out[i].Err = context.Cause(ctx).Error()
			continue
		}
		wg.Add(1)
		go func(i int, s State, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			r, err := ev.Evaluate(ctx, opt.Model, s, questions)
			if err != nil {
				out[i].Err = err.Error()
				if errors.Is(err, jev.ErrUnauthorized) {
					cancel(err)
				}
				return
			}
			out[i].Model, out[i].Answers = r.Model, r.Answers
			if opt.Cache != nil {
				if err := opt.Cache.Put(key, r); err != nil {
					out[i].Err = "cache: " + err.Error()
				}
			}
		}(i, s, key)
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return out, err
	}
	return out, nil
}
