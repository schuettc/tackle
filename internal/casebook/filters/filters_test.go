package filters

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/tackle/internal/casebook/engine"
	"github.com/subaud-tools/tackle/internal/casebook/item"
	"github.com/subaud-tools/tackle/internal/casebook/rules"
)

const specTOML = `id = "outgoing-prs"
name = "Outgoing PRs"

[[match]]
field = "kind"
op = "is"
value = "pr"

[[match]]
field = "relation"
op = "is"
value = "outgoing"
`

func outgoingPRs() Filter {
	return Filter{ID: "outgoing-prs", Name: "Outgoing PRs", Match: []rules.Condition{
		{Field: "kind", Op: "is", Value: "pr"},
		{Field: "relation", Op: "is", Value: "outgoing"},
	}}
}

func TestDecodeEncodeRoundTrip(t *testing.T) {
	f, err := Decode([]byte(specTOML))
	if err != nil {
		t.Fatal(err)
	}
	if want := outgoingPRs(); !reflect.DeepEqual(f, want) {
		t.Fatalf("decoded %+v, want %+v", f, want)
	}
	b, err := Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "# casebook saved filter. Format: internal/casebook/FORMAT.md in subaud-tools/tackle.\n") {
		t.Errorf("header missing: %q", b)
	}
	back, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, f) {
		t.Fatalf("round trip %+v, want %+v", back, f)
	}
}

func TestDecodeRejectsUnknownKey(t *testing.T) {
	_, err := Decode([]byte(`status = "draft"` + "\n" + specTOML))
	if err == nil || !strings.Contains(err.Error(), `unknown key "status"`) {
		t.Fatalf("err %v, want unknown key \"status\"", err)
	}
}

func TestValidate(t *testing.T) {
	ok := outgoingPRs()
	badID := ok
	badID.ID = "Outgoing PRs"
	noName := ok
	noName.Name = ""
	noMatch := ok
	noMatch.Match = nil
	badField := ok
	badField.Match = []rules.Condition{{Field: "colour", Op: "is", Value: "red"}}
	for _, tc := range []struct {
		name string
		f    Filter
		want string
	}{
		{"id", badID, `filter id "Outgoing PRs" must match ^[a-z0-9-]+$`},
		{"name", noName, "a filter needs a name"},
		{"match", noMatch, "a filter needs at least one condition"},
		{"field", badField, `condition 1: unknown field "colour"`},
		{"ok", ok, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.f.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExamples(t *testing.T) {
	c := func(f, o, v string) rules.Condition { return rules.Condition{Field: f, Op: o, Value: v} }
	want := []Filter{
		{ID: "outgoing-prs", Name: "Outgoing PRs", Match: []rules.Condition{c("kind", "is", "pr"), c("relation", "is", "outgoing")}},
		{ID: "incoming", Name: "Incoming", Match: []rules.Condition{c("relation", "is", "incoming")}},
		{ID: "quiet-14d", Name: "Not updated in 14 days", Match: []rules.Condition{c("updated", "older-than", "14d")}},
		{ID: "unpushed", Name: "Unpushed commits", Match: []rules.Condition{c("unpushed", "is", "true")}},
		{ID: "dormant-repos", Name: "Repos not pushed in a year", Match: []rules.Condition{c("kind", "is", "repo"), c("updated", "older-than", "365d")}},
	}
	got := Examples()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("examples %+v, want %+v", got, want)
	}
	for _, f := range got {
		if err := f.Validate(); err != nil {
			t.Errorf("%s: %v", f.ID, err)
		}
	}
}

func TestKeysIsTheMatch(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	pr := item.PRKey("a/b", 1)
	is := item.IssueKey("a/b", 2)
	br := item.BranchKey("a/b", "feat")
	res := engine.Result{Items: []engine.Item{
		{Key: pr, ID: pr.String(), Kind: pr.Kind},
		{Key: is, ID: is.String(), Kind: is.Kind},
		{Key: br, ID: br.String(), Kind: br.Kind},
	}}
	f := Filter{ID: "prs", Name: "PRs", Match: []rules.Condition{{Field: "kind", Op: "is", Value: "pr"}}}
	got, err := f.Keys(res, now)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(got))
	for k, v := range got {
		if v {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{pr.String()}) {
		t.Fatalf("keys %v, want [%s]", keys, pr)
	}
}
