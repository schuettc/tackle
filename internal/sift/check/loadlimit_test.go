package check

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/discover"
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
