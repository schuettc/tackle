package observe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner runs gh. Implementations return stdout even when err != nil
// (gh api graphql exits 1 on partial results).
type Runner interface {
	Gh(ctx context.Context, args ...string) ([]byte, error)
}

// GhError is a failed gh invocation.
type GhError struct {
	Stderr string
	Code   int
}

// Error implements the error interface.
func (e *GhError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		msg = msg[:i]
	}
	return fmt.Sprintf("gh exit %d: %s", e.Code, msg)
}

// ExecRunner runs the real gh CLI (auth is gh's own; the docket never sees a token).
type ExecRunner struct{}

// Gh runs gh with args.
func (ExecRunner) Gh(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = append(os.Environ(), "DOCKET_INTERNAL=1", "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		if errb.Len() == 0 {
			errb.WriteString(err.Error())
		}
		return out.Bytes(), &GhError{Stderr: errb.String(), Code: code}
	}
	return out.Bytes(), nil
}
