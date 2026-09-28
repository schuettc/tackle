package apply

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/check"
	tools "github.com/schuettc/tools-common"
)

func writeLastJSON(t *testing.T, root string, r check.Report) {
	t.Helper()
	dir := filepath.Join(root, ".cull")
	if err := tools.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "last.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissing(t *testing.T) {
	root := t.TempDir()
	_, err := Load(root)
	if err == nil {
		t.Fatal("want error for missing last.json")
	}
	want := "no .cull/last.json; run cull check first"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := check.Report{
		Root: root,
		Mode: "suite",
		Tests: []check.TestResult{
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestA", File: "a_test.go", Hash: "sha256:x"}, Verdict: "cut"},
		},
		Files: map[string]check.FileInfo{
			"a_test.go": {SHA256: "deadbeef", Tests: []check.FileTest{{ID: "go:a_test.go:TestA", Hash: "sha256:x"}}},
		},
	}
	writeLastJSON(t, root, want)

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tests) != 1 || got.Tests[0].ID != "go:a_test.go:TestA" || got.Tests[0].Verdict != "cut" {
		t.Fatalf("got.Tests = %+v", got.Tests)
	}
	if got.Files["a_test.go"].SHA256 != "deadbeef" {
		t.Fatalf("got.Files = %+v", got.Files)
	}
}

func TestSelectVerdictCut(t *testing.T) {
	r := check.Report{
		Tests: []check.TestResult{
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestCut", File: "a_test.go"}, Verdict: "cut"},
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestNested", File: "a_test.go", Parent: "TestParent"}, Verdict: "cut"},
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestKeep", File: "a_test.go"}, Verdict: "keep"},
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestReview", File: "a_test.go"}, Verdict: "review"},
		},
	}
	targets, refused, needsAgent := Select(r, nil, true)
	if len(targets) != 1 || targets[0].ID != "go:a_test.go:TestCut" {
		t.Fatalf("targets = %+v", targets)
	}
	if len(refused) != 0 {
		t.Fatalf("refused = %+v, want none", refused)
	}
	if len(needsAgent) != 1 || needsAgent[0].ID != "go:a_test.go:TestNested" || !strings.Contains(needsAgent[0].Reason, "edit it by hand") {
		t.Fatalf("needsAgent = %+v", needsAgent)
	}
}

func TestSelectIDs(t *testing.T) {
	r := check.Report{
		Tests: []check.TestResult{
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestA", File: "a_test.go"}, Verdict: "keep"},
			{TestCase: cases.TestCase{ID: "go:a_test.go:TestNested", File: "a_test.go", Parent: "TestParent"}, Verdict: "review"},
		},
	}
	targets, refused, needsAgent := Select(r, []string{"go:a_test.go:TestA", "go:a_test.go:TestMissing", "go:a_test.go:TestNested"}, false)
	if len(needsAgent) != 0 {
		t.Fatalf("needsAgent = %+v, want none", needsAgent)
	}
	if len(targets) != 1 || targets[0].ID != "go:a_test.go:TestA" {
		t.Fatalf("targets = %+v", targets)
	}
	if len(refused) != 2 {
		t.Fatalf("refused = %+v, want 2", refused)
	}
	byID := map[string]string{}
	for _, r := range refused {
		byID[r.ID] = r.Reason
	}
	if byID["go:a_test.go:TestMissing"] != "not in last.json" {
		t.Errorf("missing reason = %q", byID["go:a_test.go:TestMissing"])
	}
	want := "go:a_test.go:TestNested is inside TestParent; edit it by hand, then run cull check"
	if byID["go:a_test.go:TestNested"] != want {
		t.Errorf("nested reason = %q, want %q", byID["go:a_test.go:TestNested"], want)
	}
}

func TestPreflightFileChanged(t *testing.T) {
	root := t.TempDir()
	body := "func TestA(t *testing.T) {}\n"
	full := "package a\n\n" + body
	span := cases.Span{Start: len("package a\n\n"), End: len(full)}
	if err := os.WriteFile(filepath.Join(root, "a_test.go"), []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := cases.HashBody(body)
	sumHex := sha256Hex(t, []byte(full))
	r := check.Report{
		Files: map[string]check.FileInfo{
			"a_test.go": {SHA256: sumHex, Tests: []check.FileTest{{ID: "go:a_test.go:TestA", Hash: hash}}},
		},
	}
	target := Target{ID: "go:a_test.go:TestA", File: "a_test.go", Lang: "go", Hash: hash, Span: span}

	if refused := Preflight(root, r, []Target{target}); len(refused) != 0 {
		t.Fatalf("unchanged file refused: %+v", refused)
	}

	// Now change the file: preflight must refuse.
	if err := os.WriteFile(filepath.Join(root, "a_test.go"), []byte(full+"\n// extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refused := Preflight(root, r, []Target{target})
	if len(refused) != 1 {
		t.Fatalf("refused = %+v, want 1", refused)
	}
	want := "file changed since cull check; run cull check again"
	if refused[0].Reason != want {
		t.Errorf("reason = %q, want %q", refused[0].Reason, want)
	}
}

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	h := cases.HashBody(string(data))
	// cases.HashBody prefixes "sha256:"; report's file sha256 does not.
	return h[len("sha256:"):]
}
