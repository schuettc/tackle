package proj

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	tools "github.com/schuettc/tools-common"
)

// The record is proj's memory across reboots: every work session it has seen,
// the agent conversation each one held, and (when the operator saves it) the
// Ghostty window/tab arrangement. tmux itself forgets all of this when its
// servers die, so it lives in proj's state dir, not in tmux options.
//
// It is upsert-only. A reboot looks exactly like the operator closing every
// session (kill-server fires session-closed for each), so nothing observed
// from tmux may ever remove an entry; only an explicit Forget does.

// recordVersion is the only sessions.json format this build reads or writes.
const recordVersion = 1

// ErrRecordVersion is returned for a sessions.json written by a newer proj.
// proj refuses to rewrite a record it does not understand rather than
// truncating it to the fields it knows.
var ErrRecordVersion = errors.New("proj: unsupported sessions.json version")

// Entry is one remembered session. Conversation is the agent's session id
// (the @harness_session tmux option), empty when no agent announced one.
type Entry struct {
	Socket       string    `json:"socket"`
	Project      string    `json:"project"`
	Dir          string    `json:"dir"`
	Agent        string    `json:"agent"`
	Conversation string    `json:"conversation"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// GhosttyLayout is the Ghostty arrangement captured by the operator: windows in
// order, each an ordered list of session names (one per tab).
type GhosttyLayout struct {
	SavedAt time.Time  `json:"saved_at"`
	Windows [][]string `json:"windows"`
}

// Record is the whole sessions.json document, keyed by session name.
type Record struct {
	Version  int              `json:"version"`
	Sessions map[string]Entry `json:"sessions"`
	Layout   GhosttyLayout    `json:"layout"`
}

// LiveEntry is a session as observed in tmux right now. Its UpdatedAt is
// ignored; mergeLive stamps entries itself.
type LiveEntry struct {
	Name string
	Entry
}

// RecordPath is where the record lives: proj's XDG state dir (honouring
// $PROJ_HOME), which survives a reboot where /tmp does not.
func RecordPath() string {
	return filepath.Join(tools.StateDir("proj"), "sessions.json")
}

// LoadRecord reads the record. A missing file is an empty record, not an
// error; a record from a newer proj is ErrRecordVersion.
func LoadRecord() (Record, error) {
	empty := Record{Version: recordVersion, Sessions: map[string]Entry{}}
	b, err := os.ReadFile(RecordPath())
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return empty, fmt.Errorf("proj: read %s: %w", RecordPath(), err)
	}
	if rec.Version != recordVersion {
		return empty, ErrRecordVersion
	}
	if rec.Sessions == nil {
		rec.Sessions = map[string]Entry{}
	}
	return rec, nil
}

// UpdateRecord applies fn to the record under an exclusive lock and writes the
// result only when fn reports a change. The lock is blocking: tmux hooks on
// several servers fire at once, and a snapshot that gave up on a busy lock
// would silently drop that server's changes.
func UpdateRecord(fn func(*Record) bool) error {
	p := RecordPath()
	if err := tools.EnsureDir(filepath.Dir(p)); err != nil {
		return err
	}
	lf, err := os.OpenFile(p+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lf.Close() }()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck

	rec, err := LoadRecord()
	if err != nil {
		return err
	}
	if !fn(&rec) {
		return nil
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteFileAtomic(p, append(b, '\n'), 0o600)
}

// Forget removes name from the record. It is the only way an entry leaves.
func Forget(name string) error {
	return UpdateRecord(func(r *Record) bool {
		if _, ok := r.Sessions[name]; !ok {
			return false
		}
		delete(r.Sessions, name)
		return true
	})
}

// mergeLive folds the observed sessions into rec and reports whether anything
// changed. It never deletes an entry for being absent. The one removal is a
// rename: an entry that is not live and shares a live session's socket and
// (non-empty) conversation is that session under its old name.
func mergeLive(rec *Record, live []LiveEntry, now time.Time) bool {
	isLive := make(map[string]bool, len(live))
	for _, l := range live {
		isLive[l.Name] = true
	}
	changed := false
	for _, l := range live {
		if l.Conversation != "" {
			for name, e := range rec.Sessions {
				if name != l.Name && !isLive[name] && e.Socket == l.Socket && e.Conversation == l.Conversation {
					delete(rec.Sessions, name)
					changed = true
				}
			}
		}
		next := l.Entry
		old, had := rec.Sessions[l.Name]
		if next.Agent == "" {
			next.Agent = old.Agent
		}
		next.UpdatedAt = old.UpdatedAt
		if had && next == old {
			continue
		}
		next.UpdatedAt = now
		rec.Sessions[l.Name] = next
		changed = true
	}
	return changed
}
