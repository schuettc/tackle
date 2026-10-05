package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/row"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	st.Repo(t, dir, map[string]string{"README.md": "x\n"})
}

// fixtureWorkspace builds a home with a Codex global file and a workspace
// with one published repo whose files hold one hit for every check. It
// writes the config and returns the workspace.
func fixtureWorkspace(t *testing.T, home string) string {
	t.Helper()
	st.Write(t, home, ".codex/AGENTS.md", strings.Join([]string{
		"# Global",
		"",
		"- In webapp, run the slow suite before a release.",
		"- Never force-push a shared branch.",
		"- Search the notes with memory_search first.",
		"",
	}, "\n"))
	ws := t.TempDir()
	dup := "Run the full verification suite before you push, because the hook and CI run exactly the same command."
	var pad []string
	for i := 0; i < 1100; i++ {
		pad = append(pad, fmt.Sprintf("- Item %04d is plain filler text.", i))
	}
	repo := st.Repo(t, filepath.Join(ws, "webapp"), map[string]string{
		"AGENTS.md": strings.Join(append([]string{
			"# webapp",
			"",
			dup,
			"",
			"- Layout notes are in `docs/gone.md`.",
			"- Waiting on the API change before the client can switch.",
			"- password = \"hunter2hunter2\"",
			"",
			"- " + dup,
			"",
		}, pad...), "\n") + "\n",
	})
	st.Publish(t, repo)
	c := config.Default()
	c.Profiles = []string{"codex"}
	c.Roots = []config.Root{{Path: ws}}
	c.Retired = []config.Retired{{Name: "old notes", Patterns: []string{"memory_search"}}}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	return ws
}

type checkOut struct {
	Round   int64          `json:"round"`
	Summary map[string]int `json:"summary"`
	Rows    []struct {
		Check   string `json:"check"`
		Certain bool   `json:"certain"`
	} `json:"rows"`
	Muted int `json:"muted"`
}

// The plan's done-when: on a fixture workspace, one hit per check.
func TestCheckFindsOneHitPerCheck(t *testing.T) {
	home := siftEnv(t)
	fixtureWorkspace(t, home)
	code, out, errw := run(t, "", "check", "--json")
	if code != 1 {
		t.Fatalf("code %d (want 1: findings)\n%s\n%s", code, out, errw)
	}
	var got checkOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var checks []string
	for c := range got.Summary {
		checks = append(checks, c)
	}
	sort.Strings(checks)
	want := "dead-path duplicate load-limit misplaced negative-rule retired-store secret size stale-status"
	if strings.Join(checks, " ") != want {
		t.Fatalf("checks with findings: %v\nsummary %v", checks, got.Summary)
	}
	if got.Round == 0 {
		t.Error("no round recorded")
	}

	s, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r, rows, err := s.LatestRound(context.Background())
	if err != nil || r.ID != got.Round || len(rows) != len(got.Rows) {
		t.Fatalf("stored round %+v, %d rows, %v", r, len(rows), err)
	}
}

// A muted row stays out of the next round.
func TestCheckLeavesMutedRowsOut(t *testing.T) {
	home := siftEnv(t)
	fixtureWorkspace(t, home)
	_, out, _ := run(t, "", "check", "--json")
	var first checkOut
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	_, rows, _ := s.LatestRound(context.Background())
	if err := s.Mute(context.Background(), rows[0].ID); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, out, _ = run(t, "", "check", "--json")
	var second checkOut
	if err := json.Unmarshal([]byte(out), &second); err != nil {
		t.Fatal(err)
	}
	if second.Muted != 1 || len(second.Rows) != len(first.Rows)-1 {
		t.Fatalf("muted %d, rows %d then %d", second.Muted, len(first.Rows), len(second.Rows))
	}
}

func TestCheckTableAndNoFindings(t *testing.T) {
	home := siftEnv(t)
	fixtureWorkspace(t, home)
	code, out, _ := run(t, "", "check")
	if code != 1 || !strings.Contains(out, "size") || !strings.Contains(out, "AGENTS.md") {
		t.Fatalf("code %d\n%s", code, out)
	}

	// A clean workspace: exit 0.
	siftEnv(t)
	ws := t.TempDir()
	st.Repo(t, filepath.Join(ws, "clean"), map[string]string{"AGENTS.md": "# Rules\n\n- Keep changes small.\n"})
	c := config.Default()
	c.Profiles = []string{"codex"}
	c.Roots = []config.Root{{Path: ws}}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	if code, out, errw := run(t, "", "check"); code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errw)
	}
}

func TestCheckWithoutConfig(t *testing.T) {
	siftEnv(t)
	code, _, errw := run(t, "", "check")
	if code != 2 || !strings.Contains(errw, "sift init") {
		t.Fatalf("code %d %q", code, errw)
	}
}

// Repeated identical rules and a paragraph pasted twice in one file record
// as a round, and every row can be answered on its own.
func TestCheckRecordsRepeatedPassages(t *testing.T) {
	siftEnv(t)
	ws := t.TempDir()
	para := "Run the full verification suite before you push, because the hook and CI run exactly the same command."
	st.Repo(t, filepath.Join(ws, "app"), map[string]string{
		"CLAUDE.md": "# app\n\n- Never force-push.\n- Never force-push.\n- Never force-push.\n\n" + para + "\n\n" + para + "\n",
	})
	c := config.Default()
	c.Profiles = []string{"claude-code"}
	c.Roots = []config.Root{{Path: ws}}
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	code, out, errw := run(t, "", "check", "--json")
	if code != 1 || strings.Contains(errw, "not recorded") {
		t.Fatalf("code %d\n%s\n%s", code, out, errw)
	}
	var got checkOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Round == 0 || got.Summary["negative-rule"] != 3 || got.Summary["duplicate"] != 2 {
		t.Fatalf("round %d summary %v", got.Round, got.Summary)
	}
	s, err := store.Open(context.Background(), store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	_, rows, err := s.LatestRound(context.Background())
	if err != nil || len(rows) != len(got.Rows) {
		t.Fatalf("%d rows stored, %v", len(rows), err)
	}
	for _, r := range rows {
		if err := s.Decide(context.Background(), got.Round, r.ID, row.Decision{Action: "reject", Note: r.ID}); err != nil {
			t.Fatalf("row %s: %v", r.ID, err)
		}
	}
	_, rows, _ = s.LatestRound(context.Background())
	for _, r := range rows {
		if r.Decision == nil || r.Decision.Note != r.ID {
			t.Errorf("row %s has decision %+v", r.ID, r.Decision)
		}
	}
}
