package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cull.db")
	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, p
}

func items(n int) []Item {
	var out []Item
	for i := 0; i < n; i++ {
		out = append(out, Item{ID: fmt.Sprintf("t%d", i), Kind: "test", Hash: fmt.Sprintf("h%d", i), File: "a_test.go", Name: "N",
			Verdict: "keep", Rule: "review_band", State: json.RawMessage(`{"a": 1,  "b":[2]}`), Jev: json.RawMessage(`{"q": {"p":0.5}}`), Model: "m"})
	}
	return out
}

func TestOpenCreatesPrivateFile(t *testing.T) {
	_, p := open(t)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
}

func TestProjectIdempotent(t *testing.T) {
	s, _ := open(t)
	a, _ := s.Project(ctx, "/x")
	b, err := s.Project(ctx, "/x")
	if err != nil || a.ID != b.ID || a.ID == 0 {
		t.Fatalf("%v %v %v", a, b, err)
	}
	c, _ := s.Project(ctx, "/y")
	if c.ID == a.ID {
		t.Fatal("same id")
	}
	g, err := s.ProjectByID(ctx, c.ID)
	if err != nil || g.Root != "/y" {
		t.Fatalf("%v %v", g, err)
	}
}

func TestRunRoundTrip(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	if _, _, err := s.LatestRun(ctx, p.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want ErrNoRows, got %v", err)
	}
	its := items(3)
	its[1].Kind, its[1].Rows, its[1].Members = "group", [][]string{{"a", "b"}}, []string{"m1", "m2"}
	id, err := s.RecordRun(ctx, Run{ProjectID: p.ID, Mode: "all", Base: "main", QuestionsHash: "q", Total: 9, Summary: map[string]int{"cut": 2}}, its)
	if err != nil {
		t.Fatal(err)
	}
	r, got, err := s.LatestRun(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != id || r.Mode != "all" || r.Base != "main" || r.QuestionsHash != "q" || r.Total != 9 || r.Summary["cut"] != 2 || r.At.IsZero() || time.Since(r.At) > time.Minute {
		t.Fatalf("run %+v", r)
	}
	if len(got) != 3 || got[0].ID != "t0" || got[2].ID != "t2" {
		t.Fatalf("items %+v", got)
	}
	if string(got[0].State) != `{"a": 1,  "b":[2]}` || string(got[0].Jev) != `{"q": {"p":0.5}}` {
		t.Fatalf("bytes %s %s", got[0].State, got[0].Jev)
	}
	if got[1].Rows[0][1] != "b" || got[1].Members[1] != "m2" || got[0].Rows != nil || got[0].Members != nil {
		t.Fatalf("rows/members %+v", got[1])
	}
}

func TestPruneKeepsFiveRunsAndAllAnswers(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	other, _ := s.Project(ctx, "/other")
	if _, err := s.RecordRun(ctx, Run{ProjectID: other.ID}, items(1)); err != nil {
		t.Fatal(err)
	}
	first, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(1))
	if err := s.SaveAnswers(ctx, p.ID, first, []Answer{{ItemID: "t0", Hash: "h0", Kind: "test", Value: "cut", Via: "item"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if _, err := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(1)); err != nil {
			t.Fatal(err)
		}
	}
	var runs, its int
	_ = s.db.QueryRow(`SELECT count(*) FROM runs WHERE project_id = ?`, p.ID).Scan(&runs)
	_ = s.db.QueryRow(`SELECT count(*) FROM items WHERE run_id = ?`, first).Scan(&its)
	if runs != 5 || its != 0 {
		t.Fatalf("runs %d old items %d", runs, its)
	}
	if _, _, err := s.LatestRun(ctx, other.ID); err != nil {
		t.Fatalf("other project pruned: %v", err)
	}
	as, _ := s.Answers(ctx, p.ID)
	if len(as) != 1 {
		t.Fatalf("answers %v", as)
	}
}

func TestSaveAnswersUpsert(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(2))
	a := Answer{ItemID: "t0", Hash: "h0", Kind: "test", Value: "cut", Note: "n1", Via: "item", Blind: true, Jev: json.RawMessage(`{"k":1}`), Model: "m", QuestionsHash: "q"}
	if err := s.SaveAnswers(ctx, p.ID, run, []Answer{a}); err != nil {
		t.Fatal(err)
	}
	k := Key{"t0", "h0"}
	m, _ := s.Answers(ctx, p.ID)
	first := m[k]
	if !first.Blind || first.Value != "cut" || string(first.Jev) != `{"k":1}` || first.RunID != run || first.AnsweredAt.IsZero() || !first.SentAt.IsZero() {
		t.Fatalf("%+v", first)
	}
	if n, _ := s.MarkSent(ctx, p.ID); n != 1 {
		t.Fatalf("sent %d", n)
	}
	time.Sleep(5 * time.Millisecond)
	run2, err := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(2))
	if err != nil {
		t.Fatal(err)
	}
	a.Value, a.Note, a.Blind, a.Via = "keep", "n2", false, "group"
	a.Model, a.QuestionsHash, a.Jev = "m2", "q2", json.RawMessage(`{"k":2}`)
	if err := s.SaveAnswers(ctx, p.ID, run2, []Answer{a}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Answers(ctx, p.ID)
	second := m[k]
	if second.Value != "keep" || second.Note != "n2" || second.Blind ||
		second.Via != "group" || second.Model != "m2" || second.QuestionsHash != "q2" || second.RunID != run2 || run2 == run ||
		string(second.Jev) != `{"k":2}` || !second.AnsweredAt.Equal(first.AnsweredAt) || !second.SentAt.IsZero() {
		t.Fatalf("%+v", second)
	}
	if err := s.DeleteAnswer(ctx, p.ID, k); err != nil {
		t.Fatal(err)
	}
	if m, _ = s.Answers(ctx, p.ID); len(m) != 0 {
		t.Fatalf("%v", m)
	}
}

