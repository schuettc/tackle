package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/docket/config"
	"github.com/schuettc/tackle/internal/docket/observe"
	"github.com/schuettc/tackle/internal/docket/testgit"
)

type fakeGh struct{}

func (fakeGh) Gh(_ context.Context, args ...string) ([]byte, error) {
	j := strings.Join(args, " ")
	switch {
	case j == "api user --jq .login":
		return []byte("schuettc"), nil
	case strings.HasPrefix(j, "api user/orgs"), strings.HasPrefix(j, "auth status"):
		return nil, nil
	case strings.Contains(j, "repositoryOwner("):
		return []byte(`{"data":{"repositoryOwner":{"repositories":{"pageInfo":{"hasNextPage":false},"nodes":[
		 {"nameWithOwner":"schuettc/hail","pushedAt":"2026-09-20T00:00:00Z","defaultBranchRef":{"name":"main"},
		  "pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[]},"issues":{"pageInfo":{"hasNextPage":false},"nodes":[]}}]}}}}`), nil
	case strings.Contains(j, "search("):
		return []byte(`{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}`), nil
	case strings.Contains(j, "k0:"):
		return []byte(`{"data":{}}`), nil
	}
	return nil, &observe.GhError{Code: 1, Stderr: "unexpected " + j}
}

type env struct {
	t      *testing.T
	root   string
	clone  string
	remote string
}

func setup(t *testing.T) *env {
	t.Helper()
	testgit.Env(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCKET_HOME", filepath.Join(home, "lh"))
	newRunner = func() observe.Runner { return fakeGh{} }
	executable = func() string { return "/usr/bin/true" } // hook shims must never re-run the test binary
	t.Cleanup(func() {
		newRunner = func() observe.Runner { return observe.ExecRunner{} }
		executable = defaultExecutable
	})
	e := &env{t: t, remote: testgit.NewBare(t)}
	e.root, _ = filepath.EvalSymlinks(t.TempDir())
	e.clone = filepath.Join(e.root, "hail")
	os.MkdirAll(e.clone, 0o755)
	testgit.Git(t, e.clone, "init", "-q", "-b", "main")
	c := testgit.Commit(t, e.clone, "a", "1")
	testgit.Git(t, e.clone, "remote", "add", "origin", "https://github.com/schuettc/hail.git")
	testgit.Git(t, e.clone, "update-ref", "refs/remotes/origin/main", c)
	testgit.Git(t, e.clone, "switch", "-q", "-c", "feat/client")
	testgit.Commit(t, e.clone, "b", "2")
	return e
}

func (e *env) run(stdin string, args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errw)
	return code, out.String(), errw.String()
}

func (e *env) ok(args ...string) string {
	e.t.Helper()
	code, out, errw := e.run("", args...)
	if code != 0 {
		e.t.Fatalf("docket %v: exit %d\n%s%s", args, code, out, errw)
	}
	return out
}

func TestHarnessEntryPointsSilentWhenUninitialized(t *testing.T) {
	testgit.Env(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKET_HOME", filepath.Join(t.TempDir(), "missing", "deeper"))
	e := &env{t: t}
	payload := `{"tool_name":"Bash","cwd":"/w","tool_input":{"command":"git push"}}`
	for _, args := range [][]string{{"record", "--harness", "claude"}, {"brief"}, {"brief", "--cwd", "/nonexistent"}, {"hook", "post-commit"}, {"hook"}} {
		code, out, errw := e.run(payload, args...)
		if code != 0 || out != "" || errw != "" {
			t.Errorf("docket %v: exit %d out %q err %q", args, code, out, errw)
		}
	}
	if _, err := os.Stat(config.SpoolDir()); err == nil {
		b, _ := os.ReadDir(config.SpoolDir())
		for _, f := range b {
			if strings.HasPrefix(f.Name(), "events") {
				t.Error("record spooled on an uninitialized machine")
			}
		}
	}
}

func TestUninitializedCommandsHint(t *testing.T) {
	testgit.Env(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKET_HOME", t.TempDir())
	e := &env{t: t}
	code, _, errw := e.run("", "attention")
	if code != 1 || !strings.Contains(errw, "docket init") {
		t.Fatalf("exit %d: %s", code, errw)
	}
}

func TestEndToEnd(t *testing.T) {
	e := setup(t)
	if out := e.ok("init", e.remote, "--machine", "mbp", "--user", "schuettc", "--root", e.root); !strings.Contains(out, "mbp") {
		t.Errorf("init output %q", out)
	}
	if out := e.ok("decide", "repo:schuettc/hail", "keep", "--note", "active"); !strings.Contains(out, "; pushed") {
		t.Errorf("decide output %q", out)
	}
	if code, _, errw := e.run("", "decide", "repo:schuettc/hail", "merge"); code != 1 || !strings.Contains(errw, "not valid") {
		t.Errorf("invalid decide: %d %q", code, errw)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(e.ok("sync", "--json")), &rep); err != nil || rep["committed"] != true {
		t.Fatalf("sync json %v %v", rep, err)
	}
	var att struct {
		Items []struct {
			Key    string `json:"key"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(e.ok("attention", "--json")), &att); err != nil || len(att.Items) != 1 || att.Items[0].Key != "branch:schuettc/hail@feat/client" {
		t.Fatalf("attention %+v %v", att, err)
	}
	if out := e.ok("show", "repo:Schuettc/Hail"); !strings.Contains(out, "keep") || !strings.Contains(out, "active") || !strings.Contains(out, "done") {
		t.Errorf("show:\n%s", out)
	}
	if out := e.ok("brief", "--cwd", e.clone); !strings.Contains(out, "branch:schuettc/hail@feat/client") {
		t.Errorf("brief:\n%s", out)
	}
	if out := e.ok("brief", "--cwd", t.TempDir()); out != "" {
		t.Errorf("brief outside any repo: %q", out)
	}
	sheet := e.ok("triage", "--out", "-")
	i := strings.Index(sheet, `key = "branch:schuettc/hail@feat/client"`)
	if i < 0 {
		t.Fatalf("triage sheet:\n%s", sheet)
	}
	sheet = sheet[:i] + strings.Replace(sheet[i:], `disposition = ""`, `disposition = "keep"`, 1)
	f := filepath.Join(t.TempDir(), "t.toml")
	os.WriteFile(f, []byte(sheet), 0o644)
	if out := e.ok("decide", "--from", f); !strings.Contains(out, "1 decision") {
		t.Errorf("decide --from: %q", out)
	}
	if out := e.ok("validate"); !strings.Contains(out, "ok") {
		t.Errorf("validate %q", out)
	}
	if out := e.ok("history", "repo:schuettc/hail"); !strings.Contains(out, "decide repo:schuettc/hail") {
		t.Errorf("history:\n%s", out)
	}
	e.ok("hooks", "install")
	var hs map[string]any
	json.Unmarshal([]byte(e.ok("hooks", "status", "--json")), &hs)
	if hs["installed"] != true {
		t.Errorf("hooks status %v", hs)
	}
	if out := e.ok("doctor"); !strings.Contains(out, "hooks") {
		t.Errorf("doctor:\n%s", out)
	}
	e.ok("hooks", "uninstall")
	if code, out, _ := e.run("", "commands", "--json"); code != 0 || !strings.Contains(out, `"record"`) || !strings.Contains(out, `"hook"`) {
		t.Errorf("commands: %d %s", code, out)
	}
	if _, out, _ := e.run("", "help", "show"); !strings.Contains(out, "Usage: docket show <key>") {
		t.Errorf("help show usage: %q", out)
	}
}
