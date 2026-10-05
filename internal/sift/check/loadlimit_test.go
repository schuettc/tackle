package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/profile"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
)

func TestLoadLimit(t *testing.T) {
	g := &discover.File{Path: "/h/.codex/AGENTS.md", Class: discover.ClassGlobal, Content: strings.Repeat("g", 60)}
	root := inRepo(&discover.File{Class: discover.ClassRepo, Content: strings.Repeat("r", 30)}, "/w/app", "AGENTS.md")
	leaf := &discover.File{Class: discover.ClassRepo, Content: strings.Repeat("l", 30), Repo: root.Repo, Rel: "svc/AGENTS.md", Path: "/w/app/svc/AGENTS.md"}
	other := &discover.File{Class: discover.ClassRepo, Content: "x", Repo: root.Repo, Rel: "web/AGENTS.md", Path: "/w/app/web/AGENTS.md"}
	in := input(g, root, leaf, other)
	in.Chains = []discover.Chain{
		{Profile: "codex", Repo: root.Repo, Dir: "svc", Files: []*discover.File{g, root, leaf}, Bytes: 120, Limit: 100},
		{Profile: "codex", Repo: root.Repo, Dir: "web", Files: []*discover.File{g, root, other}, Bytes: 91, Limit: 100},
	}
	rows := only(t, "load-limit", in)
	if len(rows) != 1 || rows[0].Source.File != leaf.Path || rows[0].Certain {
		t.Fatalf("%+v", rows)
	}
	if got := evidence(rows[0], "dropped"); got != "/w/app/svc/AGENTS.md" {
		t.Errorf("dropped %q", got)
	}
	if got := evidence(rows[0], "chain"); got != "120 bytes, limit 100 (codex)" {
		t.Errorf("chain %q", got)
	}
}

// A root inside a repo still loads the instruction files above it: a chain
// that passes the limit only with its ancestor is flagged, on the leaf, with
// the ancestor in the evidence. The ancestor gets no row of its own.
func TestLoadLimitOnASubtreeRoot(t *testing.T) {
	st.Env(t)
	st.Home(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "app"), map[string]string{
		"AGENTS.md":     strings.Repeat("a", 20000),
		"svc/AGENTS.md": strings.Repeat("s", 20000),
	})
	p, _ := profile.Builtin("codex")
	res, err := discover.Run(ctx, discover.Options{Profiles: []profile.Profile{p}, Roots: []config.Root{{Path: filepath.Join(repo, "svc")}}})
	if err != nil {
		t.Fatal(err)
	}
	leaf, top := filepath.Join(repo, "svc", "AGENTS.md"), filepath.Join(repo, "AGENTS.md")
	if len(res.Files) != 1 || res.Files[0].Path != leaf {
		t.Fatalf("files %+v", res.Files)
	}
	in := input(res.Files...)
	in.Chains = res.Chains
	rows := only(t, "load-limit", in)
	if len(rows) != 1 || rows[0].Source.File != leaf {
		t.Fatalf("%+v", rows)
	}
	if got := evidence(rows[0], "file"); !strings.Contains(got, top+" (20000 bytes)") {
		t.Errorf("chain evidence %q lacks the ancestor", got)
	}
	for _, r := range Run(ctx, in) {
		if r.Source.File == top {
			t.Errorf("the ancestor has a row: %+v", r)
		}
	}
}

// A global that links to an audited subtree leaf is loaded twice, as the
// global and as the leaf: the chain counts it twice, and the row is on it,
// never on the unaudited ancestor between.
func TestLoadLimitOnAGlobalThatIsTheLeaf(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	repo := st.Repo(t, filepath.Join(t.TempDir(), "repo"), map[string]string{
		"AGENTS.md":     strings.Repeat("a", 12000),
		"svc/AGENTS.md": strings.Repeat("s", 12000),
	})
	global := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(global), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "svc", "AGENTS.md"), global); err != nil {
		t.Fatal(err)
	}
	p, _ := profile.Builtin("codex")
	res, err := discover.Run(ctx, discover.Options{Profiles: []profile.Profile{p}, Roots: []config.Root{{Path: filepath.Join(repo, "svc")}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Rel != "svc/AGENTS.md" {
		t.Fatalf("files %+v", res.Files)
	}
	leaf := res.Files[0]
	if len(res.Chains) != 1 {
		t.Fatalf("chains %+v", res.Chains)
	}
	c := res.Chains[0]
	if len(c.Files) != 3 || c.Files[0] != leaf || c.Files[1].Rel != "AGENTS.md" || c.Files[2] != leaf || c.Bytes != 36000 {
		t.Fatalf("chain %+v", c)
	}
	in := input(res.Files...)
	in.Chains = res.Chains
	rows := only(t, "load-limit", in)
	if len(rows) != 1 || rows[0].Source.File != leaf.Path {
		t.Fatalf("%+v", rows)
	}
	for _, r := range Run(ctx, in) {
		if r.Source.File == filepath.Join(repo, "AGENTS.md") {
			t.Errorf("the ancestor has a row: %+v", r)
		}
	}
}
