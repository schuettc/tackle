// Package ops is imprint's npm operations: each does one npm step, tells
// the agent what it means as it goes, and is done only once the registry
// shows its end state.
package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/subaud-tools/tackle/internal/imprint/npm"
	"github.com/subaud-tools/tackle/internal/imprint/registry"
)

// Report hears an operation's progress, in plain sentences for the agent.
type Report func(text string)

// Ops runs operations against a registry and npm. A publish or expected
// version is looked for every Poll; at Warn unseen the agent is told, and
// at GiveUp the watch ends.
type Ops struct {
	Reg                *registry.Client
	NPM                *npm.Runner
	Poll, Warn, GiveUp time.Duration

	mu      sync.Mutex
	pending map[string]bool // name@version published and not yet seen live
}

// Defaults are the watch intervals: every 15 seconds, a warning at 15
// minutes, giving up at an hour.
const (
	DefaultPoll   = 15 * time.Second
	DefaultWarn   = 15 * time.Minute
	DefaultGiveUp = 60 * time.Minute
)

// fail reports err (when there is one) and returns it: every operation
// ends with its failure told to the agent.
func fail(report Report, err error) error {
	if err != nil {
		report(err.Error())
	}
	return err
}

// npm runs one npm command, telling the agent of each approval, and turns
// a failed exit into an error naming npm's code, what it usually means and
// the file holding npm's full output.
func (o *Ops) npm(ctx context.Context, op, pkg string, args []string, report Report) (npm.Result, error) {
	res, err := o.NPM.Run(ctx, op, pkg, args, func(e npm.Event) {
		switch e.Kind {
		case npm.ApprovalNeeded:
			report("waiting for your approval: " + e.URL + " (opened in the browser)")
		case npm.ApprovalGiven:
			report("approved")
		}
	})
	if err != nil {
		return res, fmt.Errorf("failed: couldn't run npm %s: %w", args[0], err)
	}
	if res.ExitCode != 0 {
		return res, npmFailure(res)
	}
	return res, nil
}

// npmFailure is a failed npm call as the agent should hear it.
func npmFailure(res npm.Result) error {
	what := res.Code
	if what == "" {
		what = fmt.Sprintf("npm exited %d", res.ExitCode)
	}
	if ex := npm.Explain(res.Code, res.Output); ex != "" {
		what += ": " + ex
	} else if line := errorLine(res.Output); line != "" {
		what += ": " + line
	}
	return fmt.Errorf("failed: %s; npm's full output: %s", what, res.Log)
}

// errorLine is npm's first error message other than its code and log
// lines.
func errorLine(output string) string {
	for l := range strings.SplitSeq(output, "\n") {
		s, ok := strings.CutPrefix(strings.TrimSpace(l), "npm error ")
		if !ok || strings.HasPrefix(s, "code ") || strings.HasPrefix(s, "A complete log") || strings.HasPrefix(s, "Log files were not written") {
			continue
		}
		return s
	}
	return ""
}

// lastLine is the last non-empty line npm printed that isn't one of its
// notices or warnings.
func lastLine(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s != "" && !strings.HasPrefix(s, "npm ") {
			return s
		}
	}
	return ""
}

// errNotLive is a watch that gave up.
var errNotLive = errors.New("not live")

// watch reads name until version is live, telling the agent at Warn and
// giving up at GiveUp. Registry errors along the way are retried.
func (o *Ops) watch(ctx context.Context, name, version string, report Report) error {
	start := time.Now()
	warned := false
	t := time.NewTicker(o.Poll)
	defer t.Stop()
	for {
		p, err := o.Reg.Package(ctx, name)
		if err == nil && p.Has(version) {
			return nil
		}
		since := time.Since(start)
		if since >= o.GiveUp {
			return fmt.Errorf("gave up: %s@%s still isn't visible on npm after %s; check it later with imprint_show: %w", name, version, human(o.GiveUp), errNotLive)
		}
		if since >= o.Warn && !warned {
			warned = true
			report(fmt.Sprintf("%s@%s is still not visible on npm after %s; still watching", name, version, human(o.Warn)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// human is a duration as a sentence says it ("15 minutes").
func human(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		if d == time.Minute {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", d/time.Minute)
	}
	return d.String()
}

// Login makes sure npm is logged in: whoami first, and only when npm
// isn't logged in, a web login whose link is opened like an approval.
func (o *Ops) Login(ctx context.Context, report Report) error {
	user, err := o.whoami(ctx, report)
	if err == nil {
		report("logged in as " + user)
		return nil
	}
	if !errors.Is(err, errLoggedOut) {
		return fail(report, err)
	}
	report("npm isn't logged in; starting a web login")
	if _, err := o.npm(ctx, "login", "", []string{"login", "--auth-type=web"}, report); err != nil {
		return fail(report, err)
	}
	user, err = o.whoami(ctx, report)
	if err != nil {
		return fail(report, err)
	}
	report("logged in as " + user)
	return nil
}

var errLoggedOut = errors.New("npm isn't logged in")

// whoami is the npm user; errLoggedOut (wrapped) when npm isn't logged in.
func (o *Ops) whoami(ctx context.Context, report Report) (string, error) {
	res, err := o.npm(ctx, "whoami", "", []string{"whoami"}, report)
	if err != nil {
		if res.Code == "E401" || res.Code == "ENEEDAUTH" {
			return "", fmt.Errorf("%w: %w", err, errLoggedOut)
		}
		return "", err
	}
	user := lastLine(res.Output)
	if user == "" {
		return "", fmt.Errorf("failed: npm whoami printed no user; npm's full output: %s", res.Log)
	}
	return user, nil
}
