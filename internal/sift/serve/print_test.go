package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
)

// printOf is the fingerprint the page shows for a row.
func (f *fixture) printOf(id string) string {
	f.t.Helper()
	var m struct {
		Rows []struct {
			ID          string `json:"id"`
			Fingerprint string `json:"fingerprint"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(f.do("GET", "/api/review", "").Body.Bytes(), &m); err != nil {
		f.t.Fatal(err)
	}
	for _, r := range m.Rows {
		if r.ID == id {
			return r.Fingerprint
		}
	}
	f.t.Fatalf("no row %s", id)
	return ""
}

func (f *fixture) decisionOf(id string) *row.Decision {
	f.t.Helper()
	for _, r := range f.review().Rows {
		if r.ID == id {
			return r.Decision
		}
	}
	return nil
}

// An approval binds to the proposal the page showed: an old page accepting
// after a same-round `rows add` changed the proposal gets 409, with nothing
// stored. Undo and redo carry the fingerprint too.
func TestAnOldPageCannotAcceptAChangedProposal(t *testing.T) {
	f := newFixture(t)
	old := f.printOf("r-neg")
	if _, err := f.st.AddRows(context.Background(), f.round, []row.Row{{ID: "r-neg", Verdict: "rewrite", Text: "- Push to main on Fridays."}}); err != nil {
		t.Fatal(err)
	}
	put := func(print string) int {
		return f.do("PUT", "/api/decisions", fmt.Sprintf(`{"round":%d,"decisions":[{"id":"r-neg","action":"accept","fingerprint":%q}]}`, f.round, print)).Code
	}
	if c := put(old); c != 409 {
		t.Errorf("accept with the old fingerprint: %d", c)
	}
	if d := f.decisionOf("r-neg"); d != nil {
		t.Fatalf("stored %+v", d)
	}
	if c := put(""); c != 400 {
		t.Errorf("accept with no fingerprint: %d", c)
	}
	if c := put(f.printOf("r-neg")); c != 204 {
		t.Errorf("accept with the current fingerprint: %d", c)
	}
	fix := func(op, print string) int {
		return f.do("POST", "/api/"+op, fmt.Sprintf(`{"round":%d,"id":"r-dead","fingerprint":%q}`, f.round, print)).Code
	}
	if c := fix("undo", "0000"); c != 409 {
		t.Errorf("undo with another fingerprint: %d", c)
	}
	if c := fix("undo", ""); c != 400 {
		t.Errorf("undo with no fingerprint: %d", c)
	}
	if d := f.decisionOf("r-dead"); d != nil {
		t.Fatalf("stored %+v", d)
	}
	if c := fix("undo", f.printOf("r-dead")); c != 204 {
		t.Errorf("undo: %d", c)
	}
	if c := fix("redo", "0000"); c != 409 {
		t.Errorf("redo with another fingerprint: %d", c)
	}
}