func TestSaveAnswersStaleAndInvalid(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	q, _ := s.Project(ctx, "/q")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(2))
	good := Answer{ItemID: "t0", Hash: "h0", Kind: "test", Value: "cut", Via: "item"}
	for name, bad := range map[string]Answer{
		"id":   {ItemID: "nope", Hash: "h1", Kind: "test", Value: "cut", Via: "item"},
		"hash": {ItemID: "t1", Hash: "zzz", Kind: "test", Value: "cut", Via: "item"},
	} {
		err := s.SaveAnswers(ctx, p.ID, run, []Answer{good, bad})
		if !errors.Is(err, ErrStale) {
			t.Fatalf("%s: %v", name, err)
		}
		if m, _ := s.Answers(ctx, p.ID); len(m) != 0 {
			t.Fatalf("%s: stored %v", name, m)
		}
	}
	if err := s.SaveAnswers(ctx, q.ID, run, []Answer{good}); !errors.Is(err, ErrStale) {
		t.Fatalf("other project: %v", err)
	}
	for _, bad := range []Answer{{ItemID: "t0", Hash: "h0", Value: "maybe", Via: "item"}, {ItemID: "t0", Hash: "h0", Value: "cut", Via: "x"}} {
		err := s.SaveAnswers(ctx, p.ID, run, []Answer{bad})
		if err == nil || errors.Is(err, ErrStale) {
			t.Fatalf("want validation error, got %v", err)
		}
	}
}

func TestMarkSentCountsOnlyUnsent(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(3))
	ans := func(i int) Answer {
		return Answer{ItemID: fmt.Sprintf("t%d", i), Hash: fmt.Sprintf("h%d", i), Kind: "test", Value: "keep", Via: "item"}
	}
	_ = s.SaveAnswers(ctx, p.ID, run, []Answer{ans(0), ans(1)})
	if n, _ := s.MarkSent(ctx, p.ID); n != 2 {
		t.Fatalf("n=%d", n)
	}
	if n, _ := s.MarkSent(ctx, p.ID); n != 0 {
		t.Fatalf("n=%d", n)
	}
	_ = s.SaveAnswers(ctx, p.ID, run, []Answer{ans(2)})
	if n, _ := s.MarkSent(ctx, p.ID); n != 1 {
		t.Fatalf("n=%d", n)
	}
}

// TestHelperSave is run as a child process by TestConcurrentProcesses.
func TestHelperSave(t *testing.T) {
	p := os.Getenv("CULL_STORE_HELPER_PATH")
	if p == "" {
		t.Skip("helper")
	}
	var proj, run int64
	if _, err := fmt.Sscan(os.Getenv("CULL_STORE_HELPER_IDS"), &proj, &run); err != nil {
		t.Fatal(err)
	}
	idx := os.Getenv("CULL_STORE_HELPER_N")
	// Start barrier: wait for the parent's go file so all children collide.
	goFile := os.Getenv("CULL_STORE_HELPER_GO")
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(goFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("go file never appeared")
		}
	}
	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for r := 0; r < 10; r++ {
		a := Answer{ItemID: "t" + idx, Hash: "h" + idx, Kind: "test", Value: "cut", Via: "item", Note: fmt.Sprint(r)}
		if err := s.SaveAnswers(ctx, proj, run, []Answer{a}); err != nil {
			t.Fatal(err)
		}
		// All children also write the same key.
		sh := Answer{ItemID: "shared", Hash: "hs", Kind: "test", Value: "keep", Via: "item", Note: "child" + idx}
		if err := s.SaveAnswers(ctx, proj, run, []Answer{sh}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentProcesses(t *testing.T) {
	s, path := open(t)
	p, _ := s.Project(ctx, "/x")
	const n = 4
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, append(items(n), Item{ID: "shared", Kind: "test", Hash: "hs", Verdict: "keep"}))
	goFile := filepath.Join(t.TempDir(), "go")
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestHelperSave$", "-test.count=1")
			cmd.Env = append(os.Environ(), "CULL_STORE_HELPER_PATH="+path, "CULL_STORE_HELPER_GO="+goFile,
				fmt.Sprintf("CULL_STORE_HELPER_IDS=%d %d", p.ID, run), fmt.Sprintf("CULL_STORE_HELPER_N=%d", i))
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("%w\n%s", err, out)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond) // let children start and reach the barrier
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	m, err := s.Answers(ctx, p.ID)
	if err != nil || len(m) != n+1 {
		t.Fatalf("answers %d %v", len(m), err)
	}
	for i := 0; i < n; i++ {
		a, ok := m[Key{fmt.Sprintf("t%d", i), fmt.Sprintf("h%d", i)}]
		if !ok || a.Note != "9" || a.Value != "cut" {
			t.Fatalf("own key %d: %+v", i, a)
		}
	}
	sh := m[Key{"shared", "hs"}]
	valid := false
	for i := 0; i < n; i++ {
		valid = valid || sh.Note == fmt.Sprintf("child%d", i)
	}
	if !valid || sh.Value != "keep" || sh.Via != "item" || sh.RunID != run {
		t.Fatalf("shared key: %+v", sh)
	}
}
