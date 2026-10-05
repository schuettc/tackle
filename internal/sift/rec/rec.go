// Package rec is the file recommendation: the unit the user decides in an
// audit round. The agent writes one revised version of each file that has
// findings, covering all of them, and says for each finding what the
// rewrite did. The user accepts, edits or rejects the whole file. Check is
// every rule a recommendation must meet before it is stored; Print is the
// fingerprint a decision answers.
package rec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/schuettc/tackle/internal/sift/row"
)

// File is one audited file as the round holds it: where it is, the hash of
// its content at the audit (Base), its size and its findings. The store
// keeps no content (a file may hold a secret); it is read back at Source
// (the commit, for a repo file) and checked against Base (see Read).
type File struct {
	// Key names the file within the round (a hash of Source.File).
	Key    string     `json:"key"`
	Source row.Source `json:"source"`
	// Commit is the commit Source.Ref named at the audit, for a repo file.
	Commit string `json:"commit,omitempty"`
	// Class is the budget class (global, repo, skill) and Budget its bytes.
	Class  string `json:"class"`
	Budget int    `json:"budget"`
	// Base is Hash of the content at the audit: a recommendation names it,
	// and apply holds a file whose base has moved on since.
	Base string `json:"base"`
	Size int    `json:"size"`
	// Content is the content at the audit, when it has been read; never
	// stored.
	Content string `json:"content,omitempty"`
	// Rows are the ids of the file's findings, in line order.
	Rows []string `json:"rows"`
}

// NewFile is a round file with its key and base filled in.
func NewFile(src row.Source, class string, budget int, content string) File {
	return File{Key: Key(src.File), Source: src, Class: class, Budget: budget, Base: Hash(content), Size: len(content), Content: content}
}

// Key is a file's key: the first 16 hex digits of a hash of its path.
func Key(file string) string {
	h := sha256.Sum256([]byte("file\x00" + file))
	return hex.EncodeToString(h[:8])
}

// Hash is a content's base hash (sha256, hex).
func Hash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// Account is what a recommendation did about one finding: fixed (How says
// how) or kept (How says why).
type Account struct {
	Row string `json:"row"`
	Did string `json:"did"`
	How string `json:"how"`
}

// Rec is one file's recommendation.
type Rec struct {
	// File is the file's key (sift next gives it; a path is resolved to it).
	File string `json:"file"`
	// Base is the hash of the content the rewrite started from.
	Base string `json:"base"`
	// Content is the whole recommended file.
	Content  string    `json:"content"`
	Findings []Account `json:"findings"`
	// Links are the keys of other files whose recommendations this one
	// moves text to or from; both sides name each other.
	Links   []string `json:"links,omitempty"`
	Summary string   `json:"summary"`
}

// Decision is the user's answer to a file's recommendation: accept it, edit
// it (Content is the whole file as the user wants it) or reject it.
type Decision struct {
	Action  string `json:"action"`
	Content string `json:"content,omitempty"`
	Note    string `json:"note,omitempty"`
	// Sent is set once the user pressed Send with this decision.
	Sent bool `json:"sent,omitempty"`
}

// Validate reports what is wrong with a decision.
func (d Decision) Validate() error {
	switch d.Action {
	case "accept", "reject":
		if d.Content != "" {
			return fmt.Errorf("decision: %s takes no content (use edit)", d.Action)
		}
	case "edit":
		if strings.TrimSpace(d.Content) == "" {
			return fmt.Errorf("decision: an edit carries the whole file as you want it")
		}
	default:
		return fmt.Errorf("decision: action %q is not accept, edit or reject", d.Action)
	}
	return nil
}

// Approved is the content a decision approves: the recommendation's on an
// accept, the user's on an edit; ok is false for a reject.
func Approved(r Rec, d Decision) (string, bool) {
	switch d.Action {
	case "accept":
		return r.Content, true
	case "edit":
		return d.Content, true
	}
	return "", false
}

