package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/schuettc/tackle/internal/sift/channel"
	"github.com/schuettc/tackle/internal/sift/serve"
	tools "github.com/schuettc/tools-common"
)

const (
	waitPresence = 30 * time.Second // how often presence is registered
	waitPoll     = 25 * time.Second // one long poll (serve caps it at 60 s)
)

var waitFlags = flags("wait", "sift wait [--timeout D]",
	"Waits until the user presses Send on the review page, then prints what was sent and what to\n"+
		"do next. For sessions without the sift channel. Starts sift serve if needed and registers\n"+
		"this wait as a present session while it runs. No timeout by default.\n"+
		"Exit: 0 a send arrived (printed), 3 timed out with nothing sent, 130 interrupted.",
	func(fs *flag.FlagSet) {
		fs.Duration("timeout", 0, "give up after this long (default: wait forever)")
	})

func runWait(args []string, out, errw io.Writer) error {
	fs := waitFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "wait takes no arguments"}
	}
	timeout := fs.Lookup("timeout").Value.(flag.Getter).Get().(time.Duration)
	if timeout < 0 || (timeout == 0 && fsSet(fs, "timeout")) {
		return tools.UsageError{Msg: "--timeout must be positive"}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	client := channel.NewClient(func() (serve.Advert, error) { return startDetached(0) })
	session := "wait:" + strconv.Itoa(os.Getpid())
	wd, _ := os.Getwd()
	label := "sift wait · " + wd

	var lastPresence time.Time
	for {
		if ctx.Err() != nil {
			return &tools.ExitError{Code: 130, Msg: "interrupted"}
		}
		if time.Since(lastPresence) > waitPresence {
			if _, err := client.Do(ctx, http.MethodPost, "/api/agent/presence", map[string]any{
				"session": session, "harness": "wait", "label": label}, nil); err != nil {
				if ctx.Err() != nil {
					continue
				}
				return tools.Exitf(1, "%v", err)
			}
			lastPresence = time.Now()
		}
		poll := waitPoll
		if !deadline.IsZero() {
			left := time.Until(deadline)
			if left <= 0 {
				return tools.Exitf(3, "nothing sent")
			}
			poll = min(poll, left)
		}
		secs := int((poll + time.Second - 1) / time.Second)
		var got struct {
			Text string `json:"text"`
		}
		code, err := client.Do(ctx, http.MethodGet, fmt.Sprintf("/api/agent/wait?session=%s&timeout=%d",
			url.QueryEscape(session), secs), nil, &got)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			return tools.Exitf(1, "%v", err)
		}
		if code == http.StatusOK && got.Text != "" {
			_, _ = io.WriteString(out, got.Text+"\n")
			return nil
		}
	}
}

// fsSet reports whether the flag was given on the command line.
func fsSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}
