package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/item"
)

// ErrOffline means the remote could not be reached; local commits stay queued
// and go out at the next sync.
var ErrOffline = errors.New("casebook remote unreachable; changes are queued locally")

// ErrRefused means the remote received the push and refused it (a
// pre-receive hook, branch protection); local commits stay queued. The
// error carries git's rejected line and the remote's own message.
var ErrRefused = errors.New("the casebook remote refused the push")

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
			return res, fmt.Errorf("%w: %w", ErrOffline, err)
		}
		if _, err := gitx.Run(ctx, r.Dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/main"); err == nil {
			ahead, _ := gitx.Run(ctx, r.Dir, "rev-list", "--count", "HEAD..origin/main")
			if ahead != "0" {
				if err := r.lockedRebase(ctx, &res); err != nil {
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
		if !errors.As(err, &ge) {
			return res, fmt.Errorf("%w: %w", ErrOffline, err)
		}
		if raced(ge.Stderr) {
			continue
		}
		if why := refusal(ge.Stderr); why != "" {
			return res, fmt.Errorf("%w: %s", ErrRefused, why)
		}
		return res, fmt.Errorf("%w: %w", ErrOffline, err)
	}
	return res, fmt.Errorf("push kept racing other machines; try again")
}

// raced reports a push git rejected because the remote moved since the
// fetch ("! [rejected] … (fetch first)" or "(non-fast-forward)"): another
// machine pushed in between, so fetch, rebase and push again. Only git's own
// rejected line counts, so a remote hook whose message happens to say
// "non-fast-forward" is still a refusal.
func raced(stderr string) bool {
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "! [rejected]") && (strings.Contains(l, "(fetch first)") || strings.Contains(l, "(non-fast-forward)")) {
			return true
		}
	}
	return false
}

// refusal is the reason a remote refused a push (a pre-receive hook, branch
// protection: "! [remote rejected] … (pre-receive hook declined)"), or
// any other rejection that isn't a race: git's rejected lines, then the
// remote's own "remote:" lines. "" when stderr holds no rejection (the
// remote couldn't be reached).
func refusal(stderr string) string {
	var rejected, remote []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "! [") && strings.Contains(l, "rejected]"):
			rejected = append(rejected, strings.Join(strings.Fields(strings.TrimPrefix(l, "!")), " "))
		case strings.HasPrefix(l, "remote:"):
			if m := strings.TrimSpace(strings.TrimPrefix(l, "remote:")); m != "" {
				remote = append(remote, m)
			}
		}
	}
	if len(rejected) == 0 {
		return ""
	}
	why := strings.Join(rejected, "; ")
	if len(remote) > 0 {
		why += " · remote: " + strings.Join(remote, " ")
	}
	return why
}

// lockedRebase runs the rebase under casebook-data's exclusive lock. The
// wait for the lock ends with ctx; the rebase itself runs on a context that
// ctx's cancel doesn't end (serve stopping mid-push), bounded by
// RebaseTimeout, so casebook-data is never left mid-rebase. A rebase that
// fails, or outlives the timeout, is aborted (abort has its own context).
func (r *Repo) lockedRebase(ctx context.Context, res *SyncResult) error {
	unlock, err := r.lock(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RebaseTimeout)
	defer cancel()
	return r.rebase(rctx, res)
}

// rebase replays local commits onto origin/main, resolving decision and view
// conflicts; anything else aborts the rebase and fails. A replayed commit that
// resolution left empty (it only touched a view) is skipped.
func (r *Repo) rebase(ctx context.Context, res *SyncResult) error {
	_, err := gitx.Run(ctx, r.Dir, "rebase", "-q", "origin/main")
	for i := 0; err != nil; i++ {
		if !r.rebasing() || i > 1000 {
			r.abort()
			return fmt.Errorf("rebase onto the casebook remote failed: %w", err)
		}
		files, _ := gitx.Run(ctx, r.Dir, "diff", "--name-only", "--diff-filter=U")
		if files == "" && i == 0 {
			r.abort()
			return fmt.Errorf("rebase onto the casebook remote failed: %w", err)
		}
		if files != "" {
			for _, f := range strings.Split(files, "\n") {
				if rerr := r.resolve(ctx, f, res); rerr != nil {
					r.abort()
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
	if f == ".gitignore" {
		// Upstream's copy wins; the next open puts casebook's rule back
		// if it went (EnsureIgnore).
		if _, err := gitx.Run(ctx, r.Dir, "checkout", "--ours", "--", f); err != nil {
			return err
		}
		_, err := gitx.Run(ctx, r.Dir, "add", "--", f)
		return err
	}
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

// cleanupTimeout bounds rebasing and abort, which run on their own context:
// the rebase's may have ended (its timeout) and the rebase must still be
// found and aborted.
const cleanupTimeout = 30 * time.Second

func (r *Repo) rebasing() bool {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
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

func (r *Repo) abort() {
	if r.rebasing() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_, _ = gitx.Run(ctx, r.Dir, "rebase", "--abort")
	}
}
