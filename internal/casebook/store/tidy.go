package store

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/schuettc/tackle/internal/casebook/gitx"
)

// tidyPolicyMsg is the commit that deletes an older version's policy.toml.
const tidyPolicyMsg = "remove policy.toml: casebook no longer flags by age"

// tidy is casebook-data's housekeeping, run by Batch and BatchWithin under
// the exclusive lock before the caller's write: it deletes a policy.toml an
// older casebook wrote, as its own commit holding only that path, so the
// caller's push carries it. It needs no network and does nothing when there
// is nothing to tidy.
func (r *Repo) tidy(ctx context.Context) error {
	if _, err := os.Stat(r.abs("policy.toml")); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(r.abs("policy.toml")); err != nil {
		return err
	}
	_, err := r.commitOnly(ctx, tidyPolicyMsg, "policy.toml")
	return err
}

// commitOnly stages and commits paths alone, with msg; false when they hold
// no change. Files a caller wrote before its own commit (as Init does) are
// never swept in. casebook-data's exclusive lock is held.
func (r *Repo) commitOnly(ctx context.Context, msg string, paths ...string) (bool, error) {
	if _, err := gitx.Run(ctx, r.Dir, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return false, err
	}
	if _, err := gitx.Run(ctx, r.Dir, append([]string{"diff", "--cached", "--quiet", "--"}, paths...)...); err == nil {
		return false, nil
	}
	if _, err := gitx.Run(ctx, r.Dir, append([]string{"commit", "-q", "--no-verify", "-m", msg, "--"}, paths...)...); err != nil {
		return false, err
	}
	return true, nil
}
