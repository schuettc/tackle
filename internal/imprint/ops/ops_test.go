package ops

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/imprint/npm"
	"github.com/subaud-tools/tackle/internal/imprint/registry"
)

// fakeReg is a registry whose packages a test changes while an operation
// watches it.
type fakeReg struct {
	mu   sync.Mutex
	pkgs map[string]*fakePkg
}

type fakePkg struct {
	versions map[string]string // version → deprecation message
	prov     map[string]registry.Provenance
}

func (f *fakeReg) set(name string, versions map[string]string, prov map[string]registry.Provenance) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pkgs[name] = &fakePkg{versions: versions, prov: prov}
}

func (f *fakeReg) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.ReplaceAll(r.URL.EscapedPath(), "%2F", "/")
	if rest, ok := strings.CutPrefix(path, "/-/npm/v1/attestations/"); ok {
		i := strings.LastIndex(rest, "@")
		p := f.pkgs[rest[:i]]
		if p == nil {
			http.NotFound(w, r)
			return
		}
		pv, ok := p.prov[rest[i+1:]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		payload, _ := json.Marshal(map[string]any{"predicate": map[string]any{"buildDefinition": map[string]any{"externalParameters": map[string]any{
			"workflow": map[string]any{"repository": "https://github.com/" + pv.Repository, "path": pv.Workflow, "ref": pv.Ref}}}}})
		_ = json.NewEncoder(w).Encode(map[string]any{"attestations": []any{map[string]any{
			"predicateType": "https://slsa.dev/provenance/v1",
			"bundle":        map[string]any{"dsseEnvelope": map[string]any{"payload": base64.StdEncoding.EncodeToString(payload)}},
		}}})
		return
	}
	name := strings.TrimPrefix(path, "/")
	p := f.pkgs[name]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	versions := map[string]any{}
	times := map[string]string{}
	latest := ""
	i := 0
	for v, dep := range p.versions {
		doc := map[string]any{}
		if dep != "" {
			doc["deprecated"] = dep
		}
		if _, ok := p.prov[v]; ok {
			doc["dist"] = map[string]any{"attestations": map[string]any{"provenance": map[string]any{"predicateType": "https://slsa.dev/provenance/v1"}}}
		}
		versions[v] = doc
		times[v] = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(len(v)*100+i) * time.Second).Format(time.RFC3339)
		if v != registry.Stage && v > latest {
			latest = v
		}
		i++
	}
	tags := map[string]string{}
	if latest != "" {
		tags["latest"] = latest
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "dist-tags": tags, "versions": versions, "time": times})
}

// env is one test's fake registry and fake npm.
type env struct {
	ops     *Ops
	reg     *fakeReg
	scripts string // the fake npm's script directory
	args    string // the fake npm's recorded argv
	opened  *[]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	reg := &fakeReg{pkgs: map[string]*fakePkg{}}
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.Mkdir(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	args := filepath.Join(dir, "args")
	t.Setenv("FAKE_NPM_SCRIPT", scripts)
	t.Setenv("FAKE_NPM_ARGS", args)
	bin, err := filepath.Abs("../npm/testdata/fake-npm")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	opened := &[]string{}
	runner := &npm.Runner{NPM: bin, LogDir: filepath.Join(dir, "logs"), Open: func(u string) error {
		mu.Lock()
		defer mu.Unlock()
		*opened = append(*opened, u)
		return nil
	}}
	return &env{
		ops: &Ops{Reg: &registry.Client{Base: srv.URL, HTTP: srv.Client()}, NPM: runner,
			Poll: 5 * time.Millisecond, Warn: time.Hour, GiveUp: 2 * time.Hour},
		reg: reg, scripts: scripts, args: args, opened: opened,
	}
}

// script sets what the fake npm prints for a command ("whoami",
// "trust_list", "trust_list.2" for its second call).
func (e *env) script(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.scripts, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// calls is each npm call's argv, one per line.
func (e *env) calls() []string {
	b, _ := os.ReadFile(e.args)
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// reports collects an operation's messages.
type reports struct {
	mu   sync.Mutex
	msgs []string
	ch   chan string
}

func newReports() *reports { return &reports{ch: make(chan string, 100)} }

func (r *reports) add(s string) {
	r.mu.Lock()
	r.msgs = append(r.msgs, s)
	r.mu.Unlock()
	r.ch <- s
}

func (r *reports) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.msgs, "\n")
}

// waitFor waits for a message containing want.
func (r *reports) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case s := <-r.ch:
			if strings.Contains(s, want) {
				return
			}
		case <-deadline:
			t.Fatalf("no message saying %q; got:\n%s", want, r.all())
		}
	}
}

