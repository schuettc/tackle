package cli

import (
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

// fakeJev answers every question: the verdict gives the rubric's act option
// probability 0.9, nouls 0.1, scores 2. fail422 makes requests whose state
// mentions that string return 422; status overrides every response.
type fakeJev struct {
	n       atomic.Int32
	fail422 string
	status  int
}

func (f *fakeJev) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.n.Add(1)
		b, _ := io.ReadAll(r.Body)
		if f.status != 0 {
			w.WriteHeader(f.status)
			io.WriteString(w, `{"detail":"nope"}`)
			return
		}
		if f.fail422 != "" && strings.Contains(string(b), f.fail422) {
			w.WriteHeader(422)
			io.WriteString(w, `{"detail":"bad state"}`)
			return
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
				ans[k] = map[string]any{"type": "score", "score": 2.0, "confidence": 0.8}
			case "choice":
				var opts map[string]string
				json.Unmarshal(q.Criteria, &opts)
				probs := map[string]float64{}
				first := ""
				for o := range opts {
					probs[o] = 0.05
					if o == "cut" || o == "consolidate" {
						first = o
					}
				}
				probs[first] = 0.9
				ans[k] = map[string]any{"type": "choice", "choice": first, "probabilities": probs, "confidence": 0.85}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-test", "answers": ans})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CULL_TYPESAFE_URL", srv.URL)
}

func judgeEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv("CULL_HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", key)
}

func caseLine(id, body string) string {
	b, _ := json.Marshal(map[string]any{"id": id, "lang": "go", "framework": "testing", "name": id, "body": body})
	return string(b)
}

func groupLine(id string, members ...string) string {
	var tests []map[string]any
	for _, m := range members {
		tests = append(tests, map[string]any{"id": m, "name": m, "body": "def " + m + "(): assert f(1) == 1"})
	}
	b, _ := json.Marshal(map[string]any{"id": id, "lang": "python", "framework": "pytest", "file": "tests/test_x.py", "tests": tests})
	return string(b)
}

func lines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("output line %q: %v", l, err)
		}
		rows = append(rows, m)
	}
	return rows
}

func threeCases() string {
	return caseLine("go:a_test.go:TestA", "func TestA(){}") + "\n" + caseLine("go:b_test.go:TestB", "func TestB(){}") + "\n" + caseLine("go:c_test.go:TestC", "func TestC(){}") + "\n"
}

func TestJudgeTestsEndToEnd(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{}).start(t)
	code, out, errw := run(t, threeCases(), "judge", "--egress")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	rows := lines(t, out)
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3", len(rows))
	}
	for i, id := range []string{"go:a_test.go:TestA", "go:b_test.go:TestB", "go:c_test.go:TestC"} {
		r := rows[i]
		if r["id"] != id || r["kind"] != "test" || r["rubric"] != "test-v2" || r["verdict"] != "cut" || r["model"] != "jev-test" {
			t.Errorf("row %d = %v", i, r)
		}
	}
}

func TestJudgeGroupsEndToEnd(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{}).start(t)
	in := groupLine("group:given", "test_a", "test_b") + "\n" + groupLine("", "test_c", "test_d") + "\n"
	code, out, errw := run(t, in, "judge", "--kind", "group", "--egress")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	rows := lines(t, out)
	if len(rows) != 2 || rows[0]["id"] != "group:given" || !strings.HasPrefix(rows[1]["id"].(string), "group:") || rows[1]["id"] == "group:" {
		t.Fatalf("rows = %v", rows)
	}
	if rows[0]["kind"] != "group" || rows[0]["rubric"] != "group-v2" || rows[0]["verdict"] != "consolidate" {
		t.Errorf("row 0 = %v", rows[0])
	}
}

func TestJudgeRequiresEgress(t *testing.T) {
	judgeEnv(t, "k")
	f := &fakeJev{}
	f.start(t)
	code, _, errw := run(t, threeCases(), "judge")
	if code != 2 || !strings.Contains(errw, "api.typesafe.ai") || !strings.Contains(errw, "--egress") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests", f.n.Load())
	}
}

func TestJudgeDryRunSendsNothing(t *testing.T) {
	judgeEnv(t, "")
	f := &fakeJev{}
	f.start(t)
	code, out, errw := run(t, threeCases(), "judge", "--dry-run")
	if code != 0 || !strings.Contains(out, judge.StateNote) {
		t.Fatalf("exit %d, out %q, errw %q", code, out, errw)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests", f.n.Load())
	}
}

func TestJudgeMissingKey(t *testing.T) {
	judgeEnv(t, "")
	(&fakeJev{}).start(t)
	code, _, errw := run(t, threeCases(), "judge", "--egress")
	if code != 2 || !strings.Contains(errw, "creel exec TYPESAFE_API_KEY") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
}

