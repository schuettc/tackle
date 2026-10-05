// Command seed is a dev-only helper for the review page's probe: it writes a
// fixture's files under a base directory (each named repo as a git repo with
// one commit, the rest as plain files), opens the sift store under a
// SIFT_HOME and records ONE round of the fixture's rows and files, their
// sources pointing at those files. An audit round's recommendations go in
// through the store's own Propose, so the fixture meets every rule an
// agent's does. It is not part of the shipped binary.
//
//	go run ./internal/sift/web/seed [-scale N] [-recommending] <SIFT_HOME> <round.json> <base dir>
//
// -scale N pads the round to N rows with stale-status findings spread over
// generated files, as a large real round would be, each file recommended.
// -recommending leaves one file without a recommendation, so the round is
// still being recommended.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/rec"
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

// fixtureRec is a file's recommendation in the fixture: every finding is
// fixed with How[id], else Fixed, unless Kept names it (with why).
type fixtureRec struct {
	Summary string            `json:"summary"`
	Content string            `json:"content"`
	Links   []string          `json:"links"`
	Fixed   string            `json:"fixed"`
	How     map[string]string `json:"how"`
	Kept    map[string]string `json:"kept"`
}

type fixture struct {
	Kind  string                `json:"kind"`
	Repos []string              `json:"repos"`
	Files map[string]string     `json:"files"`
	Rows  []fixtureRow          `json:"rows"`
	Recs  map[string]fixtureRec `json:"recs"`
}

// Options says how to seed.
type Options struct {
	Scale        int  // pad the round to this many rows
	Recommending bool // leave one file without a recommendation
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := discover.Git(ctx, dir, args...)
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=seed", "GIT_AUTHOR_EMAIL=seed@example.invalid",
		"GIT_COMMITTER_NAME=seed", "GIT_COMMITTER_EMAIL=seed@example.invalid", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// Seed writes the fixture under base and records its round in the store at
// dbPath; it returns the round's id.
func Seed(ctx context.Context, dbPath string, data []byte, base string, o Options) (int64, error) {
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		return 0, fmt.Errorf("fixture: %w", err)
	}
	if o.Scale > len(fx.Rows) {
		pad(&fx, o.Scale)
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
	commits := map[string]string{}
	for _, r := range fx.Repos {
		dir := filepath.Join(base, r)
		for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
			if _, err := git(ctx, dir, args...); err != nil {
				return 0, err
			}
		}
		c, err := git(ctx, dir, "rev-parse", "HEAD")
		if err != nil {
			return 0, err
		}
		commits[r] = c
	}
	source := func(rel string) (row.Source, string) {
		src := row.Source{File: filepath.Join(base, filepath.FromSlash(rel))}
		for _, repo := range fx.Repos {
			if p, ok := strings.CutPrefix(rel, repo+"/"); ok {
				src.Repo, src.Ref, src.Path = filepath.Join(base, repo), "HEAD", p
				return src, commits[repo]
			}
		}
		src.Canon = row.Resolve(src.File)
		return src, ""
	}
	rows := make([]row.Row, 0, len(fx.Rows))
	byRel := map[string][]string{}
	for _, fr := range fx.Rows {
		r := fr.Row
		src, _ := source(fr.Source.Rel)
		src.Canon, src.Start, src.End = "", fr.Source.Start, fr.Source.End
		r.Source = src
		if !store.PerItem(fx.Kind) {
			// An audit round's findings carry no proposal: the file's
			// recommendation answers them.
			r.Verdict, r.Title, r.Destination, r.Text, r.Reason = "", "", "", "", ""
		}
		rows = append(rows, r)
		byRel[fr.Source.Rel] = append(byRel[fr.Source.Rel], r.ID)
	}
	var files []rec.File
	if !store.PerItem(fx.Kind) {
		for _, rel := range names {
			src, commit := source(rel)
			class, budget := "repo", 6000
			switch {
			case path.Base(rel) == "SKILL.md":
				class, budget = "skill", 10000
			case src.Repo == "":
				class, budget = "global", 8000
			}
			f := rec.NewFile(src, class, budget, fx.Files[rel])
			f.Commit, f.Rows = commit, append([]string{}, byRel[rel]...)
			files = append(files, f)
		}
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close() }()
	sum := map[string]int{}
	for _, r := range rows {
		sum[r.Check]++
	}
	id, err := st.RecordAudit(ctx, store.Round{Kind: fx.Kind, Summary: sum}, rows, files)
	if err != nil || store.PerItem(fx.Kind) {
		return id, err
	}
	var batch []rec.Rec
	skipped := false
	for _, f := range files {
		rel := relOf(base, f.Source.File)
		fr, ok := fx.Recs[rel]
		if !ok {
			continue
		}
		if o.Recommending && !skipped && len(fr.Links) == 0 {
			skipped = true
			continue
		}
		r := rec.Rec{File: f.Source.File, Base: f.Base, Content: fr.Content, Summary: fr.Summary}
		for _, l := range fr.Links {
			r.Links = append(r.Links, filepath.Join(base, filepath.FromSlash(l)))
		}
		for _, id := range f.Rows {
			a := rec.Account{Row: id, Did: "fixed", How: fr.Fixed}
			if how := fr.How[id]; how != "" {
				a.How = how
			}
			if why, ok := fr.Kept[id]; ok {
				a.Did, a.How = "kept", why
			}
			r.Findings = append(r.Findings, a)
		}
		batch = append(batch, r)
	}
	if _, err := st.Propose(ctx, id, batch); err != nil {
		return 0, fmt.Errorf("the fixture's recommendations: %w", err)
	}
	return id, nil
}