func tarball(t *testing.T, dir, name, version string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := fmt.Sprintf(`{"name":%q,"version":%q}`, name, version)
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	p := filepath.Join(dir, strings.NewReplacer("@", "", "/", "-").Replace(name)+"-"+version+".tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadTarball(t *testing.T) {
	dir := t.TempDir()
	tarball(t, dir, "@demo/pkg", "1.2.0")
	t.Chdir(dir)
	abs, name, ver, err := ReadTarball("demo-pkg-1.2.0.tgz")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(abs) || filepath.Base(abs) != "demo-pkg-1.2.0.tgz" || name != "@demo/pkg" || ver != "1.2.0" {
		t.Errorf("%s %s %s", abs, name, ver)
	}
}

func TestPublishRefusesAVersionOnNPM(t *testing.T) {
	e := newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": ""}, nil)
	tb := tarball(t, t.TempDir(), "demo-pkg", "1.0.0")
	r := newReports()
	err := e.ops.Publish(context.Background(), tb, r.add)
	if err == nil || !strings.Contains(r.all(), "demo-pkg@1.0.0 is already on npm") {
		t.Fatalf("%v: %s", err, r.all())
	}
	if len(e.calls()) != 0 {
		t.Errorf("npm ran: %v", e.calls())
	}
}

func TestPublishScopedFirstNeedsOrgMembership(t *testing.T) {
	e := newEnv(t)
	e.script(t, "whoami", "print demo-user\nexit 0\n")
	e.script(t, "org_ls", "print other-user - owner\nexit 0\n")
	tb := tarball(t, t.TempDir(), "@demo/pkg", "1.0.0")
	r := newReports()
	if err := e.ops.Publish(context.Background(), tb, r.add); err == nil || !strings.Contains(r.all(), "you aren't a member of the npm org demo") {
		t.Fatalf("%v: %s", err, r.all())
	}
	for _, c := range e.calls() {
		if strings.HasPrefix(c, "publish") {
			t.Fatalf("published anyway: %v", e.calls())
		}
	}

	e.script(t, "org_ls", "print other-user - owner\nprint demo-user - owner\nexit 0\n")
	e.script(t, "publish", "print + @demo/pkg@1.0.0\nexit 0\n")
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.reg.set("@demo/pkg", map[string]string{"1.0.0": ""}, nil)
	}()
	r = newReports()
	if err := e.ops.Publish(context.Background(), tb, r.add); err != nil {
		t.Fatalf("%v: %s", err, r.all())
	}
	if !strings.Contains(r.all(), "live on npm: @demo/pkg@1.0.0") {
		t.Errorf("reports:\n%s", r.all())
	}
}

func TestPublishNoSuchOrg(t *testing.T) {
	e := newEnv(t)
	e.script(t, "whoami", "print demo-user\nexit 0\n")
	e.script(t, "org_ls", "print npm error code E404\nprint npm error 404 Not Found - GET https://registry.npmjs.org/-/org/demo/user - Not found\nexit 1\n")
	tb := tarball(t, t.TempDir(), "@demo/pkg", "1.0.0")
	r := newReports()
	if err := e.ops.Publish(context.Background(), tb, r.add); err == nil || !strings.Contains(r.all(), "there is no npm org demo") {
		t.Fatalf("%v: %s", err, r.all())
	}
}

