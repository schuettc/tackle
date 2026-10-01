package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/schuettc/tools-common/sqlitedb"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	a := Answer{ItemID: "t0", Hash: "h0", Kind: "test", Value: "cut", Note: "n1", Via: "group", Blind: true, Jev: json.RawMessage(`{"k":1}`), Model: "m", QuestionsHash: "q"}
	if err := s.SaveAnswers(ctx, p.ID, run, []Answer{a}); err != nil {
		t.Fatal(err)
	}
	k := Key{"t0", "h0"}
	m, _ := s.Answers(ctx, p.ID)
	first := m[k]
	if !first.Blind || first.Value != "cut" || string(first.Jev) != `{"k":1}` || first.RunID != run || first.AnsweredAt.IsZero() || !first.SentAt.IsZero() {
		t.Fatalf("%+v", first)
	}
	if sd, _ := s.Send(ctx, p.ID, ""); sd.Counts.Total() != 1 {
		t.Fatalf("sent %+v", sd)
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

func TestSendCountsOnlyUnsent(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(3))
	ans := func(i int) Answer {
		return Answer{ItemID: fmt.Sprintf("t%d", i), Hash: fmt.Sprintf("h%d", i), Kind: "test", Value: "keep", Via: "item"}
	}
	_ = s.SaveAnswers(ctx, p.ID, run, []Answer{ans(0), ans(1)})
	if sd, _ := s.Send(ctx, p.ID, ""); sd.Counts.Total() != 2 {
		t.Fatalf("n=%+v", sd)
	}
	if sd, _ := s.Send(ctx, p.ID, ""); sd.Counts.Total() != 0 {
		t.Fatalf("n=%+v", sd)
	}
	_ = s.SaveAnswers(ctx, p.ID, run, []Answer{ans(2)})
	if sd, _ := s.Send(ctx, p.ID, ""); sd.Counts.Total() != 1 {
		t.Fatalf("n=%+v", sd)
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

func TestLatestRunIDs(t *testing.T) {
	s, _ := open(t)
	got, err := s.LatestRunIDs(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	a, _ := s.Project(ctx, "/a")
	b, _ := s.Project(ctx, "/b")
	c, _ := s.Project(ctx, "/c")                                           // no run
	if _, err := s.RecordRun(ctx, Run{ProjectID: a.ID}, nil); err != nil { // superseded by r3
		t.Fatal(err)
	}
	r2, _ := s.RecordRun(ctx, Run{ProjectID: b.ID}, nil)
	r3, _ := s.RecordRun(ctx, Run{ProjectID: a.ID}, nil)
	got, err = s.LatestRunIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[a.ID] != r3 || got[b.ID] != r2 {
		t.Fatalf("got %v (c=%d)", got, c.ID)
	}
}

func TestSaveAnswersGroupNeverReplacesItem(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(2))
	k := Key{"t0", "h0"}
	save := func(via, val string) error {
		return s.SaveAnswers(ctx, p.ID, run, []Answer{{ItemID: "t0", Hash: "h0", Kind: "test", Value: val, Via: via}})
	}
	value := func() string { m, _ := s.Answers(ctx, p.ID); return m[k].Value }
	steps := []struct{ via, val, want string }{
		{"item", "cut", "cut"},
		{"group", "keep", "cut"}, // group over item: unchanged
		{"item", "keep", "keep"},
	}
	for _, st := range steps {
		if err := save(st.via, st.val); err != nil || value() != st.want {
			t.Fatalf("%v: %v %q", st, err, value())
		}
	}
	_ = s.DeleteAnswer(ctx, p.ID, k)
	for _, st := range []struct{ via, val, want string }{{"group", "cut", "cut"}, {"group", "keep", "keep"}, {"item", "cut", "cut"}} {
		if err := save(st.via, st.val); err != nil || value() != st.want {
			t.Fatalf("%v: %v %q", st, err, value())
		}
	}
	// group on a stale item is still stale
	err := s.SaveAnswers(ctx, p.ID, run, []Answer{{ItemID: "zz", Hash: "h", Kind: "test", Value: "cut", Via: "group"}})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("%v", err)
	}
}

func answerOf(i int, value, note string) Answer {
	kind, via := "test", "item"
	if value == "merge" || value == "separate" {
		kind, via = "group", "group"
	}
	return Answer{ItemID: fmt.Sprintf("t%d", i), Hash: fmt.Sprintf("h%d", i), Kind: kind, Value: value, Note: note, Via: via}
}

func TestSendRecordsCountsAndNotes(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(4))
	if err := s.SaveAnswers(ctx, p.ID, run, []Answer{
		answerOf(0, "cut", ""), answerOf(1, "keep", "needed for the edge"), answerOf(2, "merge", ""), answerOf(3, "separate", "different rules"),
	}); err != nil {
		t.Fatal(err)
	}
	sd, err := s.Send(ctx, p.ID, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if sd.ID == 0 || sd.Counts != (Counts{Cut: 1, Keep: 1, Merge: 1, Separate: 1}) || sd.Owner != "sess-a" || sd.ProjectID != p.ID {
		t.Fatalf("%+v", sd)
	}
	if len(sd.Notes) != 2 {
		t.Fatalf("notes %+v", sd.Notes)
	}
	// Nothing new: no send row, zero counts.
	again, err := s.Send(ctx, p.ID, "sess-a")
	if err != nil || again.ID != 0 || again.Counts.Total() != 0 {
		t.Fatalf("%+v %v", again, err)
	}
	pend, err := s.Undelivered(ctx, p.ID)
	if err != nil || len(pend) != 1 || pend[0].ID != sd.ID {
		t.Fatalf("%+v %v", pend, err)
	}
}

func TestClaimSendRules(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(3))
	send := func(i int, owner string) Send {
		t.Helper()
		if err := s.SaveAnswers(ctx, p.ID, run, []Answer{answerOf(i, "cut", "")}); err != nil {
			t.Fatal(err)
		}
		sd, err := s.Send(ctx, p.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
		return sd
	}
	sd := send(0, "A")
	// The store does not filter on the send's owner: serve decides who calls.
	// A send made for A is claimable by B (the owner has since moved).
	got, ok, err := s.ClaimSend(ctx, p.ID, "B")
	if err != nil || !ok || got.ID != sd.ID || got.DeliveredTo != "B" || got.DeliveredAt.IsZero() {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if _, ok, _ := s.ClaimSend(ctx, p.ID, "A"); ok {
		t.Fatal("delivered twice")
	}
	sd2 := send(1, "A")
	got, ok, _ = s.ClaimSend(ctx, p.ID, "A")
	if !ok || got.ID != sd2.ID || got.DeliveredTo != "A" {
		t.Fatalf("%+v %v", got, ok)
	}
	// Another project's sends are never taken.
	q, _ := s.Project(ctx, "/y")
	if _, ok, _ := s.ClaimSend(ctx, q.ID, "B"); ok {
		t.Fatal("claimed in another project")
	}
	// Oldest first.
	a := send(2, "")
	if _, ok, _ := s.ClaimSend(ctx, p.ID, "C"); !ok {
		t.Fatalf("ownerless send %d not claimed", a.ID)
	}
}

func TestClaimSendOneWinner(t *testing.T) {
	s, _ := open(t)
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(1))
	_ = s.SaveAnswers(ctx, p.ID, run, []Answer{answerOf(0, "cut", "")})
	if _, err := s.Send(ctx, p.ID, ""); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok, err := s.ClaimSend(ctx, p.ID, fmt.Sprintf("s%d", i)); err != nil {
				t.Error(err)
			} else if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d winners", wins.Load())
	}
}

func TestOwnerAndSendsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cull.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Project(ctx, "/x")
	run, _ := s.RecordRun(ctx, Run{ProjectID: p.ID}, items(1))
	_ = s.SaveAnswers(ctx, p.ID, run, []Answer{answerOf(0, "cut", "")})
	if err := s.SetOwner(ctx, p.ID, "sess", "pi: x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, p.ID, "sess"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	q, _ := s.ProjectByID(ctx, p.ID)
	if q.OwnerSession != "sess" || q.OwnerLabel != "pi: x" {
		t.Fatalf("%+v", q)
	}
	if _, ok, _ := s.ClaimSend(ctx, p.ID, "sess"); !ok {
		t.Fatal("send lost across reopen")
	}
}

func TestUpgradeFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cull.db")
	d, err := sqlitedb.Open(ctx, path, sqlitedb.Options{Migrations: Migrations[:1]})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Version(ctx); v != 1 {
		t.Fatalf("version %d", v)
	}
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO projects(id, root) VALUES (1, '/old')`,
			`INSERT INTO runs(id, project_id, at, total) VALUES (1, 1, 1000, 1)`,
			`INSERT INTO answers(project_id, item_id, hash, kind, value, note, via, run_id, answered_at)
			 VALUES (1, 't1', 'h1', 'test', 'cut', 'old note', 'item', 1, 1000)`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if v, _ := s.db.Version(ctx); v != SchemaVersion {
		t.Fatalf("version %d after upgrade", v)
	}
	p, err := s.Project(ctx, "/old")
	if err != nil || p.ID != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	as, err := s.Answers(ctx, p.ID)
	if err != nil || len(as) != 1 || as[Key{"t1", "h1"}].Value != "cut" || as[Key{"t1", "h1"}].Note != "old note" {
		t.Fatalf("%+v %v", as, err)
	}
	if err := s.SetOwner(ctx, p.ID, "sess", "pi: old"); err != nil {
		t.Fatal(err)
	}
	sd, err := s.Send(ctx, p.ID, "sess")
	if err != nil || sd.Counts.Cut != 1 {
		t.Fatalf("%+v %v", sd, err)
	}
	got, ok, err := s.ClaimSend(ctx, p.ID, "sess")
	if err != nil || !ok || got.ID != sd.ID {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}
