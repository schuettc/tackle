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
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/serve"
	"github.com/schuettc/tools-common/channelmcp"
	"github.com/schuettc/tools-common/harness"
)

// Instructions are the standing rules the agent gets with the channel
// (casebook workbench spec §6.3).
const Instructions = `casebook is Court's record of what should happen to every repo, pull request, issue, branch and worktree. Its page (casebook serve) and this channel let you work on it with him.

Messages from Court arrive as channel events from casebook. Several may arrive together; they were queued while you were busy.
- Read every message in a delivery before acting on any. Later messages may refine or cancel earlier ones; when they conflict, follow the latest and say so.
- Messages were written before your last turn's results: if one is already answered by what you did, say so rather than redoing it.
- Answer the page only through casebook tools, never only in your own session: settle EVERY message with casebook_reply (answered, declined or failed, with your reply text). Use state "working" on long work.
- On anything that takes more than a few seconds, keep casebook_progress updated ("checking CI on #671", n of total).
- You never decide. Propose with casebook_propose; Court accepts, changes or rejects on the page.
- Attach what you find to items with casebook_evidence.
- casebook_status tells you the attention counts and what Court did with your proposals since you last looked. What Court was looking at comes attached to each message; there is no live view of his page.
- Open the page (casebook_open) only when Court asks or when handing him something to review; never repeatedly.
- Item keys look like repo:owner/name, pr:owner/name#N, issue:owner/name#N, branch:owner/name@branch, worktree:machine:/abs/path.`

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

// Channel is a running casebook channel.
type Channel struct {
	ID     Identity
	Client *Client
	Server *channelmcp.Server
	Log    io.Writer
	// Retry is how long to wait before looking for serve again; Presence how
	// often to refresh presence; Poll the long-poll timeout. Tests shorten them.
	Retry    time.Duration
	Presence time.Duration
	Poll     time.Duration
	// runCtx is Run's context, set before Server.Run begins; the Call closure
	// uses it so tool calls end with the channel. Nil before Run has started
	// (not expected in practice): fall back to Background.
	runCtx context.Context
}

// New builds a channel for id.
func New(id Identity, c *Client, version string) *Channel {
	ch := &Channel{ID: id, Client: c, Log: io.Discard, Retry: 5 * time.Second, Presence: 30 * time.Second, Poll: 55 * time.Second}
	ch.Server = channelmcp.New(channelmcp.Handler{
		Name: "casebook", Version: version, Instructions: Instructions, Tools: Tools(),
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

// Run serves MCP on r/w and, when the session is known, the wake loop.
func (ch *Channel) Run(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Tool calls run under ctx, so they end with the channel: channelMain
	// cancels it on SIGINT/SIGTERM, which is how a harness stops the channel.
	// Set before Server.Run starts reading, so the Call closure never races it.
	ch.runCtx = ctx
	if ch.ID.Session != "" {
		go ch.loop(ctx)
	}
	return ch.Server.Run(r, w)
}

func (ch *Channel) presence(ctx context.Context, c *Client) error {
	_, err := c.Do(ctx, http.MethodPost, "/api/agent/presence", map[string]any{
		"id": ch.ID.Session, "harness": ch.ID.Harness, "label": ch.ID.Label, "cwd": ch.ID.CWD, "pid": os.Getpid()}, nil)
	return err
}

// restarted is the channel event when serve came back as a new process.
func restarted(adv serve.Advert) string {
	s := "casebook serve restarted. Anything that was in flight to you is marked interrupted on the page; nothing was lost."
	if adv.Reopened {
		s += " The page was open, so it reopened in a new tab."
	}
	return s
}

// loop keeps presence fresh and long-polls for deliveries, emitting each as a
// channel event. The next poll is armed as soon as an event is queued
// (channelmcp.Notify only queues), so nothing sent meanwhile is missed. It
// never starts serve (only tool calls do), and it tells the agent when serve
// came back as a new process.
func (ch *Channel) loop(ctx context.Context) {
	c := ch.Client.passive()
	lastPresence := time.Time{}
	var seen time.Time // StartedAt of the serve this loop last reached
	for ctx.Err() == nil {
		if adv, err := c.Find(); err == nil && !adv.StartedAt.Equal(seen) {
			if !seen.IsZero() {
				if err := ch.Server.Notify(restarted(adv), map[string]string{"source": "casebook", "event": "restarted"}); err != nil {
					fmt.Fprintf(ch.Log, "casebook channel: notify: %v\n", err)
				}
			}
			seen, lastPresence = adv.StartedAt, time.Time{}
		}
		if time.Since(lastPresence) > ch.Presence {
			if err := ch.presence(ctx, c); err != nil {
				fmt.Fprintf(ch.Log, "casebook channel: %v\n", err)
				sleep(ctx, ch.Retry)
				continue
			}
			lastPresence = time.Now()
		}
		var got struct {
			Delivery deliver.Delivery `json:"delivery"`
			Text     string           `json:"text"`
		}
		code, err := c.Do(ctx, http.MethodGet, "/api/agent/wait?"+q("session", ch.ID.Session, "timeout", strconv.Itoa(int(ch.Poll.Seconds()))), nil, &got)
		switch {
		case err != nil:
			var se *StatusError
			if errors.As(err, &se) && se.Code == http.StatusNotFound {
				lastPresence = time.Time{} // serve restarted and forgot us
			}
			fmt.Fprintf(ch.Log, "casebook channel: %v\n", err)
			sleep(ctx, ch.Retry)
		case code == http.StatusOK && got.Delivery.ID != 0:
			var ids []string
			for _, m := range got.Delivery.Messages {
				ids = append(ids, strconv.FormatInt(m.ID, 10))
			}
			meta := map[string]string{"source": "casebook", "delivery": strconv.FormatInt(got.Delivery.ID, 10), "messages": strings.Join(ids, ",")}
			if err := ch.Server.Notify(got.Text, meta); err != nil {
				fmt.Fprintf(ch.Log, "casebook channel: notify: %v\n", err)
			}
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
