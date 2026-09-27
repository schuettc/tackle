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
	want := []SheetLabel{{ID: "go:b_test.go:TestB", Label: policy.Cut, Note: "mock only"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %+v, want %+v", got, want)
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
