// Package notes is the file layer for the scratch scratchpad: path
// resolution, atomic reads/writes, append, and the reload decision.
package notes

import (
	"errors"
	"os"
	"strings"

	tools "github.com/schuettc/tools-common"
)

// Path resolution — which pad file this invocation uses — lives in
// locate.go.

// Read returns the file contents. A missing file is not an error: it
// returns ("", nil).
func Read(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Write atomically replaces path's contents: it writes a temp file in the
// same directory, fsyncs, then renames over path. On write failure the temp
// file is cleaned up; on rename failure the temp file is kept and its path is
// reported so no content is lost.
func Write(path, content string) error {
	// The store is created on first write rather than at resolution time, so
	// merely asking where a pad lives never leaves a directory behind.
	// tools.WriteFileAtomic is this function's old body promoted to the
	// family: unique temp, fsync, rename, temp kept if the rename fails.
	return tools.WriteFileAtomic(path, []byte(content), 0o600)
}

// Append atomically adds line (with a trailing newline) to the file,
// inserting a separating newline if the existing content lacks one.
func Append(path, line string) error {
	cur, err := Read(path)
	if err != nil {
		return err
	}
	if cur != "" && !strings.HasSuffix(cur, "\n") {
		cur += "\n"
	}
	return Write(path, cur+line+"\n")
}

// Action is the decision for an observed on-disk change.
type Action int

const (
	// Ignore means the change is our own write echoing back — do nothing.
	Ignore Action = iota
	// Reload means load the disk version into a clean buffer.
	Reload
	// Flag means there are unsaved local edits — surface a "changed on
	// disk" indicator but do not overwrite.
	Flag
)

// Classify decides what to do when the file changes on disk. lastWritten is
// the content the editor believes is on disk (last saved or loaded).
func Classify(diskContent, lastWritten string, dirty bool) Action {
	if diskContent == lastWritten {
		return Ignore
	}
	if dirty {
		return Flag
	}
	return Reload
}
