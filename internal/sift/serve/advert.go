// Package serve is `sift serve`: one local server per machine that serves the
// review page's API over the store, streams live events, and writes an advert
// so other processes can find (and stop) it.
package serve

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	tools "github.com/schuettc/tools-common"
)

// Advert is how a running serve is found: StateDir/live/serve.json (0600, in
// a 0700 directory). It carries the token so a same-user process can
// authenticate with the X-Local-Token header.
type Advert struct {
	URL       string    `json:"url"`  // page URL, with ?t= for the first load
	Base      string    `json:"base"` // http://127.0.0.1:<port>
	Token     string    `json:"token"`
	PID       int       `json:"pid"`
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
}

// StateDir is sift's state directory.
func StateDir() string { return tools.StateDir("sift") }

// LiveDir holds the advert.
func LiveDir() string { return filepath.Join(StateDir(), "live") }

// AdvertPath is the advert file.
func AdvertPath() string { return filepath.Join(LiveDir(), "serve.json") }

func writeAdvert(a Advert) error {
	if err := os.MkdirAll(LiveDir(), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(LiveDir(), 0o700)
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteFileAtomic(AdvertPath(), append(b, '\n'), 0o600)
}

func removeAdvert(pid int) {
	if a, err := readAdvert(); err == nil && a.PID == pid {
		_ = os.Remove(AdvertPath())
	}
}

func readAdvert() (Advert, error) {
	var a Advert
	b, err := os.ReadFile(AdvertPath())
	if err != nil {
		return a, err
	}
	return a, json.Unmarshal(b, &a)
}

// ErrNotRunning means no live serve on this machine.
var ErrNotRunning = errors.New("sift serve is not running")

// Running returns the live serve's advert. A stale advert (dead PID) is
// removed and reported as not running.
func Running() (Advert, error) {
	a, err := readAdvert()
	if errors.Is(err, fs.ErrNotExist) {
		return a, ErrNotRunning
	}
	if err != nil {
		return a, err
	}
	if !tools.PIDAlive(a.PID) {
		_ = os.Remove(AdvertPath())
		return a, ErrNotRunning
	}
	return a, nil
}
