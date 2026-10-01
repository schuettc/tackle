package cli

import (
	"context"
	"database/sql"
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

	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tackle/internal/cull/store"
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

var serveFlags = flags("serve", "cull serve [path] [--no-open] [--port N] [--foreground] [--stop]",
	"Starts the review page's server in the background (one per machine) and opens the\n"+
		"project's review. When it is already running, opens the running one. Needs no egress.",
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
	if len(pos) > 1 {
		return tools.UsageError{Msg: "serve takes at most one path"}
	}
	ctx := context.Background()
	if boolFlag(fs, "stop") {
		if err := serve.Stop(ctx); errors.Is(err, serve.ErrNotRunning) {
			_, _ = fmt.Fprintln(out, "cull serve is not running")
			return nil
		} else if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "cull serve stopped")
		return nil
	}
	port := 0
	if v, err := strconv.Atoi(str(fs, "port")); err == nil {
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

	path := "."
	if len(pos) == 1 {
		path = pos[0]
	}
	if _, err := os.Stat(path); err != nil {
		return tools.Exitf(2, "%v", err)
	}
	root, err := discover.Root(path)
	if err != nil {
		return tools.Exitf(2, "%v", err)
	}
	st, err := store.Open(ctx, store.Path())
	if err != nil {
		return err
	}
	proj, err := st.Project(ctx, root)
	if err != nil {
		_ = st.Close()
		return err
	}
	_, _, runErr := st.LatestRun(ctx, proj.ID)
	_ = st.Close()
	if runErr != nil && !errors.Is(runErr, sql.ErrNoRows) {
		return runErr
	}

	adv, err := serve.Running()
	switch {
	case errors.Is(err, serve.ErrNotRunning):
		if adv, err = startDetached(port); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "cull serve started: %s\n", adv.Base)
	case err != nil:
		return err
	default:
		_, _ = fmt.Fprintf(out, "cull serve is running: %s\n", adv.Base)
	}
	if runErr != nil {
		_, _ = fmt.Fprintf(out, "no review for %s yet — run cull check\n", root)
	}
	if !boolFlag(fs, "no-open") {
		_ = openBrowser(adv.URL + "#/p/" + strconv.FormatInt(proj.ID, 10))
	}
	return nil
}
