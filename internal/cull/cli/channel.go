package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/schuettc/tackle/internal/cull/channel"
	"github.com/schuettc/tackle/internal/cull/serve"
	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
)

var channelFlags = flags("channel", "cull channel",
	"Runs cull's MCP server on stdin/stdout for an agent session (Claude Code or pi starts it).\n"+
		"It starts cull serve if needed, registers the session, and delivers Court's answers as\n"+
		"channel events. Gives the agent cull_check, cull_apply, cull_check_group, cull_review and\n"+
		"cull_status. stdout carries MCP frames only; logs go to stderr.",
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
		client := channel.NewClient(func() (serve.Advert, error) { return serve.Start(executable(), 0) })
		ch := channel.New(channel.FromEnv(), client, version.Number())
		ch.Log = errw
		return ch.Run(ctx, stdin, out)
	}
}
