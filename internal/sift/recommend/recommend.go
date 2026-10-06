// Package recommend is the agent's side of an audit round: Next hands it
// the next file to recommend (its content at the audit, its findings and
// the guidance for the rewrite) and Propose stores what it recommends. The
// sift next and sift propose commands and the channel's sift_next and
// sift_propose tools all go through here.
package recommend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/schuettc/tackle/internal/sift/content"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

// Guidance is how to write a file's recommendation. sift_next returns it
// with every file; it becomes the core of the sift skill.
const Guidance = `Recommend one revised version of this file: the whole file, covering all of its findings at once.

- Start from the content given here, and send its base hash back with the recommendation.
- Keep the file's own conventions: its sections and their order, its voice and its line wrapping. Where the file writes each paragraph on one line, write new paragraphs on one line too.
- Keep every rule's specifics: its conditions, exclusions, commands, examples and exceptions. A list of what to leave out or not to do is part of its rule. A rule keeps its own wording unless another finding needs that line changed.
- Merge duplicates into the one place that owns the rule, keeping what each copy adds.
- Remove stale status only once it is shown to be obsolete: the pull request it waits on has merged, or the date it waits for has passed. A record of finished work is history, not status: it stays.
- Remove the lines whose paths are gone.
- Bring the file within its budget. Keep here what every session that loads the file needs; move what only one repo or one task needs to the file that owns it.
- When text moves to another file, recommend both files in one batch, each naming the other in links, and show each side of the move in its file.
- Fix every certain finding. For each other finding, fix it and say how in one line, or keep it and say why in one line.
- End with a summary: two or three sentences on what changed and why.`

// ProposeHow is what to send back, for the output of next.
const ProposeHow = `sift propose (or sift_propose) with one JSON object per file: {"file": KEY, "base": BASE, "content": "the whole file", "findings": [{"row": ID, "did": "fixed" or "kept", "how": "one line"}], "links": [KEY, ...], "summary": "..."}; send linked files in one batch.`

// Finding is one of the file's findings, as next shows it.
type Finding struct {
	Row      string     `json:"row"`
	Check    string     `json:"check"`
	Certain  bool       `json:"certain"`
	Summary  string     `json:"summary"`
	Lines    string     `json:"lines,omitempty"` // "12" or "12-14"; "" for the whole file
	Passage  string     `json:"passage,omitempty"`
	Evidence []row.Fact `json:"evidence,omitempty"`
}

// Out is next's answer: the next file to recommend, or Done.
type Out struct {
	Round int64 `json:"round"`
	// Done is true when every file with findings has a recommendation: the
	// round is ready for the user.
	Done bool `json:"done"`
	// Left counts the files with findings still without a recommendation,
	// this one included.
	Left int `json:"left"`
	// The file: its key (what a recommendation names), where it is, the
	// commit it was read at, its budget, and its content at the audit with
	// that content's hash.
	File     string    `json:"file,omitempty"`
	Path     string    `json:"path,omitempty"`
	Repo     string    `json:"repo,omitempty"`
	Ref      string    `json:"ref,omitempty"`
	Commit   string    `json:"commit,omitempty"`
	Class    string    `json:"class,omitempty"`
	Budget   int       `json:"budget,omitempty"`
	Size     int       `json:"size,omitempty"`
	Base     string    `json:"base,omitempty"`
	Content  string    `json:"content,omitempty"`
	Findings []Finding `json:"findings,omitempty"`
	// Files lists the round's other files a recommendation may link to,
	// by key and path.
	Files    map[string]string `json:"files,omitempty"`
	Guidance string            `json:"guidance,omitempty"`
	Propose  string            `json:"propose,omitempty"`
}

// round resolves 0 to the latest round.
func round(ctx context.Context, s *store.Store, id int64) (int64, error) {
	if id != 0 {
		return id, nil
	}
	id, _, err := s.Latest(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("no round yet: run sift check")
	}
	return id, err
}

// Next is the next file of the round (0: the latest) to recommend.
func Next(ctx context.Context, s *store.Store, roundID int64) (Out, error) {
	id, err := round(ctx, s, roundID)
	if err != nil {
		return Out{}, err
	}
	rd, rows, err := s.Round(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Out{}, fmt.Errorf("no round %d", id)
	}
	if err != nil {
		return Out{}, err
	}
	if store.PerItem(rd.Kind) {
		return Out{}, fmt.Errorf("round %d is a %s round, decided per item: it takes rows (sift rows add), not file recommendations", id, rd.Kind)
	}
	items, err := s.Files(ctx, id)
	if err != nil {
		return Out{}, err
	}
	out := Out{Round: id, Done: true}
	var next *store.FileItem
	for i, it := range items {
		if len(it.Rows) > 0 && it.Rec == nil {
			out.Left++
			if next == nil {
				next = &items[i]
			}
		}
	}
	if next == nil {
		return out, nil
	}
	body, err := content.Read(ctx, next.File)
	if err != nil {
		return Out{}, err
	}
	byID := map[string]row.Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	out.Done = false
	out.File, out.Path, out.Repo, out.Ref, out.Commit = next.Key, next.Source.File, next.Source.Repo, next.Source.Ref, next.Commit
	out.Class, out.Budget, out.Size, out.Base, out.Content = next.Class, next.Budget, next.Size, next.Base, body
	for _, id := range next.Rows {
		r := byID[id]
		f := Finding{Row: id, Check: r.Check, Certain: r.Certain, Summary: r.Summary, Passage: r.Passage, Evidence: r.Evidence}
		switch {
		case r.Source.Start > 0 && r.Source.End > r.Source.Start:
			f.Lines = fmt.Sprintf("%d-%d", r.Source.Start, r.Source.End)
		case r.Source.Start > 0:
			f.Lines = fmt.Sprint(r.Source.Start)
		}
		out.Findings = append(out.Findings, f)
	}
	out.Files = map[string]string{}
	for _, it := range items {
		if it.Key != next.Key {
			out.Files[it.Key] = it.Source.File
		}
	}
	out.Guidance, out.Propose = Guidance, ProposeHow
	return out, nil
}

// Propose stores recommendations in the round (0: the latest), all or
// nothing (store.Propose says what is checked).
func Propose(ctx context.Context, s *store.Store, roundID int64, recs []rec.Rec) (store.ProposeResult, error) {
	id, err := round(ctx, s, roundID)
	if err != nil {
		return store.ProposeResult{}, err
	}
	if len(recs) == 0 {
		return store.ProposeResult{}, errors.New("no recommendation given")
	}
	return s.Propose(ctx, id, recs)
}
