// Package app runs the docket's commands against the real stores: it wires
// config, the docket repo, the spool, observation and the engine together.
package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/docket/config"
	"github.com/schuettc/tackle/internal/docket/engine"
	"github.com/schuettc/tackle/internal/docket/journal"
	"github.com/schuettc/tackle/internal/docket/observe"
	"github.com/schuettc/tackle/internal/docket/store"
	tools "github.com/schuettc/tools-common"
)

// App is an initialized docket on this machine.
type App struct {
	Cfg  config.Config
	Repo *store.Repo
	Gh   observe.Runner
	Now  func() time.Time
}

// Open loads this machine's config and docket repo.
func Open(gh observe.Runner) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	repo, err := store.Open(cfg.DocketRepo)
	if err != nil {
		return nil, fmt.Errorf("%w (run `docket init %s` to re-clone it)", err, cfg.DocketRemote)
	}
	return &App{Cfg: cfg, Repo: repo, Gh: gh, Now: time.Now}, nil
}

// InitOptions configures a first init. Empty fields take defaults; User
// defaults to gh's login.
type InitOptions struct {
	Remote   string
	Machine  string
	User     string
	Roots    []string
	NoCreate bool // never create a missing GitHub repo
}

// InitResult reports what Init did.
type InitResult struct {
	Config  config.Config
	Created string // "owner/name" of a GitHub repo Init created, or ""
}

// Init writes this machine's config (unless one exists, which is kept) and
// clones or bootstraps the docket repo. Safe to re-run.
func Init(ctx context.Context, o InitOptions, gh observe.Runner) (InitResult, error) {
	cfg, err := config.Load()
	var created string
	switch {
	case err == nil:
		if o.Remote != "" && cfg.DocketRemote != o.Remote {
			return InitResult{Config: cfg}, fmt.Errorf("already initialized with remote %s (edit %s to change it)", cfg.DocketRemote, config.Path())
		}
	case errors.Is(err, config.ErrNotInitialized):
		user := o.User
		if user == "" {
			out, gerr := gh.Gh(ctx, "api", "user", "--jq", ".login")
			user = strings.TrimSpace(string(out))
			if gerr != nil || user == "" {
				return InitResult{}, fmt.Errorf("could not read your GitHub login from gh (%v); pass --user", gerr)
			}
		}
		remote := o.Remote
		if remote == "" {
			remote = defaultRemote(user)
		}
		if repo := observe.GitHubRepo(remote); repo != "" {
			ok, gerr := ensurePrivateRepo(ctx, gh, repo, !o.NoCreate)
			if gerr != nil {
				return InitResult{}, gerr
			}
			if ok {
				created = repo
			}
		}
		cfg = config.Config{Machine: o.Machine, User: user, DocketRemote: remote, Roots: o.Roots}
		cfg.Defaults()
		if err := config.Save(cfg); err != nil {
			return InitResult{}, err
		}
	default:
		return InitResult{}, err
	}
	if _, err := store.Init(ctx, cfg.DocketRepo, cfg.DocketRemote); err != nil {
		return InitResult{Config: cfg}, err
	}
	return InitResult{Config: cfg, Created: created}, nil
}

// defaultRemote returns the default docket data remote for the given GitHub login.
func defaultRemote(login string) string {
	return "git@github.com:" + login + "/docket-data.git"
}

// ensurePrivateRepo checks that repo exists on GitHub and is private.
// If the repo does not exist and create is true, it creates it as private.
// Non-GitHub remotes should be filtered out by the caller before calling this.
func ensurePrivateRepo(ctx context.Context, gh observe.Runner, repo string, create bool) (created bool, err error) {
	out, err := gh.Gh(ctx, "api", "repos/"+repo, "--jq", ".private")
	if err != nil {
		var ge *observe.GhError
		if errors.As(err, &ge) && (strings.Contains(ge.Stderr, "HTTP 404") || strings.Contains(ge.Stderr, "Not Found")) {
			if !create {
				return false, fmt.Errorf("%s does not exist on GitHub (run docket init without --no-create to create it as a private repo)", repo)
			}
			if _, cerr := gh.Gh(ctx, "repo", "create", repo, "--private", "--description", "docket data (written by the docket binary)"); cerr != nil {
				return false, fmt.Errorf("creating %s on GitHub: %w", repo, cerr)
			}
			return true, nil
		}
		return false, fmt.Errorf("checking %s on GitHub: %w", repo, err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return false, fmt.Errorf("%s is public; docket data names your private repos — make it private or pass a different remote", repo)
	}
	return false, nil
}

func (a *App) snapshots() ([]observe.Snapshot, error) {
	files, err := a.Repo.Glob("machines/*.json")
	if err != nil {
		return nil, err
	}
	var out []observe.Snapshot
	for _, f := range files {
		b, err := a.Repo.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var s observe.Snapshot
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func loadSeen() map[string]time.Time {
	seen := map[string]time.Time{}
	if b, err := os.ReadFile(config.SeenPath()); err == nil {
		_ = json.Unmarshal(b, &seen)
	}
	return seen
}

func saveSeen(seen map[string]time.Time) error {
	b, err := json.MarshalIndent(seen, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteFileAtomic(config.SeenPath(), append(b, '\n'), 0o600)
}

func (a *App) input(g *observe.GitHub, snaps []observe.Snapshot) (engine.Input, error) {
	decisions, errs := a.Repo.Decisions()
	if len(errs) > 0 {
		return engine.Input{}, fmt.Errorf("unreadable decision files (run `docket validate`): %w", errors.Join(errs...))
	}
	pol, err := a.Repo.Policy()
	if err != nil {
		return engine.Input{}, err
	}
	return engine.Input{Now: a.Now(), GitHub: g, Snapshots: snaps, Decisions: decisions, Policy: pol, Seen: loadSeen()}, nil
}

// Build computes items from what is already known: the GitHub cache, the
// snapshots and decisions in the docket repo. No network, no writes.
func (a *App) Build(ctx context.Context) (engine.Result, []observe.Snapshot, error) {
	g, err := observe.LoadGitHub(config.CachePath())
	if err != nil {
		return engine.Result{}, nil, err
	}
	snaps, err := a.snapshots()
	if err != nil {
		return engine.Result{}, nil, err
	}
	in, err := a.input(g, snaps)
	if err != nil {
		return engine.Result{}, nil, err
	}
	return engine.Build(in), snaps, nil
}

// Events reads every journal line in the docket repo, oldest file first.
func (a *App) Events() ([]journal.Event, error) {
	files, err := a.Repo.Glob("journal/*/*/*.jsonl")
	if err != nil {
		return nil, err
	}
	var out []journal.Event
	for _, f := range files {
		fh, err := os.Open(filepath.Join(a.Repo.Dir, filepath.FromSlash(f)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var ev journal.Event
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				out = append(out, ev)
			}
		}
		fh.Close()
	}
	return out, nil
}
