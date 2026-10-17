package npm

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fake sets up the fake npm with script and returns a Runner on it, the
// file its argv is recorded in and the URLs Open was given.
func fake(t *testing.T, script string) (*Runner, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	sf := filepath.Join(dir, "script")
	if err := os.WriteFile(sf, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	args := filepath.Join(dir, "args")
	t.Setenv("FAKE_NPM_SCRIPT", sf)
	t.Setenv("FAKE_NPM_ARGS", args)
	t.Setenv("FAKE_NPM_MARKS", filepath.Join(dir, "marks"))
	bin, err := filepath.Abs("testdata/fake-npm")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var opened []string
	r := &Runner{NPM: bin, LogDir: filepath.Join(dir, "logs"), Open: func(u string) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, u)
		return nil
	}}
	return r, args, &opened
}

type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) add(e Event) { r.mu.Lock(); r.events = append(r.events, e); r.mu.Unlock() }

func TestRunOneApproval(t *testing.T) {
	r, args, opened := fake(t, "print npm notice Publishing to https://registry.npmjs.org/ with tag latest and public access\n"+
		"print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/u1\nsleep 0.2\nprint + demo-pkg@1.0.0\nexit 0\n")
	var rec recorder
	res, err := r.Run(context.Background(), "publish", "@demo/pkg", []string{"publish", "/tmp/demo-pkg-1.0.0.tgz"}, rec.add)
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{{Kind: ApprovalNeeded, URL: "https://www.npmjs.com/auth/cli/u1"}, {Kind: ApprovalGiven, URL: "https://www.npmjs.com/auth/cli/u1"}}
	if !reflect.DeepEqual(rec.events, want) {
		t.Errorf("events %+v", rec.events)
	}
	if !reflect.DeepEqual(*opened, []string{"https://www.npmjs.com/auth/cli/u1"}) {
		t.Errorf("opened %v", *opened)
	}
	if res.ExitCode != 0 || res.Code != "" || !strings.Contains(res.Output, "+ demo-pkg@1.0.0") || strings.Contains(res.Output, "\r") {
		t.Errorf("result %+v", res)
	}
	b, _ := os.ReadFile(args)
	if !strings.HasSuffix(strings.TrimSpace(string(b)), "--browser=false") {
		t.Errorf("argv %q", b)
	}
	log, err := os.ReadFile(res.Log)
	if err != nil || !strings.Contains(string(log), "+ demo-pkg@1.0.0") {
		t.Errorf("log %s: %v", res.Log, err)
	}
	if !strings.HasSuffix(res.Log, "-publish-demo-pkg.log") {
		t.Errorf("log name %s", res.Log)
	}
}

func TestRunTwoApprovals(t *testing.T) {
	r, _, opened := fake(t, "print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/u1\nsleep 0.1\n"+
		"print type: github\nprint Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/u2\nsleep 0.1\nexit 0\n")
	var rec recorder
	if _, err := r.Run(context.Background(), "trust", "demo-pkg", []string{"trust", "list", "demo-pkg"}, rec.add); err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{Kind: ApprovalNeeded, URL: "https://www.npmjs.com/auth/cli/u1"}, {Kind: ApprovalGiven, URL: "https://www.npmjs.com/auth/cli/u1"},
		{Kind: ApprovalNeeded, URL: "https://www.npmjs.com/auth/cli/u2"}, {Kind: ApprovalGiven, URL: "https://www.npmjs.com/auth/cli/u2"},
	}
	if !reflect.DeepEqual(rec.events, want) {
		t.Errorf("events %+v", rec.events)
	}
	if len(*opened) != 2 {
		t.Errorf("opened %v", *opened)
	}
}

// npm deprecate prints a line per version before it asks; an approval left
// unanswered ends in E404 on -/v1/done: never reported as given.
func TestRunExpiredApproval(t *testing.T) {
	r, _, _ := fake(t, "print deprecating demo-pkg@1.0.0\nprint deprecating demo-pkg@1.1.0\n"+
		"print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/u1\n"+
		"print npm error code E404\nprint npm error 404 Not Found - GET https://registry.npmjs.org/-/v1/done?authId=u1 - Not found\nexit 1\n")
	var rec recorder
	res, err := r.Run(context.Background(), "deprecate", "demo-pkg", []string{"deprecate", "demo-pkg", "gone"}, rec.add)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.events, []Event{{Kind: ApprovalNeeded, URL: "https://www.npmjs.com/auth/cli/u1"}}) {
		t.Errorf("events %+v", rec.events)
	}
	if res.ExitCode != 1 || res.Code != "E404" || !strings.Contains(Explain(res.Code, res.Output), "approval expired") {
		t.Errorf("result %+v", res)
	}
}

// The login prompt's link counts as an approval too.
func TestRunLoginLink(t *testing.T) {
	r, _, opened := fake(t, "print Login at:\nprint https://www.npmjs.com/login?next=/login/cli/abc\nprint Logged in on https://registry.npmjs.org/.\nexit 0\n")
	var rec recorder
	if _, err := r.Run(context.Background(), "login", "", []string{"login", "--auth-type=web"}, rec.add); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 2 || len(*opened) != 1 || (*opened)[0] != "https://www.npmjs.com/login?next=/login/cli/abc" {
		t.Errorf("events %+v opened %v", rec.events, *opened)
	}
}

// Two calls at once never overlap: the second waits for the first.
func TestRunOneAtATime(t *testing.T) {
	r, _, _ := fake(t, "mark start\nsleep 0.3\nmark end\nexit 0\n")
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := r.Run(context.Background(), "show", "demo-pkg", []string{"trust", "list", "demo-pkg"}, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	b, _ := os.ReadFile(os.Getenv("FAKE_NPM_MARKS"))
	if got := strings.Fields(string(b)); !reflect.DeepEqual(got, []string{"start", "end", "start", "end"}) {
		t.Errorf("marks %v", got)
	}
}
