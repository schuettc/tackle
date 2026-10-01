package hooks

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/journal"
	"github.com/schuettc/tackle/internal/casebook/spool"
	"github.com/schuettc/tackle/internal/casebook/temppath"
	"github.com/schuettc/tools-common/harness"
)

const maxStdinLines = 200

// Main is `casebook hook <name> [args...]`, run by the shims. It never fails,
// prints nothing, runs no git and no network, and returns within a second.
// On a machine without a casebook config it records nothing, and git activity
// in a temp folder (temppath) is never recorded.
func Main(args []string, stdin io.Reader) int {
	if len(args) == 0 || os.Getenv("CASEBOOK_DISABLE") != "" || os.Getenv("CASEBOOK_INTERNAL") != "" {
		return 0
	}
	if _, err := os.Stat(config.Path()); err != nil {
		return 0 // not initialized here: nothing would ever drain the spool
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		// An unreadable config still records (sync reports the config);
		// it only loses the configured roots' exemption from the temp rule.
		cfg, _ := config.Load()
		Handle(args[0], args[1:], stdin, config.SpoolDir(), time.Now(), temppath.New(cfg.Roots))
	}()
	select {
	case <-done:
	case <-time.After(900 * time.Millisecond):
	}
	return 0
}

// Handle turns one hook invocation into a spool line, unless temp (when not
// nil) says it happened in a temp folder. Errors are dropped: losing one
// journal line is better than slowing or failing git.
func Handle(name string, args []string, stdin io.Reader, spoolDir string, now time.Time, temp *temppath.Matcher) {
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
	if temp != nil && temp.Event(ev) {
		return
	}
	id := harness.FromEnv()
	ev.ClaudeID, ev.AgentID, ev.Child = id.ClaudeID, id.AgentID, id.Child
	_ = spool.Append(spoolDir, ev)
}
