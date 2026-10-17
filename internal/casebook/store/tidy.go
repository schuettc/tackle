package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/gitx"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
)

// tidyPolicyMsg is the commit that deletes an older version's policy.toml.
const tidyPolicyMsg = "remove policy.toml: casebook no longer flags by age"

// tidySeedMsg is the commit that adds the example filters.
const tidySeedMsg = "add example filters"

// tidyRulesMsg is the commit that adds the example rules.
const tidyRulesMsg = "add example rules"

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
	if err := r.seedFilters(ctx); err != nil {
		return err
	}
	return r.seedRules(ctx)
}

// seedRules gives a repo whose rules/ has no .gitkeep the example rules
// (on), leaving any rule it already has under an example's id as it is.
// rules/.gitkeep is never removed, so the examples arrive once.
func (r *Repo) seedRules(ctx context.Context) error {
	if _, err := os.Stat(r.abs("rules/.gitkeep")); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	files, err := exampleRuleFiles(time.Now().UTC())
	if err != nil {
		return err
	}
	for rel, b := range files {
		if _, err := os.Stat(r.abs(rel)); err == nil {
			continue
		}
		if _, err := r.WriteFile(rel, b); err != nil {
			return err
		}
	}
	return r.commitOnly(ctx, tidyRulesMsg, "rules")
}

// exampleRuleFiles are the example rules as files, and rules/.gitkeep.
func exampleRuleFiles(now time.Time) (map[string][]byte, error) {
	out := map[string][]byte{"rules/.gitkeep": {}}
	for _, ru := range rules.Examples() {
		ru.CreatedAt, ru.EditedAt = now, now
		b, err := rules.Encode(ru)
		if err != nil {
			return nil, err
		}
		out["rules/"+ru.ID+".toml"] = b
	}
	return out, nil
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
