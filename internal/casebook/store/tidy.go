package store

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/subaud-tools/tackle/internal/casebook/gitx"
)

// tidyPolicyMsg is the commit that deletes an older version's policy.toml.
const tidyPolicyMsg = "remove policy.toml: casebook no longer flags by age"

// tidySeedMsg is the commit that adds the example filters.
const tidySeedMsg = "add example filters"

// tidy is casebook-data's housekeeping, run by Batch and BatchWithin under
// the exclusive lock before the caller's write: it deletes a policy.toml an
// older casebook wrote, and gives a repo with no filters/ the example
// filters, each as its own commit holding only its paths, so the caller's
// push carries them. filters/.gitkeep is never removed, so the examples
// arrive once. It needs no network and does nothing when there is nothing
// to tidy.
func (r *Repo) tidy(ctx context.Context) error {
	if err := r.tidyPolicy(ctx); err != nil {
		return err
	}
	return r.seedFilters(ctx)
}

func (r *Repo) tidyPolicy(ctx context.Context) error {
	if _, err := os.Stat(r.abs("policy.toml")); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(r.abs("policy.toml")); err != nil {
		return err
	}
	return r.commitOnly(ctx, tidyPolicyMsg, "policy.toml")
}

func (r *Repo) seedFilters(ctx context.Context) error {
	if _, err := os.Stat(r.abs("filters")); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	files, err := exampleFiles()
	if err != nil {
		return err
	}
	for rel, b := range files {
		if _, err := r.WriteFile(rel, b); err != nil {
			return err
		}
	}
	return r.commitOnly(ctx, tidySeedMsg, "filters")
}

// commitOnly stages and commits paths alone, with msg; nothing when they
// hold no change. Files a caller wrote before its own commit (as Init does)
// are never swept in. casebook-data's exclusive lock is held.
func (r *Repo) commitOnly(ctx context.Context, msg string, paths ...string) error {
	if _, err := gitx.Run(ctx, r.Dir, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return err
	}
	if _, err := gitx.Run(ctx, r.Dir, append([]string{"diff", "--cached", "--quiet", "--"}, paths...)...); err == nil {
		return nil
	}
	_, err := gitx.Run(ctx, r.Dir, append([]string{"commit", "-q", "--no-verify", "-m", msg, "--"}, paths...)...)
	return err
}
