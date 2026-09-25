package gitx

import (
	"context"
	"errors"
	"testing"

	"github.com/schuettc/tackle/internal/docket/testgit"
)

func TestRunMarksInternalAndReportsErrors(t *testing.T) {
	testgit.Env(t)
	dir := testgit.NewRepo(t)
	out, err := Run(context.Background(), dir, "var", "GIT_EDITOR")
	if err != nil || out != "true" {
		t.Fatalf("GIT_EDITOR = %q, %v", out, err)
	}
	_, err = Run(context.Background(), dir, "rev-parse", "--verify", "nope")
	var ge *Error
	if !errors.As(err, &ge) || ge.Code == 0 || ge.Stderr == "" {
		t.Fatalf("got %#v", err)
	}
}
