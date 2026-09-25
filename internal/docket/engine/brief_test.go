package engine

import (
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/docket/item"
)

func TestBrief(t *testing.T) {
	in := fixture()
	decide(in, "repo:schuettc/hail", item.Keep, "")
	in.Decisions["repo:schuettc/hail"] = item.Decision{Disposition: item.Keep, Note: "active", DecidedBy: "court", DecidedAt: days(1)}
	r := Build(in)
	b := Brief(r, "schuettc/hail", 10)
	for _, want := range []string{"schuettc/hail", "pr:schuettc/hail#3", "branch:schuettc/hail@feat/client", "keep", "active"} {
		if !strings.Contains(b, want) {
			t.Errorf("brief lacks %q:\n%s", want, b)
		}
	}
	if Brief(r, "nobody/nothing", 10) != "" {
		t.Error("brief for an unknown repo is not empty")
	}
	if n := strings.Count(Brief(r, "schuettc/hail", 1), "\n- "); n != 1 {
		t.Errorf("max not honored: %d items", n)
	}
}
