// Package row is the one shape every sift finding takes, from an audit check
// or a memory intake: where it is, what found it, the facts behind it, what
// to do about it, and the answer.
package row

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Row is one finding.
type Row struct {
	// ID is stable: the check, the file and the normalized passage, hashed;
	// a repeat of the same passage in the file and check also hashes its
	// ordinal (see Nth).
	ID      string `json:"id"`
	Check   string `json:"check"`
	Summary string `json:"summary"`
	Source  Source `json:"source"`
	// Passage is the text the row is about, as it is now.
	Passage  string `json:"passage,omitempty"`
	Evidence []Fact `json:"evidence,omitempty"`
	// Verdict is the proposal (see ValidVerdict); empty until one is made.
	Verdict string `json:"verdict,omitempty"`
	// Title is the issue's title, for issue rows.
	Title string `json:"title,omitempty"`
	// Destination is a file and section, for move, rewrite and intake rows;
	// the repo, for issue rows.
	Destination string `json:"destination,omitempty"`
	// Text is the proposed text, written as guidance.
	Text string `json:"text,omitempty"`
	// Reason is why the agent proposes the verdict, shown with the proposal.
	Reason string `json:"reason,omitempty"`
	// Certain is true only for findings that cannot be wrong; sift applies
	// those itself.
	Certain bool `json:"certain"`
	// Fix is a certain row's own fix, the check's verdict (delete when it had
	// none), set when the round is recorded. Only this fix applies with no
	// decision: an agent's proposal that replaces it makes the row a
	// judgment (see FixOnly).
	Fix      string    `json:"fix,omitempty"`
	Decision *Decision `json:"decision,omitempty"`
	// Fingerprint is Print's hash of the proposal as the store holds it
	// now, filled when a round is read (never stored). The page sends it
	// back with each decision, and a decision on a changed row is refused.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Source is where a row's passage is.
type Source struct {
	// File is the file's path as found (for a repo file, under the repo root).
	File string `json:"file"`
	// Repo, Ref and Path place a repo file: the repo root, the ref it was
	// read at, and the repo-relative path.
	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"`
	Path string `json:"path,omitempty"`
	// Start and End are 1-based lines (0: the whole file).
	Start int `json:"start,omitempty"`
	End   int `json:"end,omitempty"`
	// Entry names a memory entry (intake rows).
	Entry string `json:"entry,omitempty"`
	// Canon is where File resolved (symlinks followed) when the row was
	// recorded, for a row read from disk rather than from git; "" when File
	// is not a clean absolute path. The file is read there, and only while
	// File still resolves to it.
	Canon string `json:"canon,omitempty"`
}

// FromDisk reports whether the row's file is read from disk: it is not a
// repo file read at a ref.
func (s Source) FromDisk() bool {
	return s.File != "" && (s.Repo == "" || s.Ref == "" || s.Path == "")
}

// Resolve is where file resolves now, for Canon: "" when file is not a
// clean absolute path ("..", ".", or relative) or does not resolve.
func Resolve(file string) string {
	if !filepath.IsAbs(file) || filepath.Clean(file) != file {
		return ""
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		return ""
	}
	return real
}

// Fact is one piece of evidence.
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Decision is the answer to a row's proposal from the review page: accept
// it, edit it (a changed verdict, title or text) or reject it.
type Decision struct {
	Action  string `json:"action"` // accept, edit or reject
	Verdict string `json:"verdict,omitempty"`
	Title   string `json:"title,omitempty"`
	Text    string `json:"text,omitempty"`
	// Cleared names the fields an edit empties ("title", "text"): an empty
	// Title or Text keeps the proposal's.
	Cleared []string `json:"cleared,omitempty"`
	Note    string   `json:"note,omitempty"`
	// Sent is set once the user pressed Send with this decision; the store
	// fills it, Validate ignores it.
	Sent bool `json:"sent,omitempty"`
}

// Change is what applying a row does: its verdict, title, destination and
// text once the decision's edits are over the proposal.
type Change struct {
	Verdict, Title, Destination, Text string
}

// MergeTarget is the row id a merge verdict names ("" for any other).
func MergeTarget(verdict string) string {
	id, _ := strings.CutPrefix(verdict, "merge:")
	if id == verdict {
		return ""
	}
	return id
}

// Print hashes every field that says what applying r does: the verdict,
// title, destination, text, the whole source, the passage, the certain fix,
// and, for a merge, its target's source and passage (target is that row;
// nil for any other verdict or a target not in the round). A decision
// answers one print: when it changes, the decision answered another row.
func (r Row) Print(target *Row) string {
	type about struct {
		Source  Source
		Passage string
	}
	v := struct {
		Verdict, Title, Destination, Text string
		Source                            Source
		Passage, Fix                      string
		Certain                           bool
		Target                            *about
	}{r.Verdict, r.Title, r.Destination, r.Text, r.Source, r.Passage, r.Fix, r.Certain, nil}
	if target != nil {
		v.Target = &about{target.Source, target.Passage}
	}
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// CertainFix is a certain row's own fix: Fix, or delete when unset.
func (r Row) CertainFix() string {
	if r.Fix != "" {
		return r.Fix
	}
	return "delete"
}

// FixOnly reports whether r is a certain row still carrying only its own
// fix: no proposal, or one that repeats the fix. Such a row is applied with
// no decision unless undone; any other row is a judgment.
func (r Row) FixOnly() bool {
	return r.Certain && (r.Verdict == "" || r.Verdict == r.CertainFix()) && r.Title == "" && r.Destination == "" && r.Text == ""
}

// Effective is the change the user approved. ok is false when there is none:
// the row is undecided, rejected, or accepted with no verdict. A certain row
// carrying only its own fix (FixOnly) needs no decision: the fix applies
// unless it was undone (a reject); a redo is an accept of the fix.
func (r Row) Effective() (Change, bool) {
	d := r.Decision
	if r.FixOnly() && (d == nil || d.Action != "edit") {
		if d != nil && d.Action == "reject" {
			return Change{}, false
		}
		return Change{Verdict: r.CertainFix()}, true
	}
	c := Change{Verdict: r.Verdict, Title: r.Title, Destination: r.Destination, Text: r.Text}
	switch {
	case d == nil, d.Action == "reject":
		return Change{}, false
	case d.Action == "edit":
		if d.Verdict != "" {
			c.Verdict = d.Verdict
		}
		if d.Title != "" {
			c.Title = d.Title
		}
		if d.Text != "" {
			c.Text = d.Text
		}
		for _, f := range d.Cleared {
			switch f {
			case "title":
				c.Title = ""
			case "text":
				c.Text = ""
			}
		}
	}
	if c.Verdict == "" {
		return Change{}, false
	}
	return c, true
}

// Validate reports what is wrong with a decision: an unknown action, an edit
// that changes nothing, an invalid verdict, a field cleared that can't be
// (or also set), or edits on an accept or reject.
func (d Decision) Validate() error {
	edited := d.Verdict != "" || d.Title != "" || d.Text != "" || len(d.Cleared) > 0
	switch d.Action {
	case "accept", "reject":
		if edited {
			return fmt.Errorf("decision: %s takes no verdict, title, text or cleared field (use edit)", d.Action)
		}
	case "edit":
		if !edited {
			return errors.New("decision: an edit changes the verdict, title or text")
		}
		seen := map[string]bool{}
		for _, f := range d.Cleared {
			switch {
			case f != "title" && f != "text":
				return fmt.Errorf("decision: %q can't be cleared (title or text can)", f)
			case seen[f]:
				return fmt.Errorf("decision: %s is cleared twice", f)
			case f == "title" && d.Title != "", f == "text" && d.Text != "":
				return fmt.Errorf("decision: %s is both set and cleared", f)
			}
			seen[f] = true
		}
		if d.Verdict != "" && !ValidVerdict(d.Verdict) {
			return fmt.Errorf("decision: %q is not a verdict", d.Verdict)
		}
	default:
		return fmt.Errorf("decision: action %q is not accept, edit or reject", d.Action)
	}
	return nil
}

// ID is a row's id: the first 16 hex digits of a hash of the check, the
// file and the passage with its whitespace collapsed.
func ID(file, check, passage string) string {
	h := sha256.Sum256([]byte(check + "\x00" + file + "\x00" + Normalize(passage)))
	return hex.EncodeToString(h[:8])
}

// Nth is the id of the n-th repeat (0-based, in line order) of a passage
// whose plain id is id: the first keeps id, so a passage gains no new id
// when a copy of it is added below.
func Nth(id string, n int) string {
	if n == 0 {
		return id
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", id, n)))
	return hex.EncodeToString(h[:8])
}

// Normalize collapses runs of whitespace to one space and trims the ends.
func Normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// plain verdicts take no argument; merge, drop and close take one after a
// colon. ask means the row needs the user's own decision before any verdict.
var plain = map[string]bool{
	"keep": true, "delete": true, "rewrite": true, "move": true,
	"issue": true, "global": true, "private": true, "ask": true,
}

// trackedRE is close:tracked's argument: the issue that already tracks it.
var trackedRE = regexp.MustCompile(`^tracked:[\w.-]+(?:/[\w.-]+)?#\d+$`)

// ValidVerdict reports whether v is a verdict: keep, delete, rewrite, move,
// merge:ID, drop:REASON, issue, global, private, ask, or close:REASON where
// REASON is done, obsolete or tracked:REPO#N.
func ValidVerdict(v string) bool {
	if plain[v] {
		return true
	}
	for _, p := range []string{"merge:", "drop:"} {
		if strings.HasPrefix(v, p) && strings.TrimSpace(v[len(p):]) != "" {
			return true
		}
	}
	if r, ok := strings.CutPrefix(v, "close:"); ok {
		return r == "done" || r == "obsolete" || trackedRE.MatchString(r)
	}
	return false
}
