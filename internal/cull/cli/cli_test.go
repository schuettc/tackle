package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/schuettc/tackle/internal/cull/judge"
)

// jevServer answers every question: verdict keep (0.9), nouls 0.1, scores 2.5.
func jevServer(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		var req struct {
			Questions map[string]struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &req)
		ans := map[string]any{}
		for k, q := range req.Questions {
			switch q.Type {
			case "noul":
				ans[k] = map[string]any{"type": "noul", "noul": 0.1}
			case "score":
				ans[k] = map[string]any{"type": "score", "score": 2.5, "confidence": 0.8}
			case "choice":
				ans[k] = map[string]any{"type": "choice", "choice": "keep", "probabilities": map[string]float64{"keep": 0.9, "cut": 0.05, "review": 0.05}, "confidence": 0.9}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-test", "answers": ans})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CULL_TYPESAFE_URL", srv.URL)
	return &n
}

func setup(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CULL_HOME", home)
	t.Setenv("TYPESAFE_API_KEY", "")
	var lines []string
	for _, id := range []string{"go:a_test.go:TestA", "go:b_test.go:TestB", "go:c_test.go:TestC"} {
		b, _ := json.Marshal(map[string]any{"id": id, "lang": "go", "framework": "testing", "name": id, "body": "func " + id + "() {}"})
		lines = append(lines, string(b))
	}
	p := filepath.Join(home, "cases.jsonl")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	return p
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(""), &out, &errw)
	return code, out.String(), errw.String()
}

func importAndLabel(t *testing.T, cases string, label string) {
	t.Helper()
	if code, _, e := run(t, "corpus", "import", cases, "--repo", "fixture"); code != 0 {
		t.Fatalf("import: %d %s", code, e)
	}
	_, sheet, _ := run(t, "corpus", "sheet", "--split", "all")
	filled := strings.ReplaceAll(sheet, "label: \n", "label: "+label+"\n")
	p := filepath.Join(t.TempDir(), "sheet.md")
	os.WriteFile(p, []byte(filled), 0o600)
	if code, _, e := run(t, "label", "--sheet", p, "--by", "court"); code != 0 {
		t.Fatalf("label --sheet: %d %s", code, e)
	}
}

func TestEvalRequiresEgress(t *testing.T) {
	importAndLabel(t, setup(t), "keep")
	n := jevServer(t)
	t.Setenv("TYPESAFE_API_KEY", "k")
	code, _, e := run(t, "eval", "--split", "all")
	if code != 2 || !strings.Contains(e, "api.typesafe.ai") || !strings.Contains(e, "--egress") {
		t.Fatalf("code %d, errw %q", code, e)
	}
	if n.Load() != 0 {
		t.Errorf("server saw %d requests", n.Load())
	}
}

func TestEvalDryRunSendsNothing(t *testing.T) {
	importAndLabel(t, setup(t), "keep")
	n := jevServer(t)
	code, out, e := run(t, "eval", "--dry-run", "--split", "all")
	if code != 0 || !strings.Contains(out, judge.StateNote) {
		t.Fatalf("code %d, out %q, errw %q", code, out, e)
	}
	if n.Load() != 0 {
		t.Errorf("server saw %d requests", n.Load())
	}
}

func TestEvalMissingKey(t *testing.T) {
	importAndLabel(t, setup(t), "keep")
	jevServer(t)
	code, _, e := run(t, "eval", "--egress", "--split", "all")
	if code != 2 || !strings.Contains(e, "creel exec TYPESAFE_API_KEY") {
		t.Fatalf("code %d, errw %q", code, e)
	}
}

func TestImportLabelEvalFlow(t *testing.T) {
	importAndLabel(t, setup(t), "keep")
	n := jevServer(t)
	t.Setenv("TYPESAFE_API_KEY", "k")
	code, out, e := run(t, "eval", "--egress", "--split", "all", "--json")
	if code != 0 {
		t.Fatalf("eval: %d %s", code, e)
	}
	var res struct {
		Metrics struct {
			N int `json:"n"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("eval --json output: %v\n%s", err, out)
	}
	if res.Metrics.N != 3 || n.Load() != 3 {
		t.Fatalf("metrics.n = %d, requests = %d, want 3, 3", res.Metrics.N, n.Load())
	}
	// A second run against itself is served from cache and reports no changes.
	code, out, _ = run(t, "eval", "--egress", "--split", "all", "--against", "v1")
	if code != 0 || n.Load() != 3 || !strings.Contains(out, "changed: 0") {
		t.Fatalf("against run: code %d, requests %d, out:\n%s", code, n.Load(), out)
	}
}

func TestLabelSheetAllOrNothing(t *testing.T) {
	cases := setup(t)
	run(t, "corpus", "import", cases)
	_, sheet, _ := run(t, "corpus", "sheet", "--split", "all")
	filled := strings.Replace(sheet, "label: \n", "label: keep\n", 1)
	filled = strings.Replace(filled, "label: \n", "label: kep\n", 1)
	p := filepath.Join(t.TempDir(), "sheet.md")
	os.WriteFile(p, []byte(filled), 0o600)
	if code, _, _ := run(t, "label", "--sheet", p); code == 0 {
		t.Fatal("bad sheet accepted")
	}
	_, out, _ := run(t, "corpus", "stats", "--json")
	var stats map[string]map[string]int
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	for split, byLabel := range stats {
		for label, n := range byLabel {
			if label != "unlabeled" && n > 0 {
				t.Errorf("%s has %d %s after a rejected sheet", split, n, label)
			}
		}
	}
}
