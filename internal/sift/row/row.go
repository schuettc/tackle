// Package row is the one shape every sift finding takes, from an audit check
// or a memory intake: where it is, what found it, the facts behind it, what
// to do about it, and the answer.
package row

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Row is one finding.
type Row struct {
	// ID is stable: the check, the file and the normalized passage, hashed.
	ID      string `json:"id"`
	Check   string `json:"check"`
	Summary string `json:"summary"`
	Source  Source `json:"source"`
	// Passage is the text the row is about, as it is now.
	Passage  string `json:"passage,omitempty"`
	Evidence []Fact `json:"evidence,omitempty"`
	// Verdict is the proposal (see ValidVerdict); empty until one is made.
	Verdict string `json:"verdict,omitempty"`
	// Destination is a file and section, for move, rewrite and intake rows.
	Destination string `json:"destination,omitempty"`
	// Text is the proposed text, written as guidance.
	Text string `json:"text,omitempty"`
	// Certain is true only for findings that cannot be wrong; sift applies
	// those itself.
	Certain  bool      `json:"certain"`
	Decision *Decision `json:"decision,omitempty"`
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
}

// Fact is one piece of evidence.
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Decision is the answer from the review page.
type Decision struct {
	Value string `json:"value"`
	Note  string `json:"note,omitempty"`
}

// ID is a row's id: the first 16 hex digits of a hash of the check, the
// file and the passage with its whitespace collapsed.
func ID(file, check, passage string) string {
	h := sha256.Sum256([]byte(check + "\x00" + file + "\x00" + Normalize(passage)))
	return hex.EncodeToString(h[:8])
}

// Normalize collapses runs of whitespace to one space and trims the ends.
func Normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// plain verdicts take no argument; merge and drop take one after a colon.
var plain = map[string]bool{
	"keep": true, "delete": true, "rewrite": true, "move": true,
	"issue": true, "global": true, "private": true,
}

// ValidVerdict reports whether v is a verdict: keep, delete, rewrite, move,
// merge:ID, drop:REASON, issue, global or private.
func ValidVerdict(v string) bool {
	if plain[v] {
		return true
	}
	for _, p := range []string{"merge:", "drop:"} {
		if strings.HasPrefix(v, p) && strings.TrimSpace(v[len(p):]) != "" {
			return true
		}
	}
	return false
}
