// Package key is where cull finds the TypeSafe API key: the environment
// first, then cull's own key file written by `cull init`. The key is never
// logged, printed or put in an error.
package key

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tools "github.com/schuettc/tools-common"
)

// ErrMissing means neither the environment nor the key file has a key.
var ErrMissing = errors.New("no TypeSafe key: run cull init")

const maxLen = 4096

// Path is the key file: tools.ConfigDir("cull")/key.
func Path() string { return filepath.Join(tools.ConfigDir("cull"), "key") }

// Load returns the key and where it came from: "environment" or the file's
// path. ErrMissing when neither has one. A key that fails Save's rules is an
// error naming the source, never the value.
func Load() (k, source string, err error) {
	if v := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY")); v != "" {
		if err := validate(v); err != nil {
			return "", "", fmt.Errorf("TYPESAFE_API_KEY in the environment is invalid: %w", err)
		}
		return v, "environment", nil
	}
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return "", "", ErrMissing
	}
	if err != nil {
		return "", "", err
	}
	if v := strings.TrimSpace(string(b)); v != "" {
		if err := validate(v); err != nil {
			return "", "", fmt.Errorf("the key file %s is invalid: %w", Path(), err)
		}
		return v, Path(), nil
	}
	return "", "", ErrMissing
}

// Exists reports whether the key file exists (the environment is ignored).
func Exists() bool {
	_, err := os.Stat(Path())
	return err == nil
}

func validate(k string) error {
	if k == "" {
		return errors.New("the key is empty")
	}
	if len(k) > maxLen {
		return errors.New("the key is longer than 4096 characters")
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return errors.New("the key has a space, control or non-ASCII character")
		}
	}
	return nil
}

// Save validates k (non-empty, printable ASCII, at most 4096 bytes) and
// writes it atomically to Path() with mode 0600 in a 0700 directory.
// Errors never include k.
func Save(k string) error {
	if err := validate(k); err != nil {
		return err
	}
	dir := filepath.Dir(Path())
	if err := tools.EnsureDir(dir); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	return tools.WriteFileAtomic(Path(), []byte(k+"\n"), 0o600)
}
