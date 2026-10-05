package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
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
// stored.
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
	if c := put(f.printOf("r-neg")); c != 200 {
		t.Errorf("accept with the current fingerprint: %d", c)
	}
}

// An edit to merge:C binds to C as the page showed it: C changed since gets
// 409 and nothing is stored, in a multi-row request too.
func TestAnOldPageCannotMergeIntoAChangedTarget(t *testing.T) {
	f := newFixture(t)
	old := f.printOf("r-size")
	if _, err := f.st.AddRows(context.Background(), f.round, []row.Row{{ID: "r-size", Verdict: "ask"}}); err != nil {
		t.Fatal(err)
	}
	put := func(target string) int {
		return f.do("PUT", "/api/decisions", fmt.Sprintf(`{"round":%d,"decisions":[{"id":"r-dead","action":"reject"},
			{"id":"r-neg","action":"edit","verdict":"merge:r-size","text":"both","target_fingerprint":%q}]}`, f.round, target)).Code
	}
	if c := put(old); c != 409 {
		t.Errorf("edit with the target's old fingerprint: %d", c)
	}
	if d, e := f.decisionOf("r-neg"), f.decisionOf("r-dead"); d != nil || e != nil {
		t.Fatalf("stored %+v %+v", d, e)
	}
	if c := put(f.printOf("r-size")); c != 200 {
		t.Errorf("edit with the target's current fingerprint: %d", c)
	}
}

// A row's clear answers the proposal the page showed: an old page's clear
// gets 409 and leaves the decision; a current one returns the row as it
// now is.
func TestAnOldPageCannotClearAChangedProposal(t *testing.T) {
	f := newFixture(t)
	old := f.printOf("r-neg")
	if _, err := f.st.AddRows(context.Background(), f.round, []row.Row{{ID: "r-neg", Verdict: "rewrite", Text: "- Push to main on Fridays."}}); err != nil {
		t.Fatal(err)
	}
	if w := f.decide("r-neg", "reject", ""); w.Code != 200 {
		t.Fatalf("reject: %d %s", w.Code, w.Body)
	}
	clear := func(print string) *httptest.ResponseRecorder {
		return f.do("DELETE", fmt.Sprintf("/api/decisions?round=%d&id=r-neg&fingerprint=%s", f.round, print), "")
	}
	if w := clear(old); w.Code != 409 || strings.Contains(w.Body.String(), "fingerprint") {
		t.Fatalf("a clear with the old fingerprint: %d %s", w.Code, w.Body)
	}
	if d := f.decisionOf("r-neg"); d == nil {
		t.Fatal("the old page's clear cleared it")
	}
	// The print alone is not enough: the clear names the decision it
	// clears, and another one is refused.
	if w := f.do("DELETE", fmt.Sprintf("/api/decisions?round=%d&id=r-neg&fingerprint=%s&decision_id=other", f.round, f.printOf("r-neg")), ""); w.Code != 409 {
		t.Fatalf("a clear of another decision: %d %s", w.Code, w.Body)
	}
	if w := clear(""); w.Code != 400 {
		t.Fatalf("a clear with no fingerprint: %d", w.Code)
	}
	w := clear(f.printOf("r-neg"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"fingerprint":"`+f.printOf("r-neg")+`"`) || strings.Contains(w.Body.String(), `"decision"`) {
		t.Fatalf("a current clear: %d %s", w.Code, w.Body)
	}
}
