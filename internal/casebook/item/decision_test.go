package item

import (
	"strings"
	"testing"
	"time"
)

func TestDecisionEncodeDecodeRoundTrip(t *testing.T) {
	d := Decision{Disposition: Watch, Note: "retire when merged", Until: "merged(pr:a/b#97)", DecidedBy: "court",
		DecidedAt: time.Date(2026, 9, 24, 10, 12, 0, 500, time.FixedZone("x", -5*3600))}
	b, err := EncodeDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "conflict") {
		t.Errorf("empty conflict encoded:\n%s", b)
	}
	got, err := DecodeDecision(b)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DecidedAt.Equal(time.Date(2026, 9, 24, 15, 12, 0, 0, time.UTC)) {
		t.Errorf("decided_at %v, want UTC truncated to the second", got.DecidedAt)
	}
	if got.Disposition != d.Disposition || got.Note != d.Note || got.Until != d.Until || got.DecidedBy != d.DecidedBy || got.Conflict != nil {
		t.Errorf("round trip: %+v", got)
	}
}

func TestDecisionConflictRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	d := Decision{Disposition: Archive, DecidedBy: "a", DecidedAt: at, Conflict: &Conflict{Disposition: Keep, DecidedBy: "b", DecidedAt: at.Add(-time.Hour)}}
	b, _ := EncodeDecision(d)
	got, err := DecodeDecision(b)
	if err != nil || got.Conflict == nil || got.Conflict.Disposition != Keep || got.Conflict.DecidedBy != "b" {
		t.Fatalf("got %+v %v\n%s", got, err, b)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	_, err := DecodeDecision([]byte("disposition = \"keep\"\ndecided_by = \"court\"\ndecided_at = 2026-09-24T00:00:00Z\ncolour = \"red\"\n"))
	if err == nil || !strings.Contains(err.Error(), "colour") {
		t.Fatalf("got %v", err)
	}
}

func TestValidate(t *testing.T) {
	at := time.Now()
	check := func(k Kind, d Disposition, until string) error {
		return Decision{Disposition: d, Until: until, DecidedBy: "c", DecidedAt: at}.Validate(k)
	}
	type row struct {
		k Kind
		d Disposition
		u string
	}
	for _, g := range []row{{KindRepo, Archive, ""}, {KindPR, Merge, ""}, {KindIssue, Close, ""}, {KindBranch, Delete, ""},
		{KindWorktree, Keep, ""}, {KindPR, Watch, "merged(pr:a/b#1)"}, {KindRepo, Keep, "inactive(90d)"}} {
		if err := check(g.k, g.d, g.u); err != nil {
			t.Errorf("%s %s: %v", g.k, g.d, err)
		}
	}
	for _, b := range []row{{KindRepo, Merge, ""}, {KindIssue, Merge, ""}, {KindBranch, Archive, ""},
		{KindWorktree, Watch, "date(2026-01-01)"}, {KindPR, Wait, ""}, {KindPR, Watch, ""}, {KindRepo, Keep, "someday"}, {KindRepo, "shelve", ""}} {
		if err := check(b.k, b.d, b.u); err == nil {
			t.Errorf("%s %s %q accepted", b.k, b.d, b.u)
		}
	}
	if err := (Decision{Disposition: Keep, DecidedAt: at}).Validate(KindRepo); err == nil {
		t.Error("missing decided_by accepted")
	}
	if err := (Decision{Disposition: Keep, DecidedBy: "c"}).Validate(KindRepo); err == nil {
		t.Error("missing decided_at accepted")
	}
}
