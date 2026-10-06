package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

// seedRound records a backlog round of two rows in the test's state and
// returns them.
func seedRound(t *testing.T) (int64, []row.Row) {
	t.Helper()
	s, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows := []row.Row{
		{ID: row.ID("/w/a/CLAUDE.md", "stale-status", "- Never push."), Check: "stale-status", Summary: "s",
			Source: row.Source{File: "/w/a/CLAUDE.md", Start: 3, End: 3}, Passage: "- Never push."},
		{ID: row.ID("/w/a/CLAUDE.md", "size", "f"), Check: "size", Summary: "s", Source: row.Source{File: "/w/a/CLAUDE.md"}},
	}
	id, err := s.RecordRound(context.Background(), store.Round{Kind: "backlog"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	return id, rows
}

func jsonl(t *testing.T, rs ...any) string {
	t.Helper()
	var b strings.Builder
	for _, r := range rs {
		j, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestRowsAddMergesProposals(t *testing.T) {
	siftEnv(t)
	id, rows := seedRound(t)
	in := jsonl(t,
		map[string]any{"id": rows[0].ID, "verdict": "rewrite", "text": "- Push to a branch.", "reason": "guidance"},
		map[string]any{"id": "mem-1", "check": "intake", "summary": "an entry", "source": map[string]any{"entry": "MEMORY.md#1"}, "verdict": "issue", "title": "t", "destination": "o/r"},
	)
	code, out, errw := run(t, in, "rows", "add")
	if code != 0 || !strings.Contains(out, "1 updated, 1 added") {
		t.Fatalf("code %d out %q err %q", code, out, errw)
	}
	s, _ := store.Open(context.Background(), store.Path())
	defer func() { _ = s.Close() }()
	_, got, _ := s.Round(context.Background(), id)
	if len(got) != 3 || got[0].Verdict != "rewrite" || got[2].ID != "mem-1" {
		t.Fatalf("rows %+v", got)
	}
}

func TestRowsAddRejects(t *testing.T) {
	siftEnv(t)
	_, rows := seedRound(t)
	for name, c := range map[string]struct{ in, want string }{
		"unknown verdict": {jsonl(t, map[string]any{"id": rows[0].ID, "verdict": "cut"}), "not a verdict"},
		"not in round":    {jsonl(t, map[string]any{"id": "ffffffffffffffff", "verdict": "keep"}), "not in round"},
		"unknown field":   {jsonl(t, map[string]any{"id": rows[0].ID, "verdikt": "keep"}), "line 1"},
		"not json":        {"{\"id\": \n", "line 1"},
		"empty":           {"\n", "no rows"},
	} {
		code, _, errw := run(t, c.in, "rows", "add")
		if code != 2 || !strings.Contains(errw, c.want) {
			t.Errorf("%s: code %d err %q, want %q", name, code, errw, c.want)
		}
	}
}

func TestRowsAddNeedsARound(t *testing.T) {
	siftEnv(t)
	code, _, errw := run(t, "{\"id\":\"x\"}\n", "rows", "add")
	if code != 2 || !strings.Contains(errw, "sift check") {
		t.Fatalf("code %d err %q", code, errw)
	}
}

// An audit round's findings are answered per file: rows add says so.
func TestRowsAddPointsAnAuditRoundAtPropose(t *testing.T) {
	home := siftEnv(t)
	fixtureWorkspace(t, home)
	_, _, _ = run(t, "", "check")
	code, _, errw := run(t, jsonl(t, map[string]any{"id": "x", "verdict": "keep"}), "rows", "add")
	if code != 2 || !strings.Contains(errw, "sift propose") {
		t.Fatalf("%d %s", code, errw)
	}
}
