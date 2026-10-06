// Package audit is one sift check from end to end: discover the files, run
// the checks, leave out muted rows and record the round. The check command
// and the agent channel's sift_check both run it.
package audit

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/schuettc/tackle/internal/sift/check"
	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/host"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Report is one audit: what was looked at and the round's rows.
type Report struct {
	Round  int64     `json:"round"`
	Kind   string    `json:"kind"`
	At     time.Time `json:"at"`
	Files  int       `json:"files"`
	Repos  int       `json:"repos"`
	Copies []Copy    `json:"copies"`
	// Skipped is what was left out as another project's: forks and
	// vendor-managed skills (config include turns them on).
	Skipped  []discover.Skip `json:"skipped"`
	Warnings []string        `json:"warnings"`
	Muted    int             `json:"muted"`
	Summary  map[string]int  `json:"summary"`
	Rows     []row.Row       `json:"rows"`
}

// Copy is an installed skill counted at its source.
type Copy struct {
	Path string `json:"path"`
	Of   string `json:"of"`
}

// Options says how to run.
type Options struct {
	Config config.Config
	// LookPath finds gh (nil: no host lookups).
	LookPath func(string) (string, error)
	Kind     string // what started the round; "" is on-demand
	// Warn gets a line when the round cannot be recorded (nil: discarded).
	Warn io.Writer
}

// Run audits and records the round. A store that cannot open or record is a
// warning: the rows are still returned (Round 0).
func Run(ctx context.Context, o Options) (Report, error) {
	if o.Kind == "" {
		o.Kind = "on-demand"
	}
	if o.Warn == nil {
		o.Warn = io.Discard
	}
	profiles, err := o.Config.Enabled()
	if err != nil {
		return Report{}, err
	}
	found, err := discover.Run(ctx, discover.Options{Profiles: profiles, Roots: o.Config.Roots, Include: o.Config.Include})
	if err != nil {
		return Report{}, err
	}
	var h host.Host
	if o.LookPath != nil {
		h = host.Detect(o.LookPath, host.Exec)
	}
	in := &check.Input{Files: found.Files, Chains: found.Chains, Repos: found.Repos, Config: o.Config, Host: h, Now: time.Now()}
	rows := check.Run(ctx, in)

	rep := Report{Kind: o.Kind, At: in.Now, Files: len(found.Files), Repos: len(found.Repos),
		Copies: []Copy{}, Skipped: append([]discover.Skip{}, found.Skipped...), Warnings: append([]string{}, found.Warnings...), Summary: map[string]int{}}
	for _, c := range found.Copies {
		rep.Copies = append(rep.Copies, Copy(c))
	}
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		_, _ = fmt.Fprintf(o.Warn, "sift: warning: the round is not recorded: %v\n", err)
	} else {
		defer func() { _ = s.Close() }()
		if rows, rep.Muted, err = s.Unmuted(ctx, rows); err != nil {
			return Report{}, err
		}
	}
	for _, r := range rows {
		rep.Summary[r.Check]++
	}
	if s != nil {
		files := Files(found.Files, rows, o.Config.Budgets)
		if rep.Round, err = s.RecordAudit(ctx, store.Round{Kind: rep.Kind, At: rep.At, Summary: rep.Summary}, rows, files); err != nil {
			_, _ = fmt.Fprintf(o.Warn, "sift: warning: the round is not recorded: %v\n", err)
		}
	}
	rep.Rows = append([]row.Row{}, rows...)
	return rep, nil
}

// Files is each audited file as the round records it, with its findings'
// ids in line order: the files with findings get a recommendation each,
// and any of them can receive text another file's recommendation moves.
func Files(found []*discover.File, rows []row.Row, b config.Budgets) []rec.File {
	ids := map[string][]string{}
	for _, r := range rows {
		ids[r.Source.File] = append(ids[r.Source.File], r.ID)
	}
	out := make([]rec.File, 0, len(found))
	for _, f := range found {
		src := row.Source{File: f.Path}
		commit := ""
		if f.Repo != nil {
			src.Repo, src.Ref, src.Path = f.Repo.Root, f.Repo.Ref, f.Rel
			commit = f.Repo.Commit
		} else {
			src.Canon = row.Resolve(f.Path)
		}
		budget := b.Repo
		switch f.Class {
		case discover.ClassGlobal:
			budget = b.Global
		case discover.ClassSkill:
			budget = b.Skill
		}
		rf := rec.NewFile(src, string(f.Class), budget, f.Content)
		rf.Commit = commit
		rf.Rows = append([]string{}, ids[f.Path]...)
		out = append(out, rf)
	}
	return out
}
