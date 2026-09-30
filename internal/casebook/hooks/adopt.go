package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/schuettc/tackle/internal/casebook/gitx"
	tools "github.com/schuettc/tools-common"
)

// AdoptResult reports what Adopt did.
type AdoptResult struct {
	Repo    string // absolute top-level path
	Prev    string // the repo's previous local core.hooksPath
	Already bool   // it was already adopted
}

// topLevel resolves the working-tree top level of repoDir, refusing bare repos.
func topLevel(ctx context.Context, repoDir string) (string, error) {
	top, err := gitx.Run(ctx, repoDir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return "", fmt.Errorf("adopt needs a working tree")
	}
	return top, nil
}

func localHooksPath(ctx context.Context, dir string) string {
	v, _ := gitx.Run(ctx, dir, "config", "--local", "--get", "core.hooksPath")
	return v
}

func localPrevHooksPath(ctx context.Context, dir string) string {
	v, _ := gitx.Run(ctx, dir, "config", "--local", "--get", "casebook.prevHooksPath")
	return v
}

// Adopt points a repo with a local core.hooksPath at casebook's shims, recording
// its previous value in the repo's own git config so the shims chain to it.
func Adopt(ctx context.Context, o Options, repoDir string) (AdoptResult, error) {
	top, err := topLevel(ctx, repoDir)
	if err != nil {
		return AdoptResult{}, err
	}
	st, err := GetStatus(ctx, o)
	if err != nil {
		return AdoptResult{}, err
	}
	if !st.Installed {
		return AdoptResult{}, fmt.Errorf("install the global hooks first: casebook hooks install")
	}
	cur := localHooksPath(ctx, top)
	if cur != "" && filepath.Clean(cur) == filepath.Clean(o.Dir) {
		if prev := localPrevHooksPath(ctx, top); prev != "" {
			if err := addAdopted(o, top); err != nil {
				return AdoptResult{}, err
			}
			return AdoptResult{Repo: top, Prev: prev, Already: true}, nil
		}
	}
	if cur == "" {
		return AdoptResult{}, fmt.Errorf("%s has no local core.hooksPath; the global hooks already cover it", top)
	}
	if _, err := gitx.Run(ctx, top, "config", "--local", "casebook.prevHooksPath", cur); err != nil {
		return AdoptResult{}, err
	}
	if _, err := gitx.Run(ctx, top, "config", "--local", "core.hooksPath", o.Dir); err != nil {
		return AdoptResult{}, err
	}
	if err := addAdopted(o, top); err != nil {
		return AdoptResult{}, err
	}
	return AdoptResult{Repo: top, Prev: cur}, nil
}

// Release restores a repo's own core.hooksPath and drops it from state.
func Release(ctx context.Context, o Options, repoDir string) error {
	top, err := gitx.Run(ctx, repoDir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		// A missing (or no longer a) repo dir: just drop it from state.
		return removeAdopted(o, repoDir)
	}
	prev := localPrevHooksPath(ctx, top)
	if prev == "" && !stateHasAdopted(o, top) {
		return fmt.Errorf("%s is not adopted", top)
	}
	if filepath.Clean(localHooksPath(ctx, top)) == filepath.Clean(o.Dir) {
		if prev != "" {
			if _, err := gitx.Run(ctx, top, "config", "--local", "core.hooksPath", prev); err != nil {
				return err
			}
		} else {
			if _, err := gitx.Run(ctx, top, "config", "--local", "--unset", "core.hooksPath"); err != nil {
				return err
			}
		}
	}
	if prev != "" {
		if _, err := gitx.Run(ctx, top, "config", "--local", "--unset", "casebook.prevHooksPath"); err != nil {
			return err
		}
	}
	return removeAdopted(o, top)
}

func stateHasAdopted(o Options, repo string) bool {
	st, err := readState(o.StatePath)
	if err != nil {
		return false
	}
	return slices.Contains(st.Adopted, repo)
}

func addAdopted(o Options, repo string) error {
	st, err := readState(o.StatePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !slices.Contains(st.Adopted, repo) {
		st.Adopted = append(st.Adopted, repo)
	}
	slices.Sort(st.Adopted)
	st.Adopted = slices.Compact(st.Adopted)
	return writeState(o.StatePath, st)
}

func removeAdopted(o Options, repo string) error {
	st, err := readState(o.StatePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	st.Adopted = slices.DeleteFunc(st.Adopted, func(s string) bool { return s == repo })
	return writeState(o.StatePath, st)
}

func writeState(p string, st State) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return tools.WriteFileAtomic(p, append(b, '\n'), 0o600)
}
