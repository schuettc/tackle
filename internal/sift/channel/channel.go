// Package channel is `sift channel`: the MCP server each agent session
// starts (Claude Code through its mcpServers, pi through channels.tools). It
// starts sift serve when it isn't running, registers the session's presence,
// holds a long poll open for the user's decisions and injects each Send as a
// notifications/claude/channel event, and gives the agent sift's tools. The
// presence and wait loops are copied in shape from cull's channel (sift does
// not import it).
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
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tools-common/channelmcp"
	"github.com/schuettc/tools-common/harness"
	"github.com/schuettc/tools-common/localweb"
)

// Instructions are the standing guidance the agent gets with the channel.
const Instructions = `sift audits the instruction files coding agents read (global files, repo files, skills). For each file with findings you recommend one revised version, and the user accepts, edits or rejects each file on a review page.
- Run sift_check when the user asks for an audit, or when sift due says one is due. It records a round and says how many files need a recommendation.
- Recommend every file: sift_next gives one file with its content, findings and the guidance for the rewrite; sift_propose stores the whole revised file. Repeat until sift_next says done. Recommend files that move text between them in one sift_propose call, each linking the other.
- Then run sift_review once to open the page for the user, tell them how many files wait, and carry on with other work. The page opens only once every file has a recommendation.
- The user's decisions arrive as a channel event from sift. Follow its steps: sift_apply writes a branch per repo; then run ` + "`sift reconcile`" + ` and review each branch.
- sift_status tells you the round's state and what is open, decided and sent.
- When sift reports an error, say what failed and leave the files as they are.`

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

// Channel is a running sift channel.
type Channel struct {
	ID     Identity
	Client *Client
	Server *channelmcp.Server
	Log    io.Writer
	// Open opens a URL in the user's browser (tests replace it).
	Open func(url string) error
	// LoadConfig reads sift's config; LookPath finds gh (tests replace them).
	LoadConfig func() (config.Config, error)
	LookPath   func(string) (string, error)
	// Retry is how long to wait before trying serve again; Presence how often
	// to refresh presence; Poll the long-poll timeout. Tests shorten them.
	Retry    time.Duration
	Presence time.Duration
	Poll     time.Duration

	runCtx context.Context
}

// New builds a channel for id.
func New(id Identity, c *Client, version string) *Channel {
	ch := &Channel{ID: id, Client: c, Log: io.Discard, Open: localweb.OpenBrowser,
		LoadConfig: func() (config.Config, error) { return config.Load(config.Path()) }, LookPath: exec.LookPath,
		Retry: 5 * time.Second, Presence: 30 * time.Second, Poll: 55 * time.Second}
	ch.Server = channelmcp.New(channelmcp.Handler{
		Name: "sift", Version: version, Instructions: Instructions, Tools: Tools(),
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

// Run serves MCP on r/w and, when the session is known, the presence and
// wake loop.
func (ch *Channel) Run(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch.runCtx = ctx // set before Server.Run starts reading
	if ch.ID.Session == "" {
		_, _ = fmt.Fprintln(ch.Log, "sift channel: no session id (not under Claude Code or pi); the user's decisions will not reach this session")
	} else {
		go ch.loop(ctx)
	}
	return ch.Server.Run(r, w)
}

func (ch *Channel) presence(ctx context.Context) error {
	_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/presence", map[string]any{
		"session": ch.ID.Session, "harness": ch.ID.Harness, "label": ch.ID.Label}, nil)
	return err
}

// loop starts serve if needed, keeps presence fresh and long-polls for sends,
// emitting each claimed send as one channel event. serve claims a send for
// exactly one caller, so an event is never duplicated.
func (ch *Channel) loop(ctx context.Context) {
	var lastPresence time.Time
	var lastErr string
	logOnce := func(err error) {
		if msg := err.Error(); msg != lastErr {
			_, _ = fmt.Fprintf(ch.Log, "sift channel: %s\n", msg)
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
			ID    int64  `json:"id"`
			Round int64  `json:"round"`
			Text  string `json:"text"`
		}
		code, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/wait?"+q(
			"session", ch.ID.Session, "timeout", strconv.Itoa(max(int(ch.Poll.Seconds()), 1))), nil, &got)
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
			meta := map[string]string{"source": "sift", "round": strconv.FormatInt(got.Round, 10), "send_id": strconv.FormatInt(got.ID, 10)}
			if err := ch.Server.Notify(got.Text, meta); err != nil {
				_, _ = fmt.Fprintf(ch.Log, "sift channel: notify: %v\n", err)
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
