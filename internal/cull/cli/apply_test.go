package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/cull/apply"
	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/extract"
)

const applyCalc = "package calc\n\nfunc Add(a, b int) int { return a + b }\n"

const applyCalcTest = `package calc

import (
	"strings"
	"testing"
)

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}

func TestUpper(t *testing.T) {
	if strings.ToUpper("a") != "A" {
		t.Fatal("bad")
	}
}

func TestUsesAdd(t *testing.T) {
	TestAdd(t)
}
`

// applyProject makes a temp Go module whose .cull.toml sets test_command,
// writes a real .cull/last.json (extracted ids, hashes, spans) with the
// named tests judged cut, and chdirs into it.
func applyProject(t *testing.T, cut ...string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
	writeFixture(t, root, ".cull.toml", "test_command = \"go test ./...\"\n")
	writeFixture(t, root, "calc.go", applyCalc)
	writeFixture(t, root, "calc_test.go", applyCalcTest)

	cutSet := map[string]bool{}
	for _, c := range cut {
		cutSet[c] = true
	}
	rel := "calc_test.go"
	res, err := extract.ForFile(rel).Extract(root, []string{rel}, 24000)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(applyCalcTest))
	fi := check.FileInfo{SHA256: hex.EncodeToString(sum[:])}
	r := check.Report{Root: root, Mode: "suite", Files: map[string]check.FileInfo{}}
	for _, c := range res.Cases {
		v := "keep"
		if cutSet[c.Name] {
			v = "cut"
		}
		r.Tests = append(r.Tests, check.TestResult{TestCase: c, Verdict: v})
		fi.Tests = append(fi.Tests, check.FileTest{ID: c.ID, Hash: c.Hash})
	}
	r.Files[rel] = fi
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".cull"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cull", "last.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

