package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// checkFake answers Jev requests without a network call. boost maps a
// substring that may appear in the request body (a test or group member
// name) to the act option ("cut" or "consolidate") that request should get;
// anything else gets the non-act option (keep / keep_separate).
type checkFake struct {
	n      atomic.Int32
	boost  map[string]string
	status int
}

func (f *checkFake) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.n.Add(1)
		b, _ := io.ReadAll(r.Body)
		if f.status != 0 {
			w.WriteHeader(f.status)
			io.WriteString(w, `{"detail":"nope"}`)
			return
		}
		body := string(b)
		act := ""
		for marker, opt := range f.boost {
			if strings.Contains(body, marker) {
				act = opt
			}
		}
		var req struct {
			Questions map[string]struct {
				Type     string          `json:"type"`
				Criteria json.RawMessage `json:"criteria"`
			} `json:"questions"`
		}
		json.Unmarshal(b, &req)
		ans := map[string]any{}
		for k, q := range req.Questions {
			switch q.Type {
			case "noul":
				ans[k] = map[string]any{"type": "noul", "noul": 0.1}
			case "score":
				ans[k] = map[string]any{"type": "score", "score": 1.0, "confidence": 0.8}
			case "choice":
				var opts map[string]string
				json.Unmarshal(q.Criteria, &opts)
				chosen := act
				if chosen == "" {
					for o := range opts {
						if o != "cut" && o != "consolidate" && o != "review" {
							chosen = o
						}
					}
				}
				probs := map[string]float64{}
				for o := range opts {
					probs[o] = 0.05
				}
				probs[chosen] = 0.9
				ans[k] = map[string]any{"type": "choice", "choice": chosen, "probabilities": probs, "confidence": 0.85}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-test", "answers": ans})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CULL_TYPESAFE_URL", srv.URL)
}

func checkEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", key)
}

func ckWriteFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	p := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ckGoModule(t *testing.T, root string) {
	t.Helper()
	ckWriteFile(t, root, "go.mod", "module example.com/fixture\n\ngo 1.22\n")
}

func ckEgress(t *testing.T, root string) {
	t.Helper()
	ckWriteFile(t, root, ".cull.toml", "egress = true\n")
}

func ckGitRepo(t *testing.T) (dir, gitconfig string) {
	t.Helper()
	dir = t.TempDir()
	gitconfig = filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Cull Test\n\temail = cull-test@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ckRunGit(t, dir, gitconfig, "init", "-q")
	return dir, gitconfig
}

func ckRunGit(t *testing.T, dir, gitconfig string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitconfig, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func oneTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ckGoModule(t, root)
	ckEgress(t, root)
	ckWriteFile(t, root, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	return root
}

func TestCheckRequiresEgress(t *testing.T) {
	checkEnv(t, "k")
	f := &checkFake{}
	f.start(t)
	root := t.TempDir()
	ckGoModule(t, root)
	ckWriteFile(t, root, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")

	code, _, errw := run(t, "", "check", root)
	if code != 2 || !strings.Contains(errw, "api.typesafe.ai") || !strings.Contains(errw, "egress = true") {
		t.Fatalf("code %d, errw %q", code, errw)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests", f.n.Load())
	}
}

func TestCheckMissingKey(t *testing.T) {
	checkEnv(t, "")
	f := &checkFake{}
	f.start(t)
	root := oneTestRepo(t)

	code, _, errw := run(t, "", "check", root)
	if code != 2 || !strings.Contains(errw, "creel exec TYPESAFE_API_KEY") {
		t.Fatalf("code %d, errw %q", code, errw)
	}
}

func TestCheckUnauthorized(t *testing.T) {
	checkEnv(t, "k")
	(&checkFake{status: 401}).start(t)
	root := oneTestRepo(t)

	code, _, errw := run(t, "", "check", root)
	if code != 2 || !strings.Contains(errw, "creel exec") {
		t.Fatalf("code %d, errw %q", code, errw)
	}
}

func TestCheckDryRunPrintsStates(t *testing.T) {
	checkEnv(t, "")
	f := &checkFake{}
	f.start(t)
	root := t.TempDir()
	ckGoModule(t, root)
	ckWriteFile(t, root, "pkg/calc_test.go", "package pkg\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")

	code, _, errw := run(t, "", "check", root, "--dry-run")
	if code != 0 {
		t.Fatalf("code %d, errw %q", code, errw)
	}
	if !strings.Contains(errw, `"test_name":"TestA"`) {
		t.Errorf("errw = %q, want a printed state for TestA", errw)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests during --dry-run", f.n.Load())
	}
}

func TestCheckBadDiffBase(t *testing.T) {
	checkEnv(t, "k")
	f := &checkFake{}
	f.start(t)
	dir, cfg := ckGitRepo(t)
	ckGoModule(t, dir)
	ckEgress(t, dir)
	ckWriteFile(t, dir, "a_test.go", "package a\n\nfunc TestA(t *testing.T) {\n\t_ = 1\n}\n")
	ckRunGit(t, dir, cfg, "add", ".")
	ckRunGit(t, dir, cfg, "commit", "-q", "-m", "base")

	code, _, errw := run(t, "", "check", dir, "--diff", "not-a-real-base-ref")
	if code != 2 || !strings.Contains(errw, "not-a-real-base-ref") {
		t.Fatalf("code %d, errw %q", code, errw)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests on a bad diff base", f.n.Load())
	}
}

func TestCheckExitCodes(t *testing.T) {
	t.Run("all keep", func(t *testing.T) {
		checkEnv(t, "k")
		(&checkFake{}).start(t)
		root := oneTestRepo(t)
		code, _, errw := run(t, "", "check", root)
		if code != 0 {
			t.Fatalf("code %d, errw %q", code, errw)
		}
	})
	t.Run("one cut", func(t *testing.T) {
		checkEnv(t, "k")
		(&checkFake{boost: map[string]string{"TestA": "cut"}}).start(t)
		root := oneTestRepo(t)
		code, out, errw := run(t, "", "check", root)
		if code != 1 {
			t.Fatalf("code %d, out %q, errw %q", code, out, errw)
		}
	})
	t.Run("one consolidate", func(t *testing.T) {
		checkEnv(t, "k")
		(&checkFake{boost: map[string]string{"TestA": "consolidate"}}).start(t)
		root := t.TempDir()
		ckGoModule(t, root)
		ckEgress(t, root)
		ckWriteFile(t, root, "pkg/calc_test.go",
			"package pkg\n\n"+
				"func TestA(t *testing.T) {\n\tx := 1\n\ty := 2\n\tz := 3\n\tw := 4\n\tv := 5\n\tu := 6\n\t_ = u\n}\n\n"+
				"func TestB(t *testing.T) {\n\tx := 1\n\ty := 2\n\tz := 3\n\tw := 4\n\tv := 5\n\tu := 7\n\t_ = u\n}\n")
		code, out, errw := run(t, "", "check", root)
		if code != 1 {
			t.Fatalf("code %d, out %q, errw %q", code, out, errw)
		}
	})
}

func TestCheckJSONOutput(t *testing.T) {
	checkEnv(t, "k")
	(&checkFake{}).start(t)
	root := oneTestRepo(t)
	code, out, errw := run(t, "", "check", root, "--json")
	if code != 0 {
		t.Fatalf("code %d, errw %q", code, errw)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("--json output not valid JSON: %v\n%s", err, out)
	}
	if report["mode"] != "suite" {
		t.Errorf("report = %v", report)
	}
}
