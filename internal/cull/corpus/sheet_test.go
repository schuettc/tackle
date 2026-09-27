package corpus

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/policy"
)

func sheetFor(t *testing.T) string {
	t.Helper()
	s, _ := Open(filepath.Join(t.TempDir(), "c.jsonl"))
	a := tc("go:a_test.go:TestA", "func TestA(t *testing.T) {\n\t// label: cut  <- inside a fence, must be ignored\n}")
	a.Callees = []cases.Callee{{Symbol: "pkg.A", File: "a.go", Source: "func A() {}"}}
	s.Import([]cases.TestCase{a, tc("go:b_test.go:TestB", "func TestB(t *testing.T) {}")})
	var buf bytes.Buffer
	if err := WriteSheet(&buf, s.Entries()); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestSheetRoundTrip(t *testing.T) {
	sheet := sheetFor(t)
	if !strings.Contains(sheet, "// pkg.A (a.go)") {
		t.Errorf("sheet lacks callee header:\n%s", sheet)
	}
	// Fill in only TestB.
	i := strings.Index(sheet, "## go:b_test.go:TestB")
	filled := sheet[:i] + strings.Replace(strings.Replace(sheet[i:], "label: \n", "label: cut\n", 1), "note: \n", "note: mock only\n", 1)
	got, err := ReadSheet(strings.NewReader(filled))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "go:b_test.go:TestB" || got[0].Label != policy.Cut || got[0].Note != "mock only" || got[0].StateHash == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestReadSheetBadLabel(t *testing.T) {
	sheet := strings.Replace(sheetFor(t), "label: \n", "label: kep\n", 1)
	line := 0
	for i, l := range strings.Split(sheet, "\n") {
		if l == "label: kep" {
			line = i + 1
			break
		}
	}
	_, err := ReadSheet(strings.NewReader(sheet))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("line %d", line)) || !strings.Contains(err.Error(), "kep") {
		t.Fatalf("err = %v, want line %d and kep", err, line)
	}
}

// fill labels the named entry in a sheet.
func fill(sheet, id, label string) string {
	i := strings.Index(sheet, "## "+id)
	return sheet[:i] + strings.Replace(sheet[i:], "label: \n", "label: "+label+"\n", 1)
}

func TestApplySheetLabels(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "c.jsonl"))
	s.Import([]cases.TestCase{tc("a", "1"), tc("b", "2")})
	var buf bytes.Buffer
	WriteSheet(&buf, s.Entries())
	labels, err := ReadSheet(strings.NewReader(fill(buf.String(), "b", "cut")))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplySheet(labels, "court", now); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.Entries() {
		if (e.ID == "b") != (e.Label == policy.Cut) {
			t.Errorf("%s label = %q", e.ID, e.Label)
		}
	}
}

func TestApplySheetRejectsStaleEntry(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "c.jsonl"))
	s.Import([]cases.TestCase{tc("a", "1"), tc("b", "2")})
	var buf bytes.Buffer
	WriteSheet(&buf, s.Entries())
	s.Import([]cases.TestCase{tc("b", "2 changed")}) // b's code changed after the sheet was written
	labels, err := ReadSheet(strings.NewReader(fill(fill(buf.String(), "a", "keep"), "b", "cut")))
	if err != nil {
		t.Fatal(err)
	}
	err = s.ApplySheet(labels, "court", now)
	if err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("stale sheet accepted: %v", err)
	}
	for _, e := range s.Entries() {
		if e.Label != "" {
			t.Errorf("%s labeled %q from a rejected sheet", e.ID, e.Label)
		}
	}
}

func TestApplySheetRequiresStateHash(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "c.jsonl"))
	s.Import([]cases.TestCase{tc("a", "1")})
	labels, _ := ReadSheet(strings.NewReader("## a\nlabel: keep\n"))
	if err := s.ApplySheet(labels, "court", now); err == nil || !strings.Contains(err.Error(), "cull corpus sheet") {
		t.Fatalf("sheet without state hash accepted: %v", err)
	}
}
