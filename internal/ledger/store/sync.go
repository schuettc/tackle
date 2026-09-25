package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/schuettc/tackle/internal/ledger/gitx"
	"github.com/schuettc/tackle/internal/ledger/item"
)

// ErrOffline means the remote could not be reached; local commits stay queued
// and go out at the next sync.
var ErrOffline = errors.New("ledger remote unreachable; changes are queued locally")

// Views are rendered files: on a rebase conflict the upstream copy is taken
// and the next render replaces it.
var Views = []string{"README.md", "CONTRIBUTIONS.md", "MACHINES.md"}

// SyncResult reports what Sync did.
type SyncResult struct {
	Pulled     bool
	Pushed     bool
	Resolved   []string // decision files auto-resolved (later decided_at wins)
	ViewsTaken bool     // a view conflicted and the upstream copy was taken
}

// Sync rebases local commits onto the remote and pushes, retrying up to three
// times when another machine pushed in between.
func (r *Repo) Sync(ctx context.Context) (SyncResult, error) {
	var res SyncResult
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := gitx.Run(ctx, r.Dir, "fetch", "-q", "origin"); err != nil {
			return res, fmt.Errorf("%w: %v", ErrOffline, err)
		}
		if _, err := gitx.Run(ctx, r.Dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/main"); err == nil {
			ahead, _ := gitx.Run(ctx, r.Dir, "rev-list", "--count", "HEAD..origin/main")
			if ahead != "0" {
				if err := r.rebase(ctx, &res); err != nil {
					return res, err
				}
				res.Pulled = true
			}
		}
		local, _ := gitx.Run(ctx, r.Dir, "rev-list", "--count", "origin/main..HEAD")
		if local == "0" {
			return res, nil
		}
		_, err := gitx.Run(ctx, r.Dir, "push", "-q", "origin", "HEAD:main")
		if err == nil {
			res.Pushed = true
			return res, nil
		}
		var ge *gitx.Error
		if errors.As(err, &ge) && (strings.Contains(ge.Stderr, "non-fast-forward") || strings.Contains(ge.Stderr, "fetch first") || strings.Contains(ge.Stderr, "rejected")) {
			continue
		}
		return res, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	return res, fmt.Errorf("push kept racing other machines; try again")
}

// rebase replays local commits onto origin/main, resolving decision and view
// conflicts; anything else aborts the rebase and fails. A replayed commit that
// resolution left empty (it only touched a view) is skipped.
func (r *Repo) rebase(ctx context.Context, res *SyncResult) error {
	_, err := gitx.Run(ctx, r.Dir, "rebase", "-q", "origin/main")
	for i := 0; err != nil; i++ {
		if !r.rebasing(ctx) || i > 1000 {
			r.abort(ctx)
			return fmt.Errorf("rebase onto the ledger remote failed: %v", err)
		}
		files, _ := gitx.Run(ctx, r.Dir, "diff", "--name-only", "--diff-filter=U")
		if files == "" && i == 0 {
			r.abort(ctx)
			return fmt.Errorf("rebase onto the ledger remote failed: %v", err)
		}
		if files != "" {
			for _, f := range strings.Split(files, "\n") {
				if rerr := r.resolve(ctx, f, res); rerr != nil {
					r.abort(ctx)
					return rerr
				}
			}
		}
		if _, derr := gitx.Run(ctx, r.Dir, "diff", "--cached", "--quiet"); derr == nil {
			_, err = gitx.Run(ctx, r.Dir, "rebase", "--skip")
		} else {
			_, err = gitx.Run(ctx, r.Dir, "-c", "core.editor=true", "rebase", "--continue")
		}
	}
	return nil
}

func (r *Repo) resolve(ctx context.Context, f string, res *SyncResult) error {
	for _, v := range Views {
		if f == v {
			// During a rebase "ours" (stage 2) is the upstream side.
			if _, err := gitx.Run(ctx, r.Dir, "checkout", "--ours", "--", f); err != nil {
				return err
			}
			res.ViewsTaken = true
			_, err := gitx.Run(ctx, r.Dir, "add", "--", f)
			return err
		}
	}
	k, err := item.KeyFromFile(f)
	if err != nil {
		return fmt.Errorf("sync conflict in %s: only decision files and views are auto-resolved; resolve it by hand in %s", f, r.Dir)
	}
	up, err1 := gitx.Run(ctx, r.Dir, "show", ":2:"+f)
	mine, err2 := gitx.Run(ctx, r.Dir, "show", ":3:"+f)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("sync conflict in %s: one side deleted it; resolve it by hand in %s", f, r.Dir)
	}
	du, err1 := item.DecodeDecision([]byte(up))
	dm, err2 := item.DecodeDecision([]byte(mine))
	if err1 != nil || err2 != nil {
		return fmt.Errorf("sync conflict in %s: unreadable side; resolve it by hand in %s", f, r.Dir)
	}
	b, err := item.EncodeDecision(Merge(du, dm))
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.abs(f), b, 0o644); err != nil {
		return err
	}
	res.Resolved = append(res.Resolved, k.File())
	_, err = gitx.Run(ctx, r.Dir, "add", "--", f)
	return err
}

// Merge resolves two racing decisions: the later decided_at wins (ties go to
// a), and the loser is kept as the winner's conflict unless both say the same
// thing.
func Merge(a, b item.Decision) item.Decision {
	win, lose := a, b
	if b.DecidedAt.After(a.DecidedAt) {
		win, lose = b, a
	}
	win.Conflict = nil
	if win.Disposition == lose.Disposition && win.Until == lose.Until && win.Note == lose.Note {
		return win
	}
	win.Conflict = &item.Conflict{Disposition: lose.Disposition, Note: lose.Note, Until: lose.Until, DecidedBy: lose.DecidedBy, DecidedAt: lose.DecidedAt}
	return win
}

func (r *Repo) rebasing(ctx context.Context) bool {
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		p, err := gitx.Run(ctx, r.Dir, "rev-parse", "--git-path", d)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			p = r.abs(p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func (r *Repo) abort(ctx context.Context) {
	if r.rebasing(ctx) {
		_, _ = gitx.Run(ctx, r.Dir, "rebase", "--abort")
	}
}
