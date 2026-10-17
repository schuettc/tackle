package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/subaud-tools/tackle/internal/imprint/channel"
	"github.com/subaud-tools/tackle/internal/imprint/npm"
	"github.com/subaud-tools/tackle/internal/imprint/ops"
	"github.com/subaud-tools/tackle/internal/imprint/registry"
	"github.com/subaud-tools/tackle/internal/version"
	tools "github.com/subaud-tools/tools-common"
	"github.com/subaud-tools/tools-common/localweb"
)

var channelFlags = flags("channel", "imprint channel",
	"Runs imprint's MCP server on stdin/stdout for an agent session (Claude Code or pi starts it).\n"+
		"Gives the agent imprint_login, imprint_show, imprint_publish, imprint_trust,\n"+
		"imprint_deprecate, imprint_expect and imprint_status. npm runs one call at a time in a\n"+
		"terminal imprint creates; each approval link opens in the browser, and progress reaches\n"+
		"the session as channel messages. Each npm call's full output is saved under\n"+
		"~/.local/state/imprint/. stdout carries MCP frames only; logs go to stderr.",
	nil)

func runChannel(stdin io.Reader) func(args []string, out, errw io.Writer) error {
	return func(args []string, out, errw io.Writer) error {
		fs := channelFlags()
		pos, err := parse(fs, args, out)
		if err != nil {
			return err
		}
		if len(pos) > 0 {
			return tools.UsageError{Msg: "channel takes no arguments"}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		o := &ops.Ops{
			Reg:  registry.New(),
			NPM:  &npm.Runner{NPM: "npm", LogDir: tools.StateDir("imprint"), Open: localweb.OpenBrowser},
			Poll: ops.DefaultPoll, Warn: ops.DefaultWarn, GiveUp: ops.DefaultGiveUp,
		}
		ch := channel.New(o, version.Number())
		ch.Log = errw
		return ch.Run(ctx, stdin, out)
	}
}