// Check reports the first rule a batch of recommendations breaks, against
// the round's files and rows and the recommendations stored already
// (stored, by file key; the batch replaces its own files'):
//   - the file is in the round, once in the batch, and the base is its base;
//   - a summary and content;
//   - every finding of the file accounted for once, as fixed with how or
//     kept with why, and nothing else;
//   - every certain finding fixed;
//   - links point at other files in the round, and both sides name each
//     other (after the batch);
//   - the content differs from the base, unless every finding is kept.
func Check(files map[string]File, rows map[string]row.Row, stored map[string]Rec, batch []Rec) error {
	after := make(map[string]Rec, len(stored)+len(batch))
	for k, r := range stored {
		after[k] = r
	}
	seen := map[string]bool{}
	for _, r := range batch {
		if seen[r.File] {
			return fmt.Errorf("%s: recommended twice in one batch", r.File)
		}
		seen[r.File] = true
		after[r.File] = r
	}
	for _, r := range batch {
		f, ok := files[r.File]
		if !ok {
			return fmt.Errorf("%s: not in the round's files", r.File)
		}
		where := f.Source.File
		switch {
		case r.Base != f.Base:
			return fmt.Errorf("%s: base %s is not the file's base at the audit (%s): start from the content sift next gives", where, short(r.Base), short(f.Base))
		case strings.TrimSpace(r.Summary) == "":
			return fmt.Errorf("%s: no summary: say in two or three sentences what changed and why", where)
		case strings.TrimSpace(r.Content) == "":
			return fmt.Errorf("%s: no content: a recommendation is the whole file", where)
		}
		if err := accounts(f, rows, r); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		allKept := true
		for _, a := range r.Findings {
			allKept = allKept && a.Did == "kept"
		}
		if Hash(r.Content) == f.Base && !allKept {
			return fmt.Errorf("%s: the content is the same as the base, but some findings are fixed", where)
		}
		linked := map[string]bool{}
		for _, l := range r.Links {
			switch {
			case l == r.File:
				return fmt.Errorf("%s: links to itself", where)
			case linked[l]:
				return fmt.Errorf("%s: links to %s twice", where, l)
			}
			linked[l] = true
			if _, ok := files[l]; !ok {
				return fmt.Errorf("%s: links to %s, which is not in the round's files", where, l)
			}
			other, ok := after[l]
			if !ok || !slices.Contains(other.Links, r.File) {
				return fmt.Errorf("%s: links to %s, but %s's recommendation does not link back: linked files name each other (recommend both in one batch)", where, files[l].Source.File, files[l].Source.File)
			}
		}
		for k, other := range after {
			if k != r.File && slices.Contains(other.Links, r.File) && !linked[k] {
				return fmt.Errorf("%s: %s's recommendation links here, so this one must link to it: linked files name each other", where, files[k].Source.File)
			}
		}
	}
	return nil
}

func accounts(f File, rows map[string]row.Row, r Rec) error {
	want := map[string]bool{}
	for _, id := range f.Rows {
		want[id] = true
	}
	got := map[string]bool{}
	for _, a := range r.Findings {
		switch {
		case !want[a.Row]:
			return fmt.Errorf("finding %s is not one of this file's", a.Row)
		case got[a.Row]:
			return fmt.Errorf("finding %s is accounted for twice", a.Row)
		case a.Did == "fixed" && strings.TrimSpace(a.How) == "":
			return fmt.Errorf("finding %s is fixed with no how: say in a line what the rewrite did", a.Row)
		case a.Did == "kept" && strings.TrimSpace(a.How) == "":
			return fmt.Errorf("finding %s is kept with no why: say in a line why it stays", a.Row)
		case a.Did != "fixed" && a.Did != "kept":
			return fmt.Errorf("finding %s: %q is not fixed or kept", a.Row, a.Did)
		case a.Did == "kept" && rows[a.Row].Certain:
			return fmt.Errorf("finding %s is certain: the rewrite must fix it", a.Row)
		}
		got[a.Row] = true
	}
	for _, id := range f.Rows {
		if !got[id] {
			return fmt.Errorf("finding %s (%s) is not accounted for: fix it, or keep it and say why", id, rows[id].Check)
		}
	}
	return nil
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Print is the fingerprint a decision on r answers: its content, base,
// findings, summary and links, each linked recommendation's content and
// base, and the edit in force (edits, by file) on r's file and each linked
// one. The page shows an edit in place of the recommendation, and an accept
// keeps it, so a decision given against another print answered other
// content, and is refused.
func Print(r Rec, linked []Rec, edits map[string]string) string {
	type side struct{ File, Base, Content string }
	type edit struct{ File, Content string }
	others := make([]side, 0, len(linked))
	var shown []edit
	if c, ok := edits[r.File]; ok {
		shown = append(shown, edit{r.File, c})
	}
	for _, l := range linked {
		others = append(others, side{l.File, l.Base, l.Content})
		if c, ok := edits[l.File]; ok {
			shown = append(shown, edit{l.File, c})
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i].File < others[j].File })
	sort.Slice(shown, func(i, j int) bool { return shown[i].File < shown[j].File })
	b, _ := json.Marshal(struct {
		Rec    Rec
		Linked []side
		Edits  []edit `json:",omitempty"`
	}{r, others, shown})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// Group is the files decided together with key: those its recommendation's
// links reach, directly or through others, key included, sorted. A file
// with no recommendation is a group of one.
func Group(recs map[string]Rec, key string) []string {
	seen := map[string]bool{key: true}
	todo := []string{key}
	for len(todo) > 0 {
		k := todo[0]
		todo = todo[1:]
		for _, l := range recs[k].Links {
			if !seen[l] {
				seen[l] = true
				todo = append(todo, l)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
