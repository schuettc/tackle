// fixture_test.go — generates the probe's committed test fixture.
//
// Usage: go test ./internal/casebook/web/ -run TestFixture -update
//
// With -update, regenerates web/testdata/home:
//
//	testdata/home/data/repo.bundle  — git bundle of a seeded casebook-data repo
//	testdata/home/cache/github.json — GitHub observation cache
//
// Without -update, skips if testdata/home is absent (first checkout) or
// verifies the fixture is present.
//
// serve.mjs uses the fixture to start a real casebook serve for probe.mjs.
package web_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/apptest"
	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

var updateFixture = flag.Bool("update", false, "regenerate testdata/home from a fresh fixture")

func TestFixture(t *testing.T) {
	if !*updateFixture {
		if _, err := os.Stat(filepath.Join("testdata", "home")); os.IsNotExist(err) {
			t.Skip("testdata/home absent; run with -update to generate")
		}
		return
	}

	t.Log("generating testdata/home...")

	// Hermetic git: fixed identity, no system config.
	testgit.Env(t)

	// Fix git dates for determinism.
	t.Setenv("GIT_AUTHOR_DATE", "2026-09-27T12:00:00+00:00")
	t.Setenv("GIT_COMMITTER_DATE", "2026-09-27T12:00:00+00:00")

	// Isolated HOME and CASEBOOK_HOME.
	tmpHome := t.TempDir()
	tmpCasebook := filepath.Join(tmpHome, "casebook-home")
	t.Setenv("HOME", tmpHome)
	t.Setenv("CASEBOOK_HOME", tmpCasebook)

	ctx := context.Background()
	gh := apptest.FakeGh{}

	// Initialize a casebook with a local bare remote.
	remote := testgit.NewBare(t)
	if _, err := app.Init(ctx, app.InitOptions{
		Remote:  remote,
		Machine: "probe",
		User:    "schuettc",
		Roots:   []string{},
	}, gh); err != nil {
		t.Fatal("init:", err)
	}

	a, err := app.Open(gh)
	if err != nil {
		t.Fatal("open:", err)
	}
	a.Now = func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }

	// Create a scan root with a fake hail clone so the sync has items.
	scanRoot := t.TempDir()
	clone := filepath.Join(scanRoot, "hail")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Git(t, clone, "init", "-q", "-b", "main")
	c1 := testgit.Commit(t, clone, "a", "1")
	testgit.Git(t, clone, "remote", "add", "origin", "git@github.com:schuettc/hail.git")
	testgit.Git(t, clone, "update-ref", "refs/remotes/origin/main", c1)
	testgit.Git(t, clone, "switch", "-q", "-c", "feat/client")
	testgit.Commit(t, clone, "b", "2")

	// Add the scan root to the config.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("load config:", err)
	}
	cfg.Roots = []string{scanRoot}
	if err := config.Save(cfg); err != nil {
		t.Fatal("save config:", err)
	}

	// Re-open to pick up the updated config.
	a, err = app.Open(gh)
	if err != nil {
		t.Fatal("re-open:", err)
	}
	a.Now = func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }

	// Sync to populate the repo and cache with observations.
	if _, err := a.Sync(ctx, app.SyncOptions{NoPush: true}); err != nil {
		t.Fatal("sync:", err)
	}

	// Write the testdata directory.
	testdataDir := filepath.Join("testdata", "home")
	if err := os.RemoveAll(testdataDir); err != nil {
		t.Fatal("remove testdata:", err)
	}

	dataDir := filepath.Join(testdataDir, "data")
	cacheDir := filepath.Join(testdataDir, "cache")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Bundle the casebook-data repo.
	bundlePath, err := filepath.Abs(filepath.Join(dataDir, "repo.bundle"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, a.Repo.Dir, "bundle", "create", bundlePath, "--all"); err != nil {
		t.Fatal("bundle:", err)
	}

	// Copy the github cache.
	cacheSrc := config.CachePath()
	cacheData, err := os.ReadFile(cacheSrc)
	if err != nil {
		t.Fatal("read cache:", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "github.json"), cacheData, 0o644); err != nil {
		t.Fatal("write cache:", err)
	}

	t.Logf("testdata/home written (bundle: %s)", bundlePath)
}
