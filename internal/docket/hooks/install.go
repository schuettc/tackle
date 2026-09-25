package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/schuettc/tackle/internal/docket/gitx"
	tools "github.com/schuettc/tools-common"
)

// Options says where the shims and install state live and which binary they call.
type Options struct {
	Dir       string // shim directory, becomes global core.hooksPath
	StatePath string // install record (previous hooksPath)
	Binary    string // absolute path of the docket executable
}

// State is the install record.
type State struct {
	Prev        string    `json:"prev"`
	Binary      string    `json:"binary"`
	InstalledAt time.Time `json:"installed_at"`
}

// Status describes the installation.
type Status struct {
	Installed       bool     // global core.hooksPath points at Dir
	GlobalHooksPath string   // current global core.hooksPath
	Prev            string   // what install replaced
	Binary          string   // binary the shims call
	BinaryOK        bool     // it exists and is executable
	Missing         []string // hook names without a shim
}

func globalHooksPath(ctx context.Context) string {
	v, _ := gitx.Run(ctx, "", "config", "--global", "--get", "core.hooksPath")
	return v
}

func readState(p string) (State, error) {
	var s State
	b, err := os.ReadFile(p)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Install writes every shim and points global core.hooksPath at them,
// remembering the previous value so the shims chain to it and uninstall
// restores it. Re-running it refreshes the shims.
func Install(ctx context.Context, o Options) error {
	if !filepath.IsAbs(o.Dir) || !filepath.IsAbs(o.Binary) {
		return fmt.Errorf("hooks: shim dir and binary must be absolute paths")
	}
	cur := globalHooksPath(ctx)
	prev := cur
	if cur != "" && filepath.Clean(cur) == filepath.Clean(o.Dir) {
		st, err := readState(o.StatePath)
		if err != nil {
			return fmt.Errorf("hooks: core.hooksPath already points at %s but the install record is unreadable: %w", o.Dir, err)
		}
		prev = st.Prev
	}
	if err := tools.EnsureDir(o.Dir); err != nil {
		return err
	}
	for _, n := range Names {
		if err := tools.WriteFileAtomic(filepath.Join(o.Dir, n), Shim(n, o.Binary, prev), 0o755); err != nil {
			return err
		}
	}
	entries, _ := os.ReadDir(o.Dir)
	for _, e := range entries {
		if !slices.Contains(Names, e.Name()) {
			_ = os.Remove(filepath.Join(o.Dir, e.Name()))
		}
	}
	b, _ := json.MarshalIndent(State{Prev: prev, Binary: o.Binary, InstalledAt: time.Now().UTC()}, "", "  ")
	if err := tools.WriteFileAtomic(o.StatePath, append(b, '\n'), 0o600); err != nil {
		return err
	}
	_, err := gitx.Run(ctx, "", "config", "--global", "core.hooksPath", o.Dir)
	return err
}

// Uninstall restores the previous global core.hooksPath (or unsets it) and
// removes the shims. If someone repointed core.hooksPath since, it is left alone.
func Uninstall(ctx context.Context, o Options) error {
	st, err := readState(o.StatePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if filepath.Clean(globalHooksPath(ctx)) == filepath.Clean(o.Dir) {
		if st.Prev != "" {
			_, err = gitx.Run(ctx, "", "config", "--global", "core.hooksPath", st.Prev)
		} else {
			_, err = gitx.Run(ctx, "", "config", "--global", "--unset", "core.hooksPath")
		}
		if err != nil {
			return err
		}
	}
	if err := os.RemoveAll(o.Dir); err != nil {
		return err
	}
	if err := os.Remove(o.StatePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// GetStatus inspects the installation.
func GetStatus(ctx context.Context, o Options) (Status, error) {
	s := Status{GlobalHooksPath: globalHooksPath(ctx)}
	s.Installed = s.GlobalHooksPath != "" && filepath.Clean(s.GlobalHooksPath) == filepath.Clean(o.Dir)
	if st, err := readState(o.StatePath); err == nil {
		s.Prev, s.Binary = st.Prev, st.Binary
	}
	if fi, err := os.Stat(s.Binary); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		s.BinaryOK = true
	}
	for _, n := range Names {
		b, err := os.ReadFile(filepath.Join(o.Dir, n))
		if err != nil || !bytes.Contains(b, []byte("docket hook shim")) {
			s.Missing = append(s.Missing, n)
		}
	}
	return s, nil
}
