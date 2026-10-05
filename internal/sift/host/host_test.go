package host

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var ctx = context.Background()

type fakeRun struct {
	calls []string
	out   map[string]string
	err   error
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.err != nil {
		return nil, f.err
	}
	out, ok := f.out[call]
	if !ok {
		return nil, errors.New("gh: Not Found (HTTP 404)")
	}
	return []byte(out), nil
}

func TestDetectWithoutGhIsNil(t *testing.T) {
	h := Detect(func(string) (string, error) { return "", errors.New("not found") }, nil)
	if h != nil {
		t.Fatalf("want no host, got %v", h)
	}
	h = Detect(func(string) (string, error) { return "/bin/gh", nil }, (&fakeRun{}).run)
	if h == nil {
		t.Fatal("gh on PATH, no host")
	}
}

func TestLookupReadsIssueAndPullState(t *testing.T) {
	f := &fakeRun{out: map[string]string{
		"gh api repos/o/r/issues/1": `{"state":"open"}`,
		"gh api repos/o/r/issues/2": `{"state":"closed"}`,
		"gh api repos/o/r/issues/3": `{"state":"open","pull_request":{"merged_at":null}}`,
		"gh api repos/o/r/issues/4": `{"state":"closed","pull_request":{"merged_at":"2026-01-02T00:00:00Z"}}`,
		"gh api repos/o/r/issues/5": `{"state":"closed","pull_request":{"merged_at":null}}`,
	}}
	h := Detect(func(string) (string, error) { return "/bin/gh", nil }, f.run)
	for n, want := range map[int]State{
		1: {Kind: "issue", State: "open"},
		2: {Kind: "issue", State: "closed"},
		3: {Kind: "pr", State: "open"},
		4: {Kind: "pr", State: "merged"},
		5: {Kind: "pr", State: "closed"},
	} {
		got, err := h.Lookup(ctx, Ref{Repo: "o/r", Number: n})
		if err != nil || got != want {
			t.Errorf("#%d: %+v %v, want %+v", n, got, err, want)
		}
	}
	// Answers are cached for the run.
	before := len(f.calls)
	_, _ = h.Lookup(ctx, Ref{Repo: "o/r", Number: 4})
	if len(f.calls) != before {
		t.Error("a repeated lookup called gh again")
	}
	if _, err := h.Lookup(ctx, Ref{Repo: "o/r", Number: 99}); err == nil {
		t.Error("a missing number is not an error")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:owner/name.git":       "owner/name",
		"https://github.com/owner/name":       "owner/name",
		"https://github.com/owner/name.git":   "owner/name",
		"ssh://git@github.com/owner/name.git": "owner/name",
	} {
		if got, ok := Slug(in); !ok || got != want {
			t.Errorf("Slug(%q) = %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "git@gitlab.com:owner/name.git", "/srv/git/name.git", "https://github.com/owner"} {
		if got, ok := Slug(in); ok {
			t.Errorf("Slug(%q) = %q, want none", in, got)
		}
	}
}

func TestRefs(t *testing.T) {
	line := "Waiting on #12 and other/repo#7; see https://github.com/a/b/pull/30 and https://github.com/a/b/issues/31 (not #x, nor a#b, nor &#39;)."
	got := Refs(line, "me/here")
	want := []Ref{{"me/here", 12}, {"other/repo", 7}, {"a/b", 30}, {"a/b", 31}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// Without a repo of its own, a bare #N names nothing.
	if got := Refs("see #12", ""); len(got) != 0 {
		t.Fatalf("bare ref without a repo: %+v", got)
	}
}

// A link's label is not a second reference: [#71](https://github.com/a/b/pull/71)
// names a/b#71 only.
func TestRefsLinkLabelIsNotABareRef(t *testing.T) {
	got := Refs("Upstream: [#71](https://github.com/a/b/pull/71), [#72](https://github.com/a/b/pull/72) and #9.", "me/here")
	want := []Ref{{"a/b", 71}, {"a/b", 72}, {"me/here", 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
