package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/policy"
)

func tc(id, body string) cases.TestCase {
	return cases.TestCase{ID: id, Lang: "go", Framework: "testing", Name: id, Body: body}
}

var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func TestImportAddUnchangedUpdate(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "c.jsonl"))
	if got := s.Import([]cases.TestCase{tc("a", "1"), tc("b", "2"), tc("c", "3")}); got != (ImportResult{Added: 3}) {
		t.Fatalf("first import = %+v", got)
	}
	if got := s.Import([]cases.TestCase{tc("a", "1"), tc("b", "2"), tc("c", "3")}); got != (ImportResult{Unchanged: 3}) {
		t.Fatalf("re-import = %+v", got)
	}
	if err := s.Label("b", policy.Cut, "court", "mock only", now); err != nil {
		t.Fatal(err)
	}
	if got := s.Import([]cases.TestCase{tc("a", "1"), tc("b", "2 changed"), tc("c", "3")}); got != (ImportResult{Updated: 1, Unchanged: 2}) {
		t.Fatalf("changed import = %+v", got)
	}
	for _, e := range s.Entries() {
		if e.ID == "b" {
			if e.Label != "" || e.LabeledBy != "" || e.Note != "" || !e.LabeledAt.IsZero() {
				t.Errorf("changed test kept its label: %+v", e)
			}
			if e.State.TestSource != "2 changed" {
				t.Errorf("state not replaced: %q", e.State.TestSource)
			}
		}
	}
}

func TestSplitForDeterministicAndRatio(t *testing.T) {
	if SplitFor("x") != SplitFor("x") {
		t.Fatal("SplitFor not deterministic")
	}
	hold := 0
	for i := 0; i < 1000; i++ {
		if SplitFor(fmt.Sprintf("go:f_test.go:Test%d", i)) == "holdout" {
			hold++
		}
	}
	if hold < 250 || hold > 350 {
		t.Errorf("holdout share %d/1000, want 250-350", hold)
	}
}

func TestSaveOpenRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "c.jsonl")
	s, _ := Open(p)
	s.Import([]cases.TestCase{tc("a", "1"), tc("b", "2")})
	s.Label("a", policy.Keep, "court", "", now)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Entries(), s2.Entries()) {
		t.Fatalf("round trip differs:\n%+v\n%+v", s.Entries(), s2.Entries())
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestLabelUnknownID(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "c.jsonl"))
	err := s.Label("go:nope", policy.Keep, "court", "", now)
	if err == nil || !strings.Contains(err.Error(), "go:nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadCasesBadLine(t *testing.T) {
	_, err := ReadCases(strings.NewReader(`{"id":"a"}` + "\n{nope\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}
