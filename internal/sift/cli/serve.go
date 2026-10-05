package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/schuettc/tackle/internal/sift/serve"
	"github.com/schuettc/tackle/internal/sift/store"
	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
	"github.com/schuettc/tools-common/localweb"
)

// Test seams for the serve command.
var (
	openBrowser   = localweb.OpenBrowser
	startDetached = defaultStartDetached
)

func defaultStartDetached(port int) (serve.Advert, error) { return serve.Start(executable(), port) }

func executable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

var serveFlags = flags("serve", "sift serve [--no-open] [--port N] [--foreground] [--stop]",
	"Starts the review page's server in the background (one per machine) and opens the latest\n"+
		"round's review. When it is already running, opens the running one. Needs no network.",
	func(fs *flag.FlagSet) {
		fs.Bool("no-open", false, "don't open the browser")
		fs.Bool("foreground", false, "run in this process until stopped (used by the background start)")
		fs.Bool("stop", false, "stop the running server")
		fs.Int("port", 0, "port (default: the last one used, else a free one)")
	})

func runServe(args []string, out, errw io.Writer) error {
	fs := serveFlags()
	pos, err := parse(fs, args, out)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return tools.UsageError{Msg: "serve takes no arguments"}
	}
	ctx := context.Background()
	if boolFlag(fs, "stop") {
		if err := serve.Stop(ctx); errors.Is(err, serve.ErrNotRunning) {
			_, _ = fmt.Fprintln(out, "sift serve is not running")
			return nil
		} else if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "sift serve stopped")
		return nil
	}
	port := 0
	if v, err := strconv.Atoi(fs.Lookup("port").Value.String()); err == nil {
		port = v
	}
	if boolFlag(fs, "foreground") {
		st, err := store.Open(ctx, store.Path())
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		sctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serve.Run(sctx, st, serve.Options{Port: port, Idle: 8 * time.Hour, Version: version.Number(), Log: errw,
			Ready: func(string) {}})
	}

	adv, err := serve.Running()
	switch {
	case errors.Is(err, serve.ErrNotRunning):
		if adv, err = startDetached(port); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "sift serve started: %s\n", adv.Base)
	case err != nil:
		return err
	default:
		_, _ = fmt.Fprintf(out, "sift serve is running: %s\n", adv.Base)
	}
	if st, err := store.Open(ctx, store.Path()); err == nil {
		if _, _, err := st.Latest(ctx); err != nil {
			_, _ = fmt.Fprintln(out, "no round yet: run sift check")
		}
		_ = st.Close()
	}
	if !boolFlag(fs, "no-open") {
		_ = openBrowser(adv.URL)
	}
	return nil
}
