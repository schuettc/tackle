// Package gitx runs git for the ledger. Every call is marked LEDGER_INTERNAL=1
// so the ledger's own git activity is never journaled by its hooks.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Error is a failed git command.
type Error struct {
	Args   []string
	Stderr string
	Code   int
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.Code, strings.TrimSpace(e.Stderr))
}

// Run runs git in dir and returns its trimmed stdout.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LEDGER_INTERNAL=1", "GIT_EDITOR=true", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return strings.TrimRight(out.String(), "\n"), &Error{Args: args, Stderr: errb.String(), Code: code}
	}
	return strings.TrimRight(out.String(), "\n"), nil
}
