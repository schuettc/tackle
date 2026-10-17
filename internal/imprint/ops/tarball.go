package ops

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadTarball resolves path to an absolute one (npm reads a relative path
// as a GitHub repo) and reads the name and version from the package.json
// at the tarball's top directory.
func ReadTarball(path string) (abs, name, version string, err error) {
	abs, err = filepath.Abs(path)
	if err != nil {
		return "", "", "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", "", "", fmt.Errorf("%s is not a gzipped tarball: %w", abs, err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", "", "", fmt.Errorf("%s holds no package.json", abs)
		}
		if err != nil {
			return "", "", "", fmt.Errorf("reading %s: %w", abs, err)
		}
		dir, file, ok := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		if !ok || dir == "" || file != "package.json" {
			continue
		}
		var pj struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(io.LimitReader(tr, 4<<20)).Decode(&pj); err != nil {
			return "", "", "", fmt.Errorf("%s: package.json: %w", abs, err)
		}
		if pj.Name == "" || pj.Version == "" {
			return "", "", "", fmt.Errorf("%s: package.json has no name or version", abs)
		}
		return abs, pj.Name, pj.Version, nil
	}
}
