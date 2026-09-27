package observe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "github.json")
	g, err := LoadGitHub(p)
	if err != nil || len(g.Owners) != 0 || g.Refs == nil {
		t.Fatalf("missing cache: %+v %v", g, err)
	}
	g.User = "schuettc"
	g.Owners["acme"] = &Owner{Login: "acme", Reachable: true, FetchedAt: time.Unix(10, 0).UTC(), Repos: []RepoObs{{Repo: "acme/x", Archived: true}}}
	if err := SaveGitHub(p, g); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	back, err := LoadGitHub(p)
	if err != nil || back.Owners["acme"].Repos[0].Archived != true || back.Version != CacheVersion {
		t.Fatalf("%+v %v", back, err)
	}
	os.WriteFile(p, []byte(`{"version":99}`), 0o600)
	if g, err := LoadGitHub(p); err != nil || len(g.Owners) != 0 {
		t.Fatalf("newer cache not discarded: %+v %v", g, err)
	}
}