func relOf(base, file string) string {
	rel, _ := filepath.Rel(base, file)
	return filepath.ToSlash(rel)
}

// pad adds generated files to the fixture's first repo and stale-status
// rows in them until it has n rows: forty files of varying length, each
// with a recommendation that drops those lines.
func pad(fx *fixture, n int) {
	repo := fx.Repos[0]
	for i := 0; len(fx.Rows) < n; i++ {
		rel := fmt.Sprintf("%s/pkg%02d/AGENTS.md", repo, i%40)
		lines := strings.Split(strings.TrimSuffix(fx.Files[rel], "\n"), "\n")
		if fx.Files[rel] == "" {
			lines = []string{fmt.Sprintf("# pkg%02d", i%40), ""}
		}
		text := fmt.Sprintf("- Waiting on the helper %d rewrite before request handlers call it.", i)
		lines = append(lines, text)
		fx.Files[rel] = strings.Join(lines, "\n") + "\n"
		var fr fixtureRow
		fr.Row = row.Row{ID: row.ID(rel, "stale-status", fmt.Sprint(text, i)), Check: "stale-status",
			Summary: "status that may be stale", Passage: text, Evidence: []row.Fact{{Name: "matched", Value: "Waiting on"}}}
		fr.Source.Rel, fr.Source.Start, fr.Source.End = rel, len(lines), len(lines)
		fx.Rows = append(fx.Rows, fr)
		if fx.Recs == nil {
			fx.Recs = map[string]fixtureRec{}
		}
		body := strings.NewReplacer("- Waiting on the helper", "- The helper", "rewrite before request handlers call it.", "is called from a background job: it blocks on the disk.").
			Replace(fx.Files[rel])
		fx.Recs[rel] = fixtureRec{Summary: "Each helper line says where it is called now that the rewrite is done.", Content: body, Fixed: "the rewrite is done: says where it is called"}
	}
}

func main() {
	scale := flag.Int("scale", 0, "pad the round to this many rows")
	recommending := flag.Bool("recommending", false, "leave one file without a recommendation")
	flag.Parse()
	if flag.NArg() != 3 {
		fmt.Fprintln(os.Stderr, "usage: seed [-scale N] [-recommending] <SIFT_HOME> <round.json> <base dir>")
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
	id, err := Seed(context.Background(), store.Path(), data, base, Options{Scale: *scale, Recommending: *recommending})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"round": id, "base": base})
}
