package check

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ciProbe = "name: ci\non: push\njobs:\n  probe:\n    runs-on: ubuntu-latest\n    steps:\n      - run: node web/probe.mjs\n      - run: go test ./...\n"

func speedFixture(t *testing.T, dir string) {
	t.Helper()
	goModule(t, dir)
	withEgress(t, dir)
	writeFile(t, dir, ".github/workflows/ci.yml", ciProbe)
	writeFile(t, dir, "web/probe.mjs", "export async function go(pg) {\n  await pg.waitForTimeout(500);\n}\n")
	writeFile(t, dir, "pkg/calc_test.go",
		"package pkg\n\nimport \"time\"\n\nfunc TestA(t *testing.T) {\n\ttime.Sleep(2 * time.Second)\n}\n\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n")
}

// The scan covers the discovered test files and the script checks, names the
// enclosing test, and is recorded in the report with the checks it came from.
func TestCheckSpeedScanAndChecks(t *testing.T) {
	dir := t.TempDir()
	speedFixture(t, dir)
	report, err := Run(context.Background(), nil, Options{Path: dir, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Speed) != 2 {
		t.Fatalf("speed = %+v", report.Speed)
	}
	byFile := map[string]string{}
	for _, f := range report.Speed {
		byFile[f.File] = f.Test
		if f.Seconds == nil {
			t.Errorf("no seconds: %+v", f)
		}
	}
	if byFile["pkg/calc_test.go"] != "go:pkg/calc_test.go:TestA" {
		t.Errorf("tests = %v", byFile)
	}
	if _, ok := byFile["web/probe.mjs"]; !ok || byFile["web/probe.mjs"] != "" {
		t.Errorf("script check not scanned (or given a test): %v", byFile)
	}
	if report.Summary["speed"] != 2 {
		t.Errorf("summary = %v", report.Summary)
	}
	kinds := map[string]bool{}
	for _, c := range report.Checks {
		kinds[c.Kind] = true
	}
	if !kinds["script"] || !kinds["go"] {
		t.Errorf("checks = %+v", report.Checks)
	}
}

func TestCheckSpeedInLastJSON(t *testing.T) {
	dir := t.TempDir()
	speedFixture(t, dir)
	f := &fakeEval{}
	if _, err := Run(context.Background(), f, Options{Path: dir, Stderr: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".cull", "last.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Checks  []map[string]any `json:"checks"`
		Speed   []map[string]any `json:"speed"`
		Summary map[string]int   `json:"summary"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Checks) < 2 || len(got.Speed) != 2 || got.Summary["speed"] != 2 {
		t.Errorf("last.json: checks=%d speed=%d summary=%v", len(got.Checks), len(got.Speed), got.Summary)
	}
}

// Diff mode scans only the files the diff touched.
func TestCheckSpeedDiffScansTouchedFilesOnly(t *testing.T) {
	dir, cfg := newGitRepo(t)
	speedFixture(t, dir)
	writeFile(t, dir, "other/other_test.go", "package other\n\nimport \"time\"\n\nfunc TestO(t *testing.T) {\n\ttime.Sleep(3 * time.Second)\n}\n")
	runGit(t, dir, cfg, "add", ".")
	runGit(t, dir, cfg, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGit(t, dir, cfg, "rev-parse", "HEAD"))
	writeFile(t, dir, "pkg/calc_test.go",
		"package pkg\n\nimport \"time\"\n\nfunc TestA(t *testing.T) {\n\ttime.Sleep(2 * time.Second)\n\t_ = 1\n}\n\nfunc TestB(t *testing.T) {\n\t_ = 2\n}\n")
	report, err := Run(context.Background(), nil, Options{Path: dir, Diff: base, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Speed) != 1 || report.Speed[0].File != "pkg/calc_test.go" {
		t.Fatalf("speed = %+v, want only the touched test file (not other/, not the untouched script)", report.Speed)
	}
	if len(report.Checks) < 2 {
		t.Errorf("checks are listed in diff mode too: %+v", report.Checks)
	}
}
