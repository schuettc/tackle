package observe

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	tools "github.com/schuettc/tools-common"
)

// NewGitHub returns an empty cache.
func NewGitHub() *GitHub {
	return &GitHub{Version: CacheVersion, Owners: map[string]*Owner{}, Refs: map[string]Ref{}}
}

// LoadGitHub reads the cache. A missing file, or one written by a newer
// ledger, yields an empty cache: it is only a cache.
func LoadGitHub(path string) (*GitHub, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewGitHub(), nil
	}
	if err != nil {
		return nil, err
	}
	g := NewGitHub()
	if err := json.Unmarshal(b, g); err != nil || g.Version != CacheVersion {
		return NewGitHub(), nil
	}
	if g.Owners == nil {
		g.Owners = map[string]*Owner{}
	}
	if g.Refs == nil {
		g.Refs = map[string]Ref{}
	}
	return g, nil
}

// SaveGitHub writes the cache with mode 0600.
func SaveGitHub(path string, g *GitHub) error {
	g.Version = CacheVersion
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteFileAtomic(path, append(b, '\n'), 0o600)
}
