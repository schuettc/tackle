package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/rubric"
)

// fake answers every question with a noul of 0.5, tagging the response model
// with the test name so results can be matched to inputs.
type fake struct {
	calls, inFlight, maxInFlight atomic.Int32
	sleep                        time.Duration
	fail                         map[string]error
	after                        func(n int32) // called with the call count after each success
}

func (f *fake) Evaluate(ctx context.Context, model string, state any, questions map[string]any) (jev.Response, error) {
	n := f.calls.Add(1)
	cur := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxInFlight.Load()
		if cur <= m || f.maxInFlight.CompareAndSwap(m, cur) {
			break
		}
	}
	time.Sleep(f.sleep)
	name := state.(State).TestName
	if err := f.fail[name]; err != nil {
		return jev.Response{}, err
	}
	half := 0.5
	ans := map[string]jev.Answer{}
	for k := range questions {
		ans[k] = jev.Answer{Type: "noul", Noul: &half}
	}
	if f.after != nil {
		f.after(n)
	}
	return jev.Response{Model: "jev-test/" + name, Answers: ans}, nil
}

func states(n int) []State {
	out := make([]State, n)
	for i := range out {
		out[i] = StateFor(cases.TestCase{Name: fmt.Sprintf("T%02d", i), Body: fmt.Sprintf("body %d", i)})
	}
	return out
}

func opts(t *testing.T, cache *Cache) Options {
	t.Helper()
	r, err := rubric.Load("v1")
	if err != nil {
		t.Fatal(err)
	}
	return Options{Model: "jev-latest", Rubric: r, Concurrency: 4, Cache: cache}
}

func TestStateFor(t *testing.T) {
	tc := cases.TestCase{
		Lang: "go", Framework: "testing", Name: "TestParse/quoted", Body: "b", Context: "c",
		Callees: []cases.Callee{{Symbol: "creel.Parse", File: "env.go", Source: "func Parse(){}"}}, Truncated: true,
	}
	s := StateFor(tc)
	if s.Note != StateNote || s.TestName != tc.Name || s.TestSource != "b" || s.SetupContext != "c" ||
		s.Language != "go" || s.Framework != "testing" || !s.Truncated || len(s.CodeUnderTest) != 1 || s.CodeUnderTest[0] != tc.Callees[0] {
		t.Fatalf("StateFor = %+v", s)
	}
	if StateHash(s) == StateHash(StateFor(cases.TestCase{Name: "other"})) {
		t.Error("different states share a hash")
	}
}

func TestJudgeAllOrderAndConcurrency(t *testing.T) {
	f := &fake{sleep: 5 * time.Millisecond}
	got, err := JudgeAll(context.Background(), f, states(20), opts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i, j := range got {
		if want := fmt.Sprintf("jev-test/T%02d", i); j.Model != want {
			t.Fatalf("result %d model %q, want %q", i, j.Model, want)
		}
	}
	if m := f.maxInFlight.Load(); m > 4 {
		t.Errorf("max in flight %d > 4", m)
	}
}

func TestCacheHitSkipsCall(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	f := &fake{}
	o := opts(t, c)
	if _, err := JudgeAll(context.Background(), f, states(5), o); err != nil {
		t.Fatal(err)
	}
	f.calls.Store(0)
	got, err := JudgeAll(context.Background(), f, states(5), o)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 0 {
		t.Errorf("second run made %d calls", f.calls.Load())
	}
	for _, j := range got {
		if !j.Cached {
			t.Error("result not marked Cached")
		}
	}
}

func TestThresholdOnlyChangeUsesCache(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	f := &fake{}
	o := opts(t, c)
	JudgeAll(context.Background(), f, states(5), o)
	f.calls.Store(0)
	o.Rubric.Policy.HazardThreshold = 0.9
	o.Rubric.Version = "v2"
	JudgeAll(context.Background(), f, states(5), o)
	if f.calls.Load() != 0 {
		t.Errorf("threshold-only change made %d calls", f.calls.Load())
	}
}

func TestQuestionChangeMissesCache(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	f := &fake{}
	o := opts(t, c)
	JudgeAll(context.Background(), f, states(5), o)
	f.calls.Store(0)
	o.Rubric.Questions[0].Instructions += " Changed."
	JudgeAll(context.Background(), f, states(5), o)
	if f.calls.Load() != 5 {
		t.Errorf("question change made %d calls, want 5", f.calls.Load())
	}
}

func TestRefreshBypassesGet(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	f := &fake{}
	o := opts(t, c)
	JudgeAll(context.Background(), f, states(5), o)
	f.calls.Store(0)
	o.Refresh = true
	JudgeAll(context.Background(), f, states(5), o)
	if f.calls.Load() != 5 {
		t.Errorf("refresh made %d calls, want 5", f.calls.Load())
	}
}

func TestPerStateErrorContinues(t *testing.T) {
	f := &fake{fail: map[string]error{"T03": errors.New("typesafe: missing answer for \"verdict\"")}}
	got, err := JudgeAll(context.Background(), f, states(6), opts(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i, j := range got {
		if (i == 3) != (j.Err != "") {
			t.Errorf("result %d Err = %q", i, j.Err)
		}
		if i != 3 && len(j.Answers) == 0 {
			t.Errorf("result %d has no answers", i)
		}
	}
}

func TestUnauthorizedAborts(t *testing.T) {
	f := &fake{fail: map[string]error{}}
	for _, s := range states(10) {
		f.fail[s.TestName] = jev.ErrUnauthorized
	}
	o := opts(t, nil)
	o.Concurrency = 1
	_, err := JudgeAll(context.Background(), f, states(10), o)
	if !errors.Is(err, jev.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if f.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", f.calls.Load())
	}
}

func TestCancelKeepsCompletedCache(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	f := &fake{after: func(n int32) {
		if n == 5 {
			once.Do(cancel)
		}
	}}
	o := opts(t, c)
	o.Concurrency = 1
	st := states(10)
	_, err := JudgeAll(ctx, f, st, o)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	for _, s := range st[:5] {
		if _, ok := c.Get(CacheKey(StateHash(s), o.Model, o.Rubric.QuestionsHash())); !ok {
			t.Errorf("%s not cached", s.TestName)
		}
	}
	files, _ := filepath.Glob(filepath.Join(c.Dir, "*"))
	for _, p := range files {
		b, _ := os.ReadFile(p)
		if !json.Valid(b) {
			t.Errorf("cache file %s is not valid JSON", p)
		}
	}
}
