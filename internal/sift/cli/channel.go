package cli

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/schuettc/tackle/internal/sift/channel"
	"github.com/schuettc/tackle/internal/sift/serve"
	"github.com/schuettc/tackle/internal/version"
	tools "github.com/schuettc/tools-common"
)

var channelFlags = flags("channel", "sift channel",
	"Runs sift's MCP server on stdin/stdout for an agent session (Claude Code or pi starts it).\n"+
		"It starts sift serve if needed, registers the session, and delivers the user's decisions\n"+
		"as channel events. Gives the agent sift_check, sift_review, sift_apply and sift_status.\n"+
		"stdout carries MCP frames only; logs go to stderr.",
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
		ch.LookPath = lookPath
		return ch.Run(ctx, stdin, out)
	}
}
