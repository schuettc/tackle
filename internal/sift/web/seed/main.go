// Command seed is a dev-only helper for the review page's probe: it writes a
// fixture's files under a base directory (each named repo as a git repo with
// one commit, the rest as plain files), opens the sift store under a
// SIFT_HOME and records ONE round of the fixture's rows, their sources
// pointing at those files. It is not part of the shipped binary.
//
//	go run ./internal/sift/web/seed [-scale N] <SIFT_HOME> <round.json> <base dir>
//
// -scale N pads the round to N rows with negative-rule findings spread over
// generated files, as a large real round would be.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

type fixtureRow struct {
	row.Row
	Source struct {
		Rel   string `json:"rel"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	} `json:"source"`
}

type fixture struct {
	Kind  string            `json:"kind"`
	Repos []string          `json:"repos"`
	Files map[string]string `json:"files"`
	Rows  []fixtureRow      `json:"rows"`
}

func git(ctx context.Context, dir string, args ...string) error {
	cmd := discover.Git(ctx, dir, args...)
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=seed", "GIT_AUTHOR_EMAIL=seed@example.invalid",
		"GIT_COMMITTER_NAME=seed", "GIT_COMMITTER_EMAIL=seed@example.invalid", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// Seed writes the fixture under base and records its round in the store at
// path; it returns the round's id.
func Seed(ctx context.Context, path string, data []byte, base string, scale int) (int64, error) {
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return 0, fmt.Errorf("fixture: %w", err)
	}
	if scale > len(fx.Rows) {
		pad(&fx, scale)
	}
	names := make([]string, 0, len(fx.Files))
	for rel := range fx.Files {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(p, []byte(fx.Files[rel]), 0o644); err != nil {
			return 0, err
		}
	}
	for _, r := range fx.Repos {
		dir := filepath.Join(base, r)
		for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
			if err := git(ctx, dir, args...); err != nil {
				return 0, err
			}
		}
	}
	rows := make([]row.Row, 0, len(fx.Rows))
	for _, fr := range fx.Rows {
		r := fr.Row
		rel := fr.Source.Rel
		r.Source = row.Source{File: filepath.Join(base, filepath.FromSlash(rel)), Start: fr.Source.Start, End: fr.Source.End}
		for _, repo := range fx.Repos {
			if p, ok := strings.CutPrefix(rel, repo+"/"); ok {
				r.Source.Repo, r.Source.Ref, r.Source.Path = filepath.Join(base, repo), "HEAD", p
			}
		}
		rows = append(rows, r)
	}
	st, err := store.Open(ctx, path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close() }()
	sum := map[string]int{}
	for _, r := range rows {
		sum[r.Check]++
	}
	return st.RecordRound(ctx, store.Round{Kind: fx.Kind, Summary: sum}, rows)
}

// pad adds generated files to the fixture's first repo and negative-rule
// rows in them until it has n rows: forty files of varying length.
func pad(fx *fixture, n int) {
	repo := fx.Repos[0]
	for i := 0; len(fx.Rows) < n; i++ {
		rel := fmt.Sprintf("%s/pkg%02d/AGENTS.md", repo, i%40)
		lines := strings.Split(strings.TrimSuffix(fx.Files[rel], "\n"), "\n")
		if fx.Files[rel] == "" {
			lines = []string{fmt.Sprintf("# pkg%02d", i%40), ""}
		}
		text := fmt.Sprintf("- Never call helper %d from a request handler; it blocks on the disk.", i)
		lines = append(lines, text)
		fx.Files[rel] = strings.Join(lines, "\n") + "\n"
		var fr fixtureRow
		fr.Row = row.Row{ID: row.ID(rel, "negative-rule", fmt.Sprint(text, i)), Check: "negative-rule",
			Summary: "a rule phrased as a prohibition", Passage: text, Evidence: []row.Fact{{Name: "matched", Value: "Never"}}}
		if i%2 == 0 {
			fr.Verdict, fr.Text, fr.Reason = "rewrite", fmt.Sprintf("- Call helper %d from a background job.", i), "Positive form."
		}
		fr.Source.Rel, fr.Source.Start, fr.Source.End = rel, len(lines), len(lines)
		fx.Rows = append(fx.Rows, fr)
	}
}

func main() {
	scale := flag.Int("scale", 0, "pad the round to this many rows")
	flag.Parse()
	if flag.NArg() != 3 {
		fmt.Fprintln(os.Stderr, "usage: seed [-scale N] <SIFT_HOME> <round.json> <base dir>")
		os.Exit(2)
	}
	home, fixturePath, base := flag.Arg(0), flag.Arg(1), flag.Arg(2)
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("SIFT_HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	id, err := Seed(context.Background(), store.Path(), data, base, *scale)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"round": id, "base": base})
}
