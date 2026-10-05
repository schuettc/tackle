package cli

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
	tools "github.com/schuettc/tools-common"
)

var rowsFlags = flags("rows", "sift rows add [--round N] < rows.jsonl",
	"add: reads rows as JSON lines on stdin and merges them into the latest round (or --round N),\n"+
		"a backlog or intake round, decided per item (an audit round's findings are answered per\n"+
		"file: sift next, sift propose), all or nothing. A row the round has gets the agent's proposal: verdict, title,\n"+
		"destination, text and reason; what the check found stays. An intake row (check \"intake\")\n"+
		"is added when the round lacks it. Refused: unknown fields, an unknown verdict, an id not\n"+
		"in the round (other than an intake row), a decision (those are made on the page). A\n"+
		"decision on a row whose proposal changed is dropped. Exit 0 merged, 2 refused.",
	func(fs *flag.FlagSet) {
		fs.Int64("round", 0, "the round to merge into (default: the latest)")
	})

func runRows(stdin io.Reader) func(args []string, out, errw io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		fs := rowsFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		if len(pos) != 1 || pos[0] != "add" {
			return tools.UsageError{Msg: "usage: sift rows add [--round N] < rows.jsonl"}
		}
		in, err := readRows(stdin)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		ctx := context.Background()
		s, err := store.Open(ctx, store.Path())
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		defer func() { _ = s.Close() }()
		id := fs.Lookup("round").Value.(flag.Getter).Get().(int64)
		if id == 0 {
			if id, _, err = s.Latest(ctx); errors.Is(err, sql.ErrNoRows) {
				return tools.Exitf(2, "no round yet").WithHint("sift check")
			} else if err != nil {
				return tools.Exitf(2, "%v", err)
			}
		}
		res, err := s.AddRows(ctx, id, in)
		if err != nil {
			return tools.Exitf(2, "%v", err)
		}
		_, _ = fmt.Fprintf(out, "round %d: %d updated, %d added", id, res.Updated, res.Added)
		if res.Cleared > 0 {
			_, _ = fmt.Fprintf(out, ", %d decision(s) dropped because their proposal changed", res.Cleared)
		}
		_, _ = fmt.Fprintln(out)
		return nil
	}
}

// readRows parses JSON lines (blank lines skipped), refusing unknown fields.
func readRows(r io.Reader) ([]row.Row, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var out []row.Row
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		var rw row.Row
		if err := dec.Decode(&rw); err != nil {
			return nil, fmt.Errorf("line %d: %v", n, strings.TrimPrefix(err.Error(), "json: "))
		}
		out = append(out, rw)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no rows on stdin")
	}
	return out, nil
}
