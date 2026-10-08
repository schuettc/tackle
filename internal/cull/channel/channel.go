// Package channel is `cull channel`: the MCP server each agent session starts
// (Claude Code through its mcpServers, pi through channels.tools). It starts
// cull serve when it isn't running, registers the session's presence, holds a
// long poll open for Court's answers and injects each Send as a
// notifications/claude/channel event, and gives the agent cull's tools. The
// presence and wait loops are copied in shape from casebook's channel (cull
// does not import it).
//
// stdout carries MCP frames only; every log line goes to the Log writer
// (stderr in the command).
package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tools-common/channelmcp"
	"github.com/schuettc/tools-common/harness"
	"github.com/schuettc/tools-common/localweb"
)

// Instructions are the standing rules the agent gets with the channel.
const Instructions = `cull judges automated tests with Jev and removes the ones that don't earn their place. Court reviews only what Jev is unsure about, on cull's review page.
- After you write or change tests, run cull_check with base set to the merge base of your branch (or no base for the whole suite when Court asks for it).
- Then run cull_apply with no ids: it removes every cut, both Jev's confident ones and Court's, runs the tests before and after, and puts everything back if they break. Never commit on cull's behalf; report what was removed.
- If cull_check reports to_review above 0, run cull_review once to open the page for Court, tell him how many items wait, and carry on with other work. Never ask Court about a test anywhere but the page, and never open the page repeatedly.
- Court's answers arrive as a channel event from cull. Follow its steps: cull_check, then cull_apply for his cuts; rewrite each group he chose to merge as one table test that keeps every row the event lists, then run cull_check_group on it and fix what it reports.
- cull_status tells you what is open, answered and sent for this project.
- When your session runs in a folder that holds several repositories (a workspace), pass the repository's path to every cull tool. The event from Court names the repository's path; use it.
- You never answer for Court. If cull reports an error, say what failed; don't work around it by editing tests by hand.
- When Court asks why tests are slow, or checks take long, run cull_time and fix what it and cull_check's speed findings point at; then run cull_time again and report before and after.`

// Identity is the session this channel serves.
type Identity struct {
	Session string
	Harness string // "claude" or "pi"
	Label   string
	CWD     string
}

// FromEnv resolves the session by the family rule (tools-common harness).
// Session is "" outside a harness.
func FromEnv() Identity {
	c := harness.FromEnv()
	id := Identity{Session: c.SessionID, CWD: c.CWD, Harness: "pi"}
	if c.SessionID != "" && c.SessionID == c.ClaudeID {
		id.Harness = "claude"
	}
	if id.CWD == "" {
		id.CWD, _ = os.Getwd()
	}
	id.Label = id.Harness + " · " + filepath.Base(id.CWD)
	return id
}

// Channel is a running cull channel.
type Channel struct {
	ID     Identity
	Client *Client
	Server *channelmcp.Server
	Log    io.Writer
	// Open opens a URL in Court's browser (tests replace it).
	Open func(url string) error
	// Retry is how long to wait before trying serve again; Presence how often
	// to refresh presence; Poll the long-poll timeout. Tests shorten them.
	Retry    time.Duration
	Presence time.Duration
	Poll     time.Duration

	root    string // the session's project root; "" when it can't be resolved
	rootErr error
	runCtx  context.Context
}

// New builds a channel for id. The project root is discover.Root of the
// session's working directory.
func New(id Identity, c *Client, version string) *Channel {
	ch := &Channel{ID: id, Client: c, Log: io.Discard, Open: localweb.OpenBrowser,
		Retry: 5 * time.Second, Presence: 30 * time.Second, Poll: 55 * time.Second}
	ch.root, ch.rootErr = discover.Root(id.CWD)
	ch.Server = channelmcp.New(channelmcp.Handler{
		Name: "cull", Version: version, Instructions: Instructions, Tools: Tools(),
		Call: func(name string, args json.RawMessage) (string, error) {
			ctx := ch.runCtx
			if ctx == nil {
				ctx = context.Background()
			}
			return ch.Call(ctx, name, args)
		},
	})
	return ch
}

// scope is the session's folder: the session covers the repository that
// contains it and every repository inside it (a workspace root), so Court's
// Send for any of them reaches this session.
func (ch *Channel) scope() string {
	if abs, err := filepath.Abs(ch.ID.CWD); err == nil {
		return abs
	}
	return ch.root
}

// Run serves MCP on r/w and, when the session and its project are known, the
// presence and wake loop.
func (ch *Channel) Run(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch.runCtx = ctx // set before Server.Run starts reading
	switch {
	case ch.ID.Session == "":
		_, _ = fmt.Fprintln(ch.Log, "cull channel: no session id (not under Claude Code or pi); Court's answers will not reach this session")
	case ch.rootErr != nil:
		_, _ = fmt.Fprintf(ch.Log, "cull channel: %v\n", ch.rootErr)
	default:
		go ch.loop(ctx)
	}
	return ch.Server.Run(r, w)
}

func (ch *Channel) presence(ctx context.Context) error {
	_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/presence", map[string]any{
		"session": ch.ID.Session, "harness": ch.ID.Harness, "label": ch.ID.Label, "root": ch.scope()}, nil)
	return err
}

// loop starts serve if needed, keeps presence fresh and long-polls for sends,
// emitting each claimed send as one channel event. The next poll is armed as
// soon as an event is queued (Notify only queues), so nothing sent meanwhile
// is missed. serve claims a send for exactly one caller, so an event is never
// duplicated.
func (ch *Channel) loop(ctx context.Context) {
	var lastPresence time.Time
	var lastErr string
	logOnce := func(err error) {
		if msg := err.Error(); msg != lastErr {
			_, _ = fmt.Fprintf(ch.Log, "cull channel: %s\n", msg)
			lastErr = msg
		}
	}
	for ctx.Err() == nil {
		if time.Since(lastPresence) > ch.Presence {
			if err := ch.presence(ctx); err != nil {
				logOnce(err)
				sleep(ctx, ch.Retry)
				continue
			}
			lastPresence, lastErr = time.Now(), ""
		}
		var got struct {
			ID   int64  `json:"id"`
			Root string `json:"root"`
			Text string `json:"text"`
		}
		code, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/wait?"+q(
			"session", ch.ID.Session, "root", ch.scope(), "timeout", strconv.Itoa(max(int(ch.Poll.Seconds()), 1))), nil, &got)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			var se *StatusError
			if errors.As(err, &se) {
				lastPresence = time.Time{}
			}
			logOnce(err)
			sleep(ctx, ch.Retry)
		case code == http.StatusOK && got.ID != 0:
			lastErr = ""
			meta := map[string]string{"source": "cull", "project": got.Root, "send_id": strconv.FormatInt(got.ID, 10)}
			if err := ch.Server.Notify(got.Text, meta); err != nil {
				_, _ = fmt.Fprintf(ch.Log, "cull channel: notify: %v\n", err)
			}
		default:
			lastErr = ""
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
