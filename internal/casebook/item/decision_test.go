package item

import (
	"encoding/json"
	"regexp"
	"slices"
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

func TestDecisionFormat2FieldsRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	d := Decision{Disposition: Close, DecidedBy: "court", DecidedAt: at, ProposedBy: "pi:s-1", Rule: "stale-bots"}
	b, err := EncodeDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `proposed_by = "pi:s-1"`) || !strings.Contains(string(b), `rule = "stale-bots"`) {
		t.Fatalf("encoded:\n%s", b)
	}
	got, err := DecodeDecision(b)
	if err != nil || got.ProposedBy != "pi:s-1" || got.Rule != "stale-bots" {
		t.Fatalf("got %+v %v", got, err)
	}
	plain, _ := EncodeDecision(Decision{Disposition: Keep, DecidedBy: "court", DecidedAt: at})
	if strings.Contains(string(plain), "proposed_by") || strings.Contains(string(plain), "rule") {
		t.Errorf("empty format-2 fields encoded:\n%s", plain)
	}
}

func TestDecisionJSONIsSnakeCase(t *testing.T) {
	b, _ := json.Marshal(Decision{Disposition: Keep, DecidedBy: "court", ProposedBy: "rule:x"})
	for _, want := range []string{`"disposition":"keep"`, `"decided_by":"court"`, `"proposed_by":"rule:x"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("json %s lacks %s", b, want)
		}
	}
}

// TestVocabularyNotNowForms: every Not now form, once its hole is filled
// with a sample of what it asks for, is an until condition ParseUntil reads.
func TestVocabularyNotNowForms(t *testing.T) {
	samples := map[string]string{
		"days":        "2026-10-13",
		"date":        "2026-12-01",
		"pr":          "pr:schuettc/tackle#5",
		"pr-or-issue": "issue:schuettc/tackle#6",
		"repo":        "repo:schuettc/tackle",
	}
	ids := []string{"in-1w", "in-1m", "on-date", "pr-merges", "closes", "quiet-90", "release"}
	forms := NotNowForms()
	if len(forms) != len(ids) {
		t.Fatalf("not now forms %+v, want %v", forms, ids)
	}
	for i, f := range forms {
		if f.ID != ids[i] {
			t.Errorf("form %d id %q, want %q", i, f.ID, ids[i])
		}
		holes := strings.Count(f.Template, "%s")
		if f.Asks == "" {
			if holes != 0 {
				t.Errorf("%s is fixed but has a hole: %q", f.ID, f.Template)
			}
		} else if holes != 1 {
			t.Errorf("%s asks %q but has %d holes: %q", f.ID, f.Asks, holes, f.Template)
		}
		until := f.Fill(samples[f.Asks])
		if _, err := ParseUntil(until); err != nil {
			t.Errorf("%s: %q: %v", f.ID, until, err)
		}
		if f.Asks == "days" && f.Days <= 0 {
			t.Errorf("%s asks days but has none", f.ID)
		}
	}
}

// TestWordingNamesNoOne: product text names no person, provider or model,
// and has no em dash.
func TestWordingNamesNoOne(t *testing.T) {
	re := regexp.MustCompile(`(?i)\b(court|claude|codex|openai|anthropic|gpt|gemini|opus|sonnet)\b`)
	var texts []string
	for _, k := range []Kind{KindRepo, KindPR, KindIssue, KindBranch, KindWorktree} {
		texts = append(texts, Question(k))
		for _, c := range Choices(k) {
			texts = append(texts, c.Label, c.Says)
		}
	}
	for _, f := range NotNowForms() {
		texts = append(texts, f.Label)
	}
	for _, s := range texts {
		if s == "" {
			t.Error("empty wording")
		}
		if re.MatchString(s) {
			t.Errorf("%q names someone", s)
		}
		if strings.Contains(s, "\u2014") {
			t.Errorf("%q has an em dash", s)
		}
	}
}

// TestChoicesAreAllowed: every offered choice is a disposition the kind
// allows, wait is offered and watch is not.
func TestChoicesAreAllowed(t *testing.T) {
	for _, k := range []Kind{KindRepo, KindPR, KindIssue, KindBranch, KindWorktree} {
		var hasWait bool
		for _, c := range Choices(k) {
			if !slices.Contains(Allowed(k), c.Disposition) {
				t.Errorf("%s offers %s, not allowed", k, c.Disposition)
			}
			if c.Disposition == Watch {
				t.Errorf("%s offers watch", k)
			}
			hasWait = hasWait || c.Disposition == Wait
		}
		if !hasWait {
			t.Errorf("%s doesn't offer wait", k)
		}
	}
}
