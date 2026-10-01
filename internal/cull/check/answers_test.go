package check

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/store"
)

const testSrc = "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n"

var bg = context.Background()

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(bg, filepath.Join(t.TempDir(), "cull.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func fixture(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	goModule(t, root)
	withEgress(t, root)
	writeFile(t, root, "pkg/calc_test.go", src)
	return root
}

// runCheck refreshes: the Jev answer cache is shared across tests here and
// keyed by state, so cached answers from another fake would leak in.
func runCheck(t *testing.T, root string, f *fakeEval, s *store.Store) Report {
	t.Helper()
	var se strings.Builder
	r, err := Run(bg, f, Options{Path: root, Store: s, Stderr: &se, Refresh: true})
	if se.Len() > 0 {
		t.Log(se.String())
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// answerLatest stores an answer for the named item of the project's latest run.
func answerLatest(t *testing.T, s *store.Store, root, id, kind, value, note string) {
	t.Helper()
	p, err := s.Project(bg, root)
	if err != nil {
		t.Fatal(err)
	}
	run, items, err := s.LatestRun(bg, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == id {
			err := s.SaveAnswers(bg, p.ID, run.ID, []store.Answer{{ItemID: id, Hash: it.Hash, Kind: kind, Value: value, Note: note, Via: "item"}})
			if err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("item %s not in latest run %+v", id, items)
}

func TestAnswerSettlesUnchangedTest(t *testing.T) {
	root := fixture(t, testSrc)
	s := openStore(t)
	f := &fakeEval{review: map[string]bool{"TestA": true}}
	first := runCheck(t, root, f, s)
	if first.Tests[0].Verdict != "review" || first.Summary["answered"] != 0 {
		t.Fatalf("first: %+v %v", first.Tests[0], first.Summary)
	}
	answerLatest(t, s, first.Root, first.Tests[0].ID, "test", "cut", "dead weight")
	second := runCheck(t, root, f, s)
	tr := second.Tests[0]
	if tr.Verdict != "cut" || tr.Rule != "court" || second.Summary["answered"] != 1 || second.Summary["cut"] != 1 || second.Summary["review"] != 0 {
		t.Fatalf("second: %+v %v", tr, second.Summary)
	}
	if last := tr.Reasons[len(tr.Reasons)-1]; last != "court: dead weight" {
		t.Fatalf("reasons %v", tr.Reasons)
	}
	if !second.HasActions() {
		t.Fatal("HasActions false")
	}
}

func TestAnswerIgnoredAfterEdit(t *testing.T) {
	root := fixture(t, testSrc)
	s := openStore(t)
	f := &fakeEval{review: map[string]bool{"TestA": true}}
	first := runCheck(t, root, f, s)
	answerLatest(t, s, first.Root, first.Tests[0].ID, "test", "cut", "")
	writeFile(t, root, "pkg/calc_test.go", strings.Replace(testSrc, "_ = 1", "_ = 2", 1))
	second := runCheck(t, root, f, s)
	if second.Tests[0].Verdict != "review" || second.Summary["answered"] != 0 {
		t.Fatalf("%+v %v", second.Tests[0], second.Summary)
	}
}

func TestAnswerNeverOverridesConfidentVerdict(t *testing.T) {
	root := fixture(t, testSrc)
	s := openStore(t)
	first := runCheck(t, root, &fakeEval{review: map[string]bool{"TestA": true}}, s)
	answerLatest(t, s, first.Root, first.Tests[0].ID, "test", "keep", "")
	second := runCheck(t, root, &fakeEval{cut: map[string]bool{"TestA": true}}, s)
	tr := second.Tests[0]
	if tr.Verdict != "cut" || tr.Rule != "act" || second.Summary["answered"] != 0 {
		t.Fatalf("%+v %v", tr, second.Summary)
	}
}

const dupGroupSrc = "package pkg\n\n" +
	"func TestA1(t *testing.T) {\n\tcfg := \"cfg\"\n\t_ = cfg\n\tx := \"one\"\n\t_ = x\n\ty := 1\n\t_ = y\n}\n\n" +
	"func TestA2(t *testing.T) {\n\tcfg := \"cfg\"\n\t_ = cfg\n\tx := \"two\"\n\t_ = x\n\ty := 2\n\t_ = y\n}\n"

func TestGroupAnswerMerge(t *testing.T) {
	root := fixture(t, dupGroupSrc)
	s := openStore(t)
	f := &fakeEval{review: map[string]bool{"TestA1": true, "TestA2": true}}
	first := runCheck(t, root, f, s)
	if len(first.Groups) != 1 || first.Groups[0].Verdict != "review" {
		t.Fatalf("first groups %+v", first.Groups)
	}
	g := first.Groups[0]
	if GroupHash(g.MemberHashes) == "" {
		t.Fatal("empty hash")
	}
	answerLatest(t, s, first.Root, g.ID, "group", "merge", "")
	second := runCheck(t, root, f, s)
	g = second.Groups[0]
	if g.Verdict != "consolidate" || g.Rule != "court" || second.Summary["consolidate"] != 1 || second.Summary["group_review"] != 0 {
		t.Fatalf("%+v %v", g, second.Summary)
	}
	// An answer whose value does not fit the kind is ignored.
	got := applyAnswers([]TestResult{{TestCase: first.Tests[0].TestCase, Verdict: "review", Rule: "review_band"}}, nil,
		map[store.Key]store.Answer{{ItemID: first.Tests[0].ID, Hash: first.Tests[0].Hash}: {Value: "merge"}})
	if got != 0 {
		t.Fatalf("misfit answer applied: %d", got)
	}
}

func TestRunRecorded(t *testing.T) {
	src := testSrc + "\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n\nfunc TestC(t *testing.T) {\n\t_ = 3\n}\n\nfunc TestD(t *testing.T) {\n\t_ = 4\n}\n"
	root := fixture(t, src)
	s := openStore(t)
	f := &fakeEval{review: map[string]bool{"TestA": true, "TestB": true}, cut: map[string]bool{"TestC": true}}
	first := runCheck(t, root, f, s)
	var bID string
	for _, tr := range first.Tests {
		if tr.Name == "TestB" {
			bID = tr.ID
		}
	}
	answerLatest(t, s, first.Root, bID, "test", "keep", "")
	second := runCheck(t, root, f, s)

	p, _ := s.Project(bg, second.Root)
	run, items, err := s.LatestRun(bg, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Total != 4 || run.Mode != "suite" || run.QuestionsHash == "" || run.Summary["answered"] != 1 || run.Summary["review"] != second.Summary["review"] {
		t.Fatalf("run %+v", run)
	}
	if len(items) != 2 {
		t.Fatalf("items %+v", items)
	}
	byName := map[string]store.Item{}
	for _, it := range items {
		byName[it.Name] = it
	}
	a, b := byName["TestA"], byName["TestB"]
	if a.Verdict != "review" || a.Rule != "review_band" || b.Verdict != "keep" || b.Rule != "court" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	for _, it := range []store.Item{a, b} {
		if it.Kind != "test" || it.Hash == "" || it.Model != "fake-model" || len(it.State) == 0 || len(it.Jev) == 0 || it.File == "" {
			t.Fatalf("item %+v", it)
		}
	}
}

func TestStoreUnavailableStillWritesLastJSON(t *testing.T) {
	root := fixture(t, testSrc)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state"), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CULL_HOME", home)
	var stderr strings.Builder
	r, err := Run(bg, &fakeEval{review: map[string]bool{"TestA": true}}, Options{Path: root, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "cull: review store unavailable: ") {
		t.Fatalf("stderr %q", stderr.String())
	}
	if r.Tests[0].Verdict != "review" {
		t.Fatalf("%+v", r.Tests[0])
	}
	if _, err := os.Stat(filepath.Join(root, ".cull", "last.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDryRunTouchesNoStore(t *testing.T) {
	root := fixture(t, testSrc)
	home := t.TempDir()
	t.Setenv("CULL_HOME", home)
	if _, err := Run(bg, &fakeEval{}, Options{Path: root, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state dir touched: %v", err)
	}
	s := openStore(t)
	dry, err := Run(bg, &fakeEval{}, Options{Path: root, DryRun: true, Store: s})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Project(bg, dry.Root)
	if _, _, err := s.LatestRun(bg, p.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("run recorded: %v", err)
	}
}
