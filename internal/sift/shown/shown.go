// Package shown is the one rule a decision on a file or a row follows: the
// review page shows the edit in force when there is one, otherwise the
// agent's proposal (a file's recommendation, a row's verdict and text), and
// a decision answers the print of exactly that. An accept of an edited item
// approves the edit it showed; going back to the proposal is a clear,
// after which the page shows the proposal again. rec (files) and row (rows)
// both hash through Print, and the store's decisions on both go through
// Kept, so the two can't drift apart.
package shown

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Print is the fingerprint of v, what the page shows of an item: the first
// 8 bytes of a SHA-256 of its JSON, in hex.
func Print(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// Decision is a decision on a file (rec.Decision) or a row (row.Decision).
type Decision[D any] interface {
	// Act is accept, edit or reject.
	Act() string
	// Noted is the decision with note in place of its own.
	Noted(note string) D
}

// Kept is the decision d stores over cur, the one in force (nil: none). An
// accept of an edited item keeps the edit, which is what the page showed,
// with the accept's note; any other decision is stored as given.
func Kept[D Decision[D]](d D, note string, cur *D) D {
	if d.Act() == "accept" && cur != nil && (*cur).Act() == "edit" {
		return (*cur).Noted(note)
	}
	return d
}