func TestJudgeBadInputLine(t *testing.T) {
	judgeEnv(t, "k")
	f := &fakeJev{}
	f.start(t)
	in := caseLine("go:a_test.go:TestA", "x") + "\n{nope\n"
	code, _, errw := run(t, in, "judge", "--egress")
	if code != 2 || !strings.Contains(errw, "line 2") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests before the bad line was reported", f.n.Load())
	}
}

func TestJudgeMissingID(t *testing.T) {
	judgeEnv(t, "k")
	f := &fakeJev{}
	f.start(t)
	code, _, errw := run(t, `{"lang":"go","body":"x"}`+"\n", "judge", "--egress")
	if code != 2 || !strings.Contains(errw, "line 1") || !strings.Contains(errw, "id") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
}

func TestJudgeRejectsOneMemberGroup(t *testing.T) {
	judgeEnv(t, "k")
	f := &fakeJev{}
	f.start(t)
	code, _, errw := run(t, groupLine("g", "test_only")+"\n", "judge", "--kind", "group", "--egress")
	if code != 2 || !strings.Contains(errw, "at least 2") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
}

func TestJudgeKindMismatch(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{}).start(t)
	code, _, errw := run(t, threeCases(), "judge", "--kind", "test", "--rubric", "group-v2", "--egress")
	if code != 2 || !strings.Contains(errw, "test") || !strings.Contains(errw, "group") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
}

func TestJudgeItemErrorExit1(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{fail422: "TestB"}).start(t)
	code, out, _ := run(t, threeCases(), "judge", "--egress")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	rows := lines(t, out)
	if rows[1]["err"] == nil || rows[0]["verdict"] != "cut" || rows[2]["verdict"] != "cut" {
		t.Fatalf("rows = %v", rows)
	}
}

func TestJudgeUnauthorized(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{status: 401}).start(t)
	code, _, errw := run(t, threeCases(), "judge", "--egress")
	if code != 2 || !strings.Contains(errw, "creel exec") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
}

func TestJudgeCachesAnswers(t *testing.T) {
	judgeEnv(t, "k")
	f := &fakeJev{}
	f.start(t)
	if code, _, errw := run(t, threeCases(), "judge", "--egress"); code != 0 {
		t.Fatalf("first run: %s", errw)
	}
	f.n.Store(0)
	code, out, _ := run(t, threeCases(), "judge", "--egress")
	if code != 0 || f.n.Load() != 0 {
		t.Fatalf("second run: exit %d, %d requests", code, f.n.Load())
	}
	for _, r := range lines(t, out) {
		if r["cached"] != true {
			t.Errorf("row not cached: %v", r)
		}
	}
}

func TestJudgeReadsFileArgument(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{}).start(t)
	p := filepath.Join(t.TempDir(), "cases.jsonl")
	os.WriteFile(p, []byte(threeCases()), 0o600)
	code, out, errw := run(t, "", "judge", "--egress", p)
	if code != 0 || len(lines(t, out)) != 3 {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
}

// I1: group JSONL judged as tests would send empty test states to Jev.
func TestJudgeTestRejectsEmptyBody(t *testing.T) {
	judgeEnv(t, "k")
	f := &fakeJev{}
	f.start(t)
	code, _, errw := run(t, groupLine("g", "test_a", "test_b")+"\n", "judge", "--egress")
	if code != 2 || !strings.Contains(errw, "line 1") || !strings.Contains(errw, "--kind group") {
		t.Fatalf("exit %d, errw %q", code, errw)
	}
	if f.n.Load() != 0 {
		t.Errorf("server saw %d requests", f.n.Load())
	}
}

// I2: truncation must reach the policy and force review, for groups and tests.
func TestJudgeTruncatedGroupIsReview(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{}).start(t)
	code, out, errw := run(t, groupLine("g", "test_a", "test_b", "test_c")+"\n", "judge", "--kind", "group", "--egress", "--max-context-bytes", "70")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if r := lines(t, out)[0]; r["verdict"] != "review" || r["rule"] != "truncated" {
		t.Fatalf("row = %v", r)
	}
}

func TestJudgeTruncatedTestIsReview(t *testing.T) {
	judgeEnv(t, "k")
	(&fakeJev{}).start(t)
	b, _ := json.Marshal(map[string]any{"id": "go:a_test.go:TestA", "lang": "go", "name": "TestA", "body": "func TestA(){}", "truncated": true})
	code, out, errw := run(t, string(b)+"\n", "judge", "--egress")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errw)
	}
	if r := lines(t, out)[0]; r["verdict"] != "review" || r["rule"] != "truncated" {
		t.Fatalf("row = %v", r)
	}
}