// A relative tarball path reaches npm absolute; npm's processing is never
// taken for live, and the same version can't be published again meanwhile.
func TestPublishWatchesUntilLive(t *testing.T) {
	e := newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"0.9.0": ""}, nil)
	e.script(t, "publish", "print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/p1\nsleep 0.1\nprint + demo-pkg@1.0.0\nexit 0\n")
	dir := t.TempDir()
	tb := tarball(t, dir, "demo-pkg", "1.0.0")
	t.Chdir(dir)
	r := newReports()
	done := make(chan error, 1)
	go func() { done <- e.ops.Publish(context.Background(), filepath.Base(tb), r.add) }()
	r.waitFor(t, "waiting for your approval: https://www.npmjs.com/auth/cli/p1")
	r.waitFor(t, "approved")
	r.waitFor(t, "npm accepted demo-pkg@1.0.0 and is processing it")
	e.reg.set("demo-pkg", map[string]string{"0.9.0": "", registry.Stage: ""}, nil)
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(r.all(), "live on npm") {
		t.Fatalf("staging reported live:\n%s", r.all())
	}
	r2 := newReports()
	if err := e.ops.Publish(context.Background(), tb, r2.add); err == nil || !strings.Contains(r2.all(), "still processing") {
		t.Fatalf("second publish: %v: %s", err, r2.all())
	}
	e.reg.set("demo-pkg", map[string]string{"0.9.0": "", "1.0.0": ""}, nil)
	if err := <-done; err != nil {
		t.Fatalf("%v: %s", err, r.all())
	}
	r.waitFor(t, "live on npm: demo-pkg@1.0.0")
	calls := e.calls()
	want := "publish " + tb + " --access public --ignore-scripts --tag latest --browser=false"
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("npm calls %q, want %q", calls, want)
	}
	if len(*e.opened) != 1 {
		t.Errorf("opened %v", *e.opened)
	}
}

func TestPublishWarnsAndGivesUp(t *testing.T) {
	e := newEnv(t)
	e.ops.Warn, e.ops.GiveUp = 20*time.Millisecond, 60*time.Millisecond
	e.script(t, "publish", "exit 0\n")
	tb := tarball(t, t.TempDir(), "demo-pkg", "1.0.0")
	r := newReports()
	err := e.ops.Publish(context.Background(), tb, r.add)
	if err == nil || !strings.Contains(r.all(), "still not visible on npm after") || !strings.Contains(r.all(), "gave up") {
		t.Fatalf("%v: %s", err, r.all())
	}
}

