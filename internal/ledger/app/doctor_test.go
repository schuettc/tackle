package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/ledger/config"
	"github.com/schuettc/tackle/internal/ledger/hooks"
	"github.com/schuettc/tackle/internal/ledger/observe"
)

func TestDoctor(t *testing.T) {
	r := newRig(t)
	if _, err := r.app.Sync(ctx, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	g, _ := observe.LoadGitHub(config.CachePath())
	g.Owners["acme"] = &observe.Owner{Login: "Acme", Reachable: false, Stale: true, Reason: "SAML SSO: gh's token is not authorized for this org"}
	observe.SaveGitHub(config.CachePath(), g)
	h := hooks.Options{Dir: filepath.Join(t.TempDir(), "hooks"), StatePath: filepath.Join(t.TempDir(), "hooks.json"), Binary: "/nonexistent/ledger"}
	checks := r.app.Doctor(ctx, h, "/nonexistent/ledger")
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for name, want := range map[string]string{"config": "ok", "ledger repo": "ok", "remote": "ok", "gh": "ok", "hooks": "warn", "owner Acme": "warn"} {
		if c := byName[name]; c.Status != want {
			t.Errorf("%s = %+v, want %s", name, c, want)
		}
	}
	if !strings.Contains(byName["owner Acme"].Detail, "SAML") {
		t.Errorf("owner detail %q", byName["owner Acme"].Detail)
	}
}