func calcTestNow(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "calc_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyExitCodes(t *testing.T) {
	t.Run("0 applied and verified", func(t *testing.T) {
		root := applyProject(t, "TestUpper")
		code, out, errw := run(t, "", "apply", "--verdict", "cut")
		if code != 0 {
			t.Fatalf("code %d, errw %q, out %q", code, errw, out)
		}
		if strings.Contains(calcTestNow(t, root), "TestUpper") {
			t.Error("TestUpper not removed")
		}
		for _, want := range []string{"go:calc_test.go:TestUpper", "calc_test.go", "strings", "go test ./...", "snapshot"} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("0 json", func(t *testing.T) {
		applyProject(t, "TestUpper")
		code, out, errw := run(t, "", "apply", "--ids", "go:calc_test.go:TestUpper", "--json")
		if code != 0 {
			t.Fatalf("code %d, errw %q", code, errw)
		}
		var got struct {
			Applied        []string            `json:"applied"`
			ImportsRemoved map[string][]string `json:"imports_removed"`
			After          []struct {
				Command string `json:"command"`
				OK      bool   `json:"ok"`
			} `json:"after"`
			RolledBack bool   `json:"rolled_back"`
			Snapshot   string `json:"snapshot"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(got.Applied) != 1 || len(got.After) != 1 || !got.After[0].OK || got.RolledBack || got.Snapshot == "" {
			t.Errorf("json = %+v", got)
		}
	})
	t.Run("1 rolled back", func(t *testing.T) {
		root := applyProject(t, "TestAdd")
		code, out, errw := run(t, "", "apply", "--verdict", "cut")
		if code != 1 {
			t.Fatalf("code %d, errw %q, out %q", code, errw, out)
		}
		if calcTestNow(t, root) != applyCalcTest {
			t.Error("not restored byte-for-byte")
		}
		if !strings.Contains(out, "undefined: TestAdd") {
			t.Errorf("output should show the failing tail:\n%s", out)
		}
		if !strings.Contains(errw, "rolled back") || !strings.Contains(errw, filepath.Join(".cull", "rollback")) {
			t.Errorf("errw should name the rollback and snapshot: %q", errw)
		}
	})
	t.Run("2 refused", func(t *testing.T) {
		root := applyProject(t, "TestUpper")
		code, out, errw := run(t, "", "apply", "--ids", "go:calc_test.go:TestUpper", "go:calc_test.go:TestNope")
		if code != 2 {
			t.Fatalf("code %d, errw %q", code, errw)
		}
		if !strings.Contains(out, "go:calc_test.go:TestNope") || !strings.Contains(out, "not in last.json") {
			t.Errorf("output should list the refusal:\n%s", out)
		}
		if calcTestNow(t, root) != applyCalcTest {
			t.Error("file touched")
		}
	})
	t.Run("2 no last.json", func(t *testing.T) {
		t.Chdir(t.TempDir())
		code, _, errw := run(t, "", "apply", "--verdict", "cut")
		if code != 2 || !strings.Contains(errw, "run cull check first") {
			t.Errorf("code %d, errw %q", code, errw)
		}
	})
}

func TestWriteOutcomeRollbackFailed(t *testing.T) {
	var b strings.Builder
	writeOutcome(&b, apply.Outcome{
		Applied:        []string{"go:a_test.go:TestA"},
		Files:          []string{"a_test.go", "b_test.go"},
		RollbackFailed: true,
		Snapshot:       "/p/.cull/rollback/20260927T000000Z",
	})
	out := b.String()
	if strings.Contains(out, "every file restored") {
		t.Errorf("claims a restore that failed:\n%s", out)
	}
	for _, want := range []string{"NOT", "a_test.go", "b_test.go", "/p/.cull/rollback/20260927T000000Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	js, err := json.Marshal(apply.Outcome{RollbackFailed: true})
	if err != nil || !strings.Contains(string(js), `"rollback_failed":true`) || !strings.Contains(string(js), `"rolled_back":false`) {
		t.Errorf("json = %s, %v", js, err)
	}
}

func TestApplyRequiresIDsOrVerdict(t *testing.T) {
	root := applyProject(t, "TestUpper")
	for _, args := range [][]string{
		{"apply"},
		{"apply", "--ids", "go:calc_test.go:TestUpper", "--verdict", "cut"},
		{"apply", "--verdict", "keep"},
		{"apply", "go:calc_test.go:TestUpper"},
	} {
		code, _, errw := run(t, "", args...)
		if code != 2 {
			t.Errorf("%v: code %d, errw %q", args, code, errw)
		}
	}
	if calcTestNow(t, root) != applyCalcTest {
		t.Error("file touched")
	}
}

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestApplySignalsRestore: SIGTERM and SIGHUP during the after-run kill
// the test command and restore every file (exit 2), like SIGINT (I-5).
func TestApplySignalsRestore(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			root := applyProject(t, "TestUpper")
			writeFixture(t, root, ".cull.toml",
				"test_command = 'if [ -d .cull/rollback ]; then : > started; sleep 60; fi'\n")
			go func() {
				for i := 0; i < 600; i++ {
					if _, err := os.Stat(filepath.Join(root, "started")); err == nil {
						_ = syscall.Kill(os.Getpid(), sig)
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
			}()
			code, _, errw := run(t, "", "apply", "--verdict", "cut")
			if code != 2 || !strings.Contains(errw, "interrupted") {
				t.Fatalf("code %d, errw %q", code, errw)
			}
			if calcTestNow(t, root) != applyCalcTest {
				t.Error("not restored byte-for-byte")
			}
		})
	}
}

// TestApplyJSONSnapshotOnStderr: in --json mode the snapshot path still
// reaches stderr as soon as it is written (I-5).
func TestApplyJSONSnapshotOnStderr(t *testing.T) {
	applyProject(t, "TestUpper")
	code, out, errw := run(t, "", "apply", "--verdict", "cut", "--json", "--no-verify")
	if code != 0 {
		t.Fatalf("code %d, errw %q", code, errw)
	}
	var got struct {
		Snapshot string `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Snapshot == "" || !strings.Contains(errw, got.Snapshot) {
		t.Errorf("errw %q should name snapshot %q", errw, got.Snapshot)
	}
}