func TestPublishFailureExplained(t *testing.T) {
	e := newEnv(t)
	e.script(t, "publish", "print npm error code E403\nprint npm error 403 403 Forbidden - PUT https://registry.npmjs.org/demo-pkg - You cannot publish over the previously published versions: 1.0.0.\nexit 1\n")
	tb := tarball(t, t.TempDir(), "demo-pkg", "1.0.0")
	r := newReports()
	if err := e.ops.Publish(context.Background(), tb, r.add); err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(r.all(), "failed: E403") || !strings.Contains(r.all(), "check with imprint_show") || !strings.Contains(r.all(), "npm's full output: ") {
		t.Errorf("reports:\n%s", r.all())
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	e.script(t, "whoami", "print demo-user\nexit 0\n")
	r := newReports()
	if err := e.ops.Login(context.Background(), r.add); err != nil || !strings.Contains(r.all(), "logged in as demo-user") {
		t.Fatalf("%v: %s", err, r.all())
	}
	if len(e.calls()) != 1 {
		t.Errorf("calls %v", e.calls())
	}

	e = newEnv(t)
	e.script(t, "whoami.1", "print npm error code ENEEDAUTH\nprint npm error need auth This command requires you to be logged in.\nexit 1\n")
	e.script(t, "whoami.2", "print demo-user\nexit 0\n")
	e.script(t, "login", "print Login at:\nprint https://www.npmjs.com/login?next=/login/cli/l1\nsleep 0.1\nprint Logged in on https://registry.npmjs.org/.\nexit 0\n")
	r = newReports()
	if err := e.ops.Login(context.Background(), r.add); err != nil {
		t.Fatalf("%v: %s", err, r.all())
	}
	if !strings.Contains(r.all(), "waiting for your approval: https://www.npmjs.com/login?next=/login/cli/l1") || !strings.HasSuffix(r.all(), "logged in as demo-user") {
		t.Errorf("reports:\n%s", r.all())
	}
	if c := e.calls(); len(c) != 3 || c[1] != "login --auth-type=web --browser=false" {
		t.Errorf("calls %q", c)
	}
}

const trustListOld = "print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/t1\n" +
	"print type: github\nprint id: 11111111-aaaa\nprint file: publish.yml\nprint repository: olduser/widgets\nprint permissions: publish\nprint\n" +
	"print type: github\nprint id: 22222222-bbbb\nprint file: release.yml\nprint repository: olduser/other\nprint permissions: publish\nexit 0\n"

const trustListNew = "print type: github\nprint id: 33333333-cccc\nprint file: release.yml\nprint repository: acme/widgets\nprint permissions: publish\nexit 0\n"

func TestParseTrust(t *testing.T) {
	var lines []string
	for l := range strings.SplitSeq(trustListOld, "\n") {
		if s, ok := strings.CutPrefix(l, "print "); ok {
			lines = append(lines, s)
		} else if l == "print" {
			lines = append(lines, "")
		}
	}
	got := ParseTrust(strings.Join(lines, "\n"))
	if len(got) != 2 || got[0] != (Trust{ID: "11111111-aaaa", Type: "github", File: "publish.yml", Repository: "olduser/widgets"}) || got[1].ID != "22222222-bbbb" {
		t.Errorf("%+v", got)
	}
}

func TestTrust(t *testing.T) {
	e := newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": ""}, nil)
	e.script(t, "trust_list", trustListNew)
	r := newReports()
	if err := e.ops.Trust(context.Background(), "demo-pkg", "acme/widgets", "release.yml", r.add); err != nil || !strings.Contains(r.all(), "already trusts acme/widgets") {
		t.Fatalf("%v: %s", err, r.all())
	}
	if len(e.calls()) != 1 {
		t.Errorf("calls %v", e.calls())
	}

	e = newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": ""}, nil)
	e.script(t, "trust_list.1", trustListOld)
	e.script(t, "trust_list.2", trustListNew)
	e.script(t, "trust_revoke", "exit 0\n")
	e.script(t, "trust_github", "print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/t2\nexit 0\n")
	r = newReports()
	if err := e.ops.Trust(context.Background(), "demo-pkg", "acme/widgets", "release.yml", r.add); err != nil {
		t.Fatalf("%v: %s", err, r.all())
	}
	want := []string{
		"trust list demo-pkg --browser=false",
		"trust revoke demo-pkg --id=11111111-aaaa --browser=false",
		"trust revoke demo-pkg --id=22222222-bbbb --browser=false",
		"trust github demo-pkg --file release.yml --repo acme/widgets --allow-publish -y --browser=false",
		"trust list demo-pkg --browser=false",
	}
	if got := e.calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(r.all(), "unproven") || !strings.Contains(r.all(), "imprint_expect") {
		t.Errorf("reports:\n%s", r.all())
	}
}

func TestTrustNeedsAnExistingPackage(t *testing.T) {
	e := newEnv(t)
	r := newReports()
	if err := e.ops.Trust(context.Background(), "demo-pkg", "acme/widgets", "release.yml", r.add); err == nil || !strings.Contains(r.all(), "imprint_publish") {
		t.Fatalf("%v: %s", err, r.all())
	}
}

func TestDeprecate(t *testing.T) {
	e := newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": "Moved to demo-new", "1.1.0": "Moved to demo-new"}, nil)
	r := newReports()
	if err := e.ops.Deprecate(context.Background(), "demo-pkg", "Moved to demo-new", r.add); err != nil || !strings.Contains(r.all(), "already deprecated") {
		t.Fatalf("%v: %s", err, r.all())
	}
	if len(e.calls()) != 0 {
		t.Errorf("npm ran: %v", e.calls())
	}

	// npm prints a line per version before it asks; the approval expires.
	e = newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": "", "1.1.0": ""}, nil)
	e.script(t, "deprecate", "print deprecating demo-pkg@1.0.0\nprint deprecating demo-pkg@1.1.0\n"+
		"print Authenticate your account at:\nprint https://www.npmjs.com/auth/cli/d1\n"+
		"print npm error code E404\nprint npm error 404 Not Found - GET https://registry.npmjs.org/-/v1/done?authId=d1 - Not found\nexit 1\n")
	r = newReports()
	if err := e.ops.Deprecate(context.Background(), "demo-pkg", "Moved to demo-new", r.add); err == nil {
		t.Fatalf("no error: %s", r.all())
	}
	if !strings.Contains(r.all(), "approval expired") || strings.Contains(r.all(), "approved") || strings.Contains(r.all(), "deprecated every") {
		t.Errorf("reports:\n%s", r.all())
	}

	// Done only once the registry shows the message.
	e = newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": "", "1.1.0": ""}, nil)
	e.script(t, "deprecate", "print deprecating demo-pkg@1.0.0\nprint deprecating demo-pkg@1.1.0\nexit 0\n")
	go func() {
		time.Sleep(30 * time.Millisecond)
		e.reg.set("demo-pkg", map[string]string{"1.0.0": "Moved to demo-new", "1.1.0": "Moved to demo-new"}, nil)
	}()
	r = newReports()
	if err := e.ops.Deprecate(context.Background(), "demo-pkg", "Moved to demo-new", r.add); err != nil || !strings.Contains(r.all(), "deprecated every version of demo-pkg") {
		t.Fatalf("%v: %s", err, r.all())
	}
	if c := e.calls(); len(c) != 1 || c[0] != "deprecate demo-pkg Moved to demo-new --browser=false" {
		t.Errorf("calls %q", c)
	}
}

func TestExpect(t *testing.T) {
	e := newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": ""}, nil)
	go func() {
		time.Sleep(30 * time.Millisecond)
		e.reg.set("demo-pkg", map[string]string{"1.0.0": "", "1.1.0": ""}, map[string]registry.Provenance{
			"1.1.0": {Repository: "acme/widgets", Workflow: ".github/workflows/release.yml", Ref: "refs/tags/v1.1.0"}})
	}()
	r := newReports()
	if err := e.ops.Expect(context.Background(), "demo-pkg", "1.1.0", "acme/widgets", r.add); err != nil {
		t.Fatalf("%v: %s", err, r.all())
	}
	if !strings.Contains(r.all(), "demo-pkg@1.1.0 is live and signed by CI from acme/widgets (.github/workflows/release.yml, refs/tags/v1.1.0)") {
		t.Errorf("reports:\n%s", r.all())
	}

	r = newReports()
	if err := e.ops.Expect(context.Background(), "demo-pkg", "1.1.0", "acme/other", r.add); err == nil || !strings.Contains(r.all(), "signed by CI from acme/widgets") || !strings.Contains(r.all(), "not acme/other") {
		t.Fatalf("%v: %s", err, r.all())
	}
	r = newReports()
	if err := e.ops.Expect(context.Background(), "demo-pkg", "1.0.0", "acme/widgets", r.add); err == nil || !strings.Contains(r.all(), "no provenance") {
		t.Fatalf("%v: %s", err, r.all())
	}
}

func TestShow(t *testing.T) {
	e := newEnv(t)
	e.reg.set("demo-pkg", map[string]string{registry.Stage: "", "1.0.0": "Moved", "1.1.0": ""}, map[string]registry.Provenance{
		"1.1.0": {Repository: "acme/widgets", Workflow: ".github/workflows/release.yml", Ref: "refs/tags/v1.1.0"}})
	s, err := e.ops.Show(context.Background(), "demo-pkg")
	if err != nil {
		t.Fatal(err)
	}
	text := s.String()
	for _, want := range []string{"latest: 1.1.0", "1.1.0 signed by CI from acme/widgets", "1.0.0 deprecated: Moved", "still processing"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary lacks %q:\n%s", want, text)
		}
	}
	if s, _ := e.ops.Show(context.Background(), "demo-missing"); !strings.Contains(s.String(), "not on npm") {
		t.Errorf("missing: %s", s)
	}
}

func TestShowTrust(t *testing.T) {
	e := newEnv(t)
	e.reg.set("demo-pkg", map[string]string{"1.0.0": ""}, nil)
	e.script(t, "trust_list", trustListNew)
	r := newReports()
	if err := e.ops.ShowTrust(context.Background(), "demo-pkg", r.add); err != nil {
		t.Fatalf("%v: %s", err, r.all())
	}
	if !strings.Contains(r.all(), "acme/widgets") || !strings.Contains(r.all(), "unproven") {
		t.Errorf("reports:\n%s", r.all())
	}
}
