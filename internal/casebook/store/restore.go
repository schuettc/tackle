package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// RestoreRecord is one line of a restores/<date>.tsv file: the undo information
// recorded before a destructive apply step runs. Before holds the state that
// the step overwrites (a branch tip, an archived flag, a PR state) and
// RestoreCommand is the single deterministic command that reverses the step.
type RestoreRecord struct {
	Key            string
	Action         string
	Before         string
	RestoreCommand string
}

const restoreHeader = "key\taction\tbefore\trestore-command\n"

// AppendRestore appends rec as a TAB-separated line to restores/<date>.tsv,
// writing the header on first creation, and commits the change to casebook-data
// with the message "restore record for <key> (<action>)". It returns whether a
// commit was made. The commit is the safety gate: a destructive step must only
// run after AppendRestore reports a successful commit.
func (r *Repo) AppendRestore(ctx context.Context, date string, rec RestoreRecord) (bool, error) {
	rel := "restores/" + date + ".tsv"
	var buf []byte
	if _, err := os.Stat(r.abs(rel)); errors.Is(err, fs.ErrNotExist) {
		buf = append(buf, restoreHeader...)
	} else if err != nil {
		return false, err
	}
	line := strings.Join([]string{
		rec.Key,
		rec.Action,
		rec.Before,
		rec.RestoreCommand,
	}, "\t") + "\n"
	buf = append(buf, line...)
	if err := r.AppendFile(rel, buf); err != nil {
		return false, err
	}
	return r.Commit(ctx, fmt.Sprintf("restore record for %s (%s)", rec.Key, rec.Action))
}
