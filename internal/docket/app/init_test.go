package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/docket/observe"
)

// scriptGh is a scripted fake Runner keyed by the first 3 args joined with spaces.
type scriptGh struct {
	answers map[string]struct {
		out string
		err error
	}
	calls []string
}

func (s *scriptGh) Gh(_ context.Context, args ...string) ([]byte, error) {
	full := strings.Join(args, " ")
	s.calls = append(s.calls, full)
	key := strings.Join(args[:min(3, len(args))], " ")
	if ans, ok := s.answers[key]; ok {
		return []byte(ans.out), ans.err
	}
	return nil, fmt.Errorf("scriptGh: no answer for %q", full)
}

func hasCall(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestEnsurePrivateRepoExistingPrivate(t *testing.T) {
	sg := &scriptGh{answers: map[string]struct {
		out string
		err error
	}{
		"api repos/schuettc/docket-data --jq": {out: "true\n"},
	}}
	created, err := ensurePrivateRepo(ctx, sg, "schuettc/docket-data", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created {
		t.Error("expected created=false for existing private repo")
	}
	if hasCall(sg.calls, "repo create") {
		t.Error("expected no repo create call for existing private repo")
	}
}

func TestEnsurePrivateRepoRefusesPublic(t *testing.T) {
	sg := &scriptGh{answers: map[string]struct {
		out string
		err error
	}{
		"api repos/schuettc/docket-data --jq": {out: "false\n"},
	}}
	_, err := ensurePrivateRepo(ctx, sg, "schuettc/docket-data", true)
	if err == nil || !strings.Contains(err.Error(), "is public") {
		t.Fatalf("expected 'is public' error, got: %v", err)
	}
	if hasCall(sg.calls, "repo create") {
		t.Error("expected no repo create call for public repo refusal")
	}
}

func TestEnsurePrivateRepoCreatesMissing(t *testing.T) {
	sg := &scriptGh{answers: map[string]struct {
		out string
		err error
	}{
		"api repos/schuettc/docket-data --jq": {err: &observe.GhError{Code: 1, Stderr: "gh: Not Found (HTTP 404)"}},
		"repo create schuettc/docket-data":    {out: ""},
	}}
	created, err := ensurePrivateRepo(ctx, sg, "schuettc/docket-data", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected created=true for missing repo")
	}
	wantCreate := "repo create schuettc/docket-data --private --description docket data (written by the docket binary)"
	var foundCreate string
	for _, c := range sg.calls {
		if strings.HasPrefix(c, "repo create") {
			foundCreate = c
			break
		}
	}
	if foundCreate != wantCreate {
		t.Errorf("create call: got %q, want %q", foundCreate, wantCreate)
	}
}

func TestEnsurePrivateRepoNoCreate(t *testing.T) {
	sg := &scriptGh{answers: map[string]struct {
		out string
		err error
	}{
		"api repos/schuettc/docket-data --jq": {err: &observe.GhError{Code: 1, Stderr: "gh: Not Found (HTTP 404)"}},
	}}
	_, err := ensurePrivateRepo(ctx, sg, "schuettc/docket-data", false)
	if err == nil || !strings.Contains(err.Error(), "--no-create") {
		t.Fatalf("expected '--no-create' error, got: %v", err)
	}
	if hasCall(sg.calls, "repo create") {
		t.Error("expected no repo create call with create=false")
	}
}

func TestEnsurePrivateRepoOtherError(t *testing.T) {
	sg := &scriptGh{answers: map[string]struct {
		out string
		err error
	}{
		"api repos/schuettc/docket-data --jq": {err: &observe.GhError{Code: 1, Stderr: "gh: HTTP 502"}},
	}}
	_, err := ensurePrivateRepo(ctx, sg, "schuettc/docket-data", true)
	if err == nil || !strings.Contains(err.Error(), "checking schuettc/docket-data") {
		t.Fatalf("expected 'checking schuettc/docket-data' error, got: %v", err)
	}
}

func TestDefaultRemote(t *testing.T) {
	got := defaultRemote("schuettc")
	want := "git@github.com:schuettc/docket-data.git"
	if got != want {
		t.Errorf("defaultRemote(%q) = %q, want %q", "schuettc", got, want)
	}
}
