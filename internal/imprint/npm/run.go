// Package npm runs npm, one command at a time, inside a terminal imprint
// creates, so npm offers its browser approvals without anyone pressing
// Enter, and reports each approval link the moment npm prints it.
package npm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// EventKind is what happened to an approval.
type EventKind int

const (
	// ApprovalNeeded: npm printed a link the user must approve in the
	// browser.
	ApprovalNeeded EventKind = iota + 1
	// ApprovalGiven: npm moved past the approval (to its next one, or it
	// exited 0).
	ApprovalGiven
)

func (k EventKind) String() string {
	switch k {
	case ApprovalNeeded:
		return "approval needed"
	case ApprovalGiven:
		return "approval given"
	}
	return "unknown"
}

// Event is one approval's step; URL is the approval's link.
type Event struct {
	Kind EventKind
	URL  string
}

// Result is how an npm call ended.
type Result struct {
	ExitCode int
	Code     string // npm's error code, e.g. E403; "" for none
	Output   string // what npm printed, as plain text
	Log      string // the file holding npm's full output
}

// Runner runs npm. NPM is the npm binary ("npm" when empty), LogDir where
// each call's full output is saved, and Open opens an approval link in
// the user's browser.
type Runner struct {
	NPM    string
	LogDir string
	Open   func(url string) error
	mu     sync.Mutex
}

// Run runs npm with args plus --browser=false for operation op on package
// pkg (pkg names the log file; it may be empty), holding the runner for
// the whole call so only one npm runs at a time. events, when not nil,
// hears each approval as it is needed and given. An error means npm could
// not be run at all; npm's own failure is in the Result.
func (r *Runner) Run(ctx context.Context, op, pkg string, args []string, events func(Event)) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if events == nil {
		events = func(Event) {}
	}
	logf, err := r.createLog(op, pkg)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = logf.Close() }()
	bin := r.NPM
	if bin == "" {
		bin = "npm"
	}
	cmd := exec.CommandContext(ctx, bin, append(append([]string{}, args...), "--browser=false")...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	_, _ = fmt.Fprintf(logf, "$ %s\n", strings.Join(cmd.Args, " "))
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 400})
	if err != nil {
		return Result{Log: logf.Name()}, fmt.Errorf("starting npm: %w", err)
	}
	defer func() { _ = tty.Close() }()

	var out strings.Builder
	pending := ""   // the approval npm is waiting on
	expect := false // the next non-empty line is an approval link
	need := func(url string) {
		if url == "" || url == pending {
			return
		}
		if pending != "" {
			events(Event{Kind: ApprovalGiven, URL: pending})
		}
		pending = url
		if r.Open != nil {
			_ = r.Open(url)
		}
		events(Event{Kind: ApprovalNeeded, URL: url})
	}
	rd := bufio.NewReader(io.TeeReader(tty, logf))
	for {
		raw, err := rd.ReadString('\n')
		if raw != "" {
			line := clean(strings.TrimSuffix(raw, "\n"))
			out.WriteString(line + "\n")
			t := strings.TrimSpace(line)
			switch {
			case expect && t != "":
				expect = false
				if u := urlRE.FindString(t); u != "" {
					need(u)
				}
			default:
				if ok, u := approvalPrompt(t); ok {
					if u = urlRE.FindString(u); u != "" {
						need(u)
					} else {
						expect = true
					}
				}
			}
		}
		if err != nil {
			break // EOF, or EIO once npm has exited and closed the terminal
		}
	}
	res := Result{Output: out.String(), Log: logf.Name()}
	werr := cmd.Wait()
	var ee *exec.ExitError
	switch {
	case werr == nil:
		if pending != "" {
			events(Event{Kind: ApprovalGiven, URL: pending})
		}
	case errors.As(werr, &ee):
		res.ExitCode = ee.ExitCode()
		if res.ExitCode < 0 {
			res.ExitCode = 1
		}
	default:
		return res, werr
	}
	res.Code = ErrorCode(res.Output)
	_, _ = fmt.Fprintf(logf, "\n[imprint] npm exited %d at %s\n", res.ExitCode, time.Now().UTC().Format(time.RFC3339))
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	return res, nil
}

var (
	urlRE  = regexp.MustCompile(`https?://\S+`)
	slugRE = regexp.MustCompile(`[^a-z0-9]+`)
)

// createLog makes a new file for one call's output:
// <LogDir>/<UTC yyyymmddThhmmss>-<op>-<pkg-slug>.log.
func (r *Runner) createLog(op, pkg string) (*os.File, error) {
	if err := os.MkdirAll(r.LogDir, 0o700); err != nil {
		return nil, err
	}
	base := time.Now().UTC().Format("20060102T150405") + "-" + op
	if s := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(pkg), "-"), "-"); s != "" {
		base += "-" + s
	}
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		f, err := os.OpenFile(filepath.Join(r.LogDir, name+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return f, err
	}
}
