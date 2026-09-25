package hooks

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/docket/config"
	"github.com/schuettc/tackle/internal/docket/journal"
	"github.com/schuettc/tackle/internal/docket/spool"
	"github.com/schuettc/tools-common/harness"
)

const maxStdinLines = 200

// Main is `docket hook <name> [args...]`, run by the shims. It never fails,
// prints nothing, runs no git and no network, and returns within a second.
// On a machine without a docket config it records nothing.
func Main(args []string, stdin io.Reader) int {
	if len(args) == 0 || os.Getenv("DOCKET_DISABLE") != "" || os.Getenv("DOCKET_INTERNAL") != "" {
		return 0
	}
	if _, err := os.Stat(config.Path()); err != nil {
		return 0 // not initialized here: nothing would ever drain the spool
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		Handle(args[0], args[1:], stdin, config.SpoolDir(), time.Now())
	}()
	select {
	case <-done:
	case <-time.After(900 * time.Millisecond):
	}
	return 0
}

// Handle turns one hook invocation into a spool line. Errors are dropped:
// losing one journal line is better than slowing or failing git.
func Handle(name string, args []string, stdin io.Reader, spoolDir string, now time.Time) {
	if name == "reference-transaction" && (len(args) == 0 || args[0] != "committed") {
		return
	}
	ev := journal.Event{V: 1, TS: now.UTC(), Src: "git-hook", Hook: name, Args: append([]string(nil), args...)}
	if name == "pre-push" && len(ev.Args) > 1 {
		ev.Args[1] = journal.RedactURL(ev.Args[1])
	}
	if stdinHooks[name] && stdin != nil {
		sc := bufio.NewScanner(io.LimitReader(stdin, 256*1024))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) == 0 {
				continue
			}
			if len(ev.Stdin) == maxStdinLines {
				ev.Truncated = true
				break
			}
			ev.Stdin = append(ev.Stdin, f)
		}
	}
	ev.CWD, _ = os.Getwd()
	if gd := os.Getenv("GIT_DIR"); gd != "" {
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(ev.CWD, gd)
		}
		ev.GitDir = filepath.Clean(gd)
	}
	id := harness.FromEnv()
	ev.ClaudeID, ev.AgentID, ev.Child = id.ClaudeID, id.AgentID, id.Child
	_ = spool.Append(spoolDir, ev)
}
