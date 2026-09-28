package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/schuettc/tackle/internal/casebook/channel"
	"github.com/schuettc/tackle/internal/casebook/serve"
	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
	"github.com/schuettc/tools-common/harness"
	"github.com/schuettc/tools-common/localweb"
)

// Test seams for the workbench commands.
var (
	openBrowser   = localweb.OpenBrowser
	startDetached = defaultStartDetached
)

// defaultStartDetached starts serve in the background from this binary.
func defaultStartDetached(port int) (serve.Advert, error) { return serve.Start(executable(), port) }

func serveFlags() *flag.FlagSet {
	return flags("serve", "casebook serve [--no-open] [--port N] [--foreground] [--stop]",
		"Starts the casebook page and agent server in the background (one per machine) and opens it. When it's already running, opens the running one.", func(fs *flag.FlagSet) {
			fs.Bool("no-open", false, "don't open the browser")
			fs.Bool("foreground", false, "run in this process until stopped (used by the background start)")
			fs.Bool("stop", false, "stop the running server")
			fs.Int("port", 0, "port (default: the last one used, else a free one)")
		})()
}

func settledFlags() *flag.FlagSet {
	return flags("settled", "casebook settled --session ID | --harness claude < hook-payload.json",
		"Tells casebook serve an agent's turn ended, so its queued page messages go out. Called by pi-casebook and the Claude Code Stop hook. Never fails, never prints.", func(fs *flag.FlagSet) {
			fs.String("session", "", "the session whose turn ended")
			fs.String("harness", "", "claude: read the session id from the Stop hook payload on stdin")
		})()
}

func workbenchCommands(stdin io.Reader) []tools.Command {
	return []tools.Command{
		{
			Name: "serve", Group: "observe", Synopsis: "serve [--no-open] [--port N] [--stop]",
			Summary: "open the casebook page (starts the local server if needed)", NewFlags: serveFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := serveFlags()
				if _, err := parse(fs, args, out); err != nil {
					return err
				}
				ctx := context.Background()
				if boolFlag(fs, "stop") {
					if err := serve.Stop(ctx); errors.Is(err, serve.ErrNotRunning) {
						fmt.Fprintln(out, "casebook serve is not running")
						return nil
					} else if err != nil {
						return err
					}
					fmt.Fprintln(out, "casebook serve stopped")
					return nil
				}
				port, _ := strconv.Atoi(strFlag(fs, "port"))
				if boolFlag(fs, "foreground") {
					a, err := open()
					if err != nil {
						return err
					}
					sctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
					defer stop()
					return serve.Run(sctx, a, serve.Options{Version: version.Number(), Port: port, Idle: 8 * time.Hour, Log: errw,
						// A restart reopens a page that was open: its tab's
						// token died with the old serve.
						Ready: func(url string, pageWasOpen bool) {
							if !boolFlag(fs, "no-open") || pageWasOpen {
								_ = openBrowser(url)
							}
						},
						Open: func(url string) error { return openBrowser(url) }})
				}
				adv, err := serve.Running()
				if errors.Is(err, serve.ErrNotRunning) {
					if _, err := open(); err != nil {
						return err
					}
					if adv, err = startDetached(port); err != nil {
						return err
					}
					fmt.Fprintf(out, "casebook serve started: %s\n", adv.Base)
				} else if err != nil {
					return err
				} else {
					fmt.Fprintf(out, "casebook serve is running: %s\n", adv.Base)
				}
				if !boolFlag(fs, "no-open") {
					_ = openBrowser(adv.URL)
				}
				return nil
			},
		},
		{
			Name: "settled", Group: "plumbing", Summary: "report an agent's turn ended (never fails)", NewFlags: settledFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := settledFlags()
				if _, err := parse(fs, args, io.Discard); err != nil {
					return nil
				}
				// The 2 s budget covers both the stdin read and the HTTP call so
				// an unclosed stdin (e.g. a hung Claude hook) never blocks forever.
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				session := strFlag(fs, "session")
				if strFlag(fs, "harness") == "claude" {
					type readResult struct{ b []byte }
					done := make(chan readResult, 1)
					go func() {
						b, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
						done <- readResult{b}
					}()
					select {
					case res := <-done:
						session = harness.FromHookPayload(res.b).SessionID
					case <-ctx.Done():
						return nil
					}
				}
				if session == "" {
					return nil
				}
				c := channel.NewClient()
				_, _ = c.Do(ctx, http.MethodPost, "/api/agent/settled", map[string]string{"session": session}, nil)
				return nil
			},
		},
		{Name: "channel", Group: "plumbing", Summary: "the MCP channel an agent session runs: tools + wake events (stdio; self-routed)"},
		{Name: "mcp", Group: "plumbing", Summary: "casebook's tools over MCP without wake events (for pi-mcp-adapter; stdio; self-routed)"},
	}
}

// channelMain is `casebook channel`: MCP on stdin/stdout, diagnostics on stderr.
// It exposes the nine casebook tools AND runs the wake loop (presence +
// long-poll). Claude Code uses one such process per session for both.
func channelMain(stdin io.Reader, out, errw io.Writer) int {
	c := channel.NewClient()
	c.Start = func() (serve.Advert, error) { return startDetached(0) } // any tool call starts serve
	ch := channel.New(channel.FromEnv(), c, version.Number())
	ch.Log = errw
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ch.Run(ctx, stdin, out); err != nil {
		fmt.Fprintf(errw, "casebook channel: %v\n", err)
		return 1
	}
	return 0
}

// mcpMain is `casebook mcp`: the same nine tools over MCP on stdin/stdout, but
// with NO wake loop (no presence, no long-poll). Intended for pi-mcp-adapter
// (configured in ~/.config/mcp/mcp.json), which needs the tools but must not
// compete with the channels.tools casebook channel for deliveries.
// Session-bound tool calls still work via callSessionBound's register-on-404.
func mcpMain(stdin io.Reader, out, errw io.Writer) int {
	c := channel.NewClient()
	c.Start = func() (serve.Advert, error) { return startDetached(0) } // any tool call starts serve
	ch := channel.New(channel.FromEnv(), c, version.Number())
	ch.NoWake = true
	ch.Log = errw
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := ch.Run(ctx, stdin, out); err != nil {
		fmt.Fprintf(errw, "casebook mcp: %v\n", err)
		return 1
	}
	return 0
}
