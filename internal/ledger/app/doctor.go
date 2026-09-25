package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/ledger/config"
	"github.com/schuettc/tackle/internal/ledger/gitx"
	"github.com/schuettc/tackle/internal/ledger/hooks"
	"github.com/schuettc/tackle/internal/ledger/observe"
)

// Check is one doctor finding.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

// Doctor inspects this machine's ledger setup. exe is the running binary,
// compared with what the hook shims call.
func (a *App) Doctor(ctx context.Context, h hooks.Options, exe string) []Check {
	var out []Check
	add := func(name, status, format string, args ...any) {
		out = append(out, Check{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)})
	}
	add("config", "ok", "machine %s, user %s, roots %s", a.Cfg.Machine, a.Cfg.User, strings.Join(a.Cfg.Roots, ", "))

	st, err := gitx.Run(ctx, a.Repo.Dir, "status", "--porcelain")
	switch {
	case err != nil:
		add("ledger repo", "fail", "%s: %v", a.Repo.Dir, err)
	case st != "":
		add("ledger repo", "warn", "%s has uncommitted changes (a sync will commit them)", a.Repo.Dir)
	default:
		queued, _ := gitx.Run(ctx, a.Repo.Dir, "rev-list", "--count", "origin/main..HEAD")
		if queued != "" && queued != "0" {
			add("ledger repo", "warn", "%s commits not pushed yet", queued)
		} else {
			add("ledger repo", "ok", "%s", a.Repo.Dir)
		}
	}
	if _, err := gitx.Run(ctx, a.Repo.Dir, "ls-remote", "-q", "origin", "HEAD"); err != nil {
		add("remote", "warn", "%s unreachable: %v", a.Cfg.LedgerRemote, err)
	} else {
		add("remote", "ok", "%s", a.Cfg.LedgerRemote)
	}
	if _, err := a.Gh.Gh(ctx, "auth", "status"); err != nil {
		add("gh", "fail", "gh is not usable: %v", err)
	} else {
		add("gh", "ok", "authenticated")
	}

	hs, _ := hooks.GetStatus(ctx, h)
	switch {
	case !hs.Installed:
		add("hooks", "warn", "not installed; git actions are not journaled (run `ledger hooks install`)")
	case !hs.BinaryOK:
		add("hooks", "fail", "shims call %s, which is missing (re-run `ledger hooks install`)", hs.Binary)
	case len(hs.Missing) > 0:
		add("hooks", "warn", "shims missing for %s (re-run `ledger hooks install`)", strings.Join(hs.Missing, ", "))
	case exe != "" && hs.Binary != exe:
		add("hooks", "warn", "shims call %s but this is %s", hs.Binary, exe)
	default:
		add("hooks", "ok", "global core.hooksPath → %s", h.Dir)
	}

	if files, _ := filepath.Glob(filepath.Join(config.SpoolDir(), "*.jsonl")); len(files) > 0 {
		add("spool", "ok", "%d spool file(s) waiting for sync", len(files))
	}

	g, err := observe.LoadGitHub(config.CachePath())
	if err != nil {
		add("github cache", "fail", "%v", err)
	} else {
		logins := make([]string, 0, len(g.Owners))
		for k := range g.Owners {
			logins = append(logins, k)
		}
		sort.Strings(logins)
		for _, k := range logins {
			ow := g.Owners[k]
			switch {
			case !ow.Reachable:
				add("owner "+ow.Login, "warn", "unreachable: %s", ow.Reason)
			case ow.Stale:
				add("owner "+ow.Login, "warn", "stale since %s: %s", ow.FetchedAt.Format(time.DateOnly), ow.Reason)
			}
		}
		if len(g.Owners) == 0 {
			add("github cache", "warn", "empty (run `ledger sync`)")
		}
	}

	if _, err := a.Repo.ReadFile("machines/" + a.Cfg.Machine + ".json"); err == nil {
		snaps, _ := a.snapshots()
		for _, s := range snaps {
			if s.Machine != a.Cfg.Machine {
				continue
			}
			var local, nogh []string
			for _, c := range s.Clones {
				if c.LocalHooksPath != "" {
					local = append(local, c.Path)
				}
				if c.Repo == "" {
					nogh = append(nogh, c.Path)
				}
			}
			if len(local) > 0 {
				add("not journaled", "warn", "local core.hooksPath bypasses the global hooks in: %s", strings.Join(local, ", "))
			}
			if len(nogh) > 0 {
				add("no GitHub remote", "ok", "%d clone(s) tracked only locally", len(nogh))
			}
		}
	} else if !os.IsNotExist(err) {
		add("snapshot", "warn", "%v", err)
	}
	return out
}
