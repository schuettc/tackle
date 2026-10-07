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
const Instructions = `casebook is the user's record of what should happen to every repo, pull request, issue, branch and worktree. Its page (casebook serve) and this channel let you work on it with them.

Messages from the user arrive as channel events from casebook. Several may arrive together; they were queued while you were busy.
- Read every message in a delivery before acting on any. Later messages may refine or cancel earlier ones; when they conflict, follow the latest and say so.
- Messages were written before your last turn's results: if one is already answered by what you did, say so rather than redoing it.
- Answer the page only through casebook tools, never only in your own session: settle EVERY message with casebook_reply (answered, declined or failed, with your reply text). Use state "working" on long work.
- On anything that takes more than a few seconds, keep casebook_progress updated ("checking CI on #671", n of total).
- You never decide. Propose with casebook_propose; the user accepts, changes or rejects on the page.
- Attach what you find to items with casebook_evidence.
- casebook_status tells you the attention counts and what the user did with your proposals since you last looked. What the user was looking at comes attached to each message; there is no live view of their page.
- Open the page (casebook_open) only when the user asks or when handing them something to review; never repeatedly.
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

// presence announces the session. pid is the HARNESS process's (the
// channel's parent: pi's channels.tools and Claude Code spawn the channel
// directly), not the channel's own: pi-subagents runs worker sessions
// inside their parent's pi process, each with its own channel, so a shared
// harness pid is how serve tells a worker from a fork (which runs in a pi
// process of its own). pi-casebook reports the same pid (pi's process.pid).
func (ch *Channel) presence(ctx context.Context, c *Client) error {
	_, err := c.Do(ctx, http.MethodPost, "/api/agent/presence", map[string]any{
		"id": ch.ID.Session, "harness": ch.ID.Harness, "label": ch.ID.Label, "cwd": ch.ID.CWD, "pid": os.Getppid()}, nil)
	return err
}

// restarted is the channel event when serve came back as a new process and
// its start interrupted messages in flight to this session; "" when it
// interrupted none (then the restart is none of this session's business).
// It speaks only of the session's own work: whether Court's page reopened
// is about Court's browser, not the agent's.
func restarted(v serve.InterruptedView) string {
	n := len(v.Messages)
	if n == 0 {
		return ""
	}
	ids := make([]string, n)
	for i, id := range v.Messages {
		ids[i] = strconv.FormatInt(id, 10)
	}
	what := "1 message"
	if n > 1 {
		what = strconv.Itoa(n) + " messages"
	}
	return fmt.Sprintf("casebook serve restarted while %s to you %s in flight (%s). %s marked interrupted on the page; nothing was lost. "+
		"You can still settle %s with casebook_reply.",
		what, plural(n, "was", "were"), strings.Join(ids, ", "), plural(n, "It is", "They are"), plural(n, "it", "them"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// interrupted asks serve whether its start interrupted a delivery of this
// session, for the restart to the serve started at startedAt. Anything but a
// clear answer about that serve (an older serve without the endpoint, an
// error, an answer about another serve) is no answer: ok false, and the
// channel says nothing.
func (ch *Channel) interrupted(ctx context.Context, c *Client, startedAt time.Time) (serve.InterruptedView, bool) {
	var v serve.InterruptedView
	if _, err := c.Do(ctx, http.MethodGet, "/api/agent/interrupted?"+q("session", ch.ID.Session), nil, &v); err != nil {
		return v, false
	}
	if !v.StartedAt.Equal(startedAt) {
		return v, false
	}
	return v, true
}

// logLoopErrOnce writes err to w only when it is not ErrNoServe and its
// message differs from *last. On write it updates *last. Callers clear *last
// on success so the same error is logged again after a recovery.
func logLoopErrOnce(w io.Writer, last *string, err error) {
	if errors.Is(err, ErrNoServe) {
		return
	}
	msg := err.Error()
	if msg == *last {
		return
	}
	_, _ = fmt.Fprintf(w, "casebook channel: %v\n", err)
	*last = msg
}

// loop keeps presence fresh and long-polls for deliveries, emitting each as a
// channel event. The next poll is armed as soon as an event is queued
// (channelmcp.Notify only queues), so nothing sent meanwhile is missed. It
// never starts serve (only tool calls do). When serve came back as a new
// process it tells the agent only if that restart interrupted messages in
// flight to this session (serve says so); otherwise, or when serve can't
// say, it stays silent.
func (ch *Channel) loop(ctx context.Context) {
	c := ch.Client.passive()
	lastPresence := time.Time{}
	var seen time.Time     // StartedAt of the serve this loop last reached
	var lastLoopErr string // last non-ErrNoServe loop error logged; "" after success
	for ctx.Err() == nil {
		if adv, err := c.Find(); err == nil && !adv.StartedAt.Equal(seen) {
			if !seen.IsZero() {
				if v, ok := ch.interrupted(ctx, c, adv.StartedAt); ok {
					if text := restarted(v); text != "" {
						if err := ch.Server.Notify(text, map[string]string{"source": "casebook", "event": "restarted"}); err != nil {
							_, _ = fmt.Fprintf(ch.Log, "casebook channel: notify: %v\n", err)
						}
					}
				}
			}
			seen, lastPresence = adv.StartedAt, time.Time{}
		}
		if time.Since(lastPresence) > ch.Presence {
			if err := ch.presence(ctx, c); err != nil {
				logLoopErrOnce(ch.Log, &lastLoopErr, err)
				sleep(ctx, ch.Retry)
				continue
			}
			lastPresence = time.Now()
			lastLoopErr = ""
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
			logLoopErrOnce(ch.Log, &lastLoopErr, err)
			sleep(ctx, ch.Retry)
		case code == http.StatusOK && got.Delivery.ID != 0:
			lastLoopErr = ""
			var ids []string
			for _, m := range got.Delivery.Messages {
				ids = append(ids, strconv.FormatInt(m.ID, 10))
			}
			meta := map[string]string{"source": "casebook", "delivery": strconv.FormatInt(got.Delivery.ID, 10), "messages": strings.Join(ids, ",")}
			if err := ch.Server.Notify(got.Text, meta); err != nil {
				_, _ = fmt.Fprintf(ch.Log, "casebook channel: notify: %v\n", err)
			}
		default:
			lastLoopErr = ""
		}
	}
}

// callSessionBound executes a session-bound API call fn. If serve responds with
// a 404 whose JSON body carries "code":"unknown_session" (meaning serve started
// fresh and the wake loop hasn't registered presence yet, or serve restarted
// and forgot the session), callSessionBound registers presence using ch.Client
// (which may start serve) and retries fn once.
//
// Any other error — including a 404 for a missing message or item — is returned
// immediately without a retry, so a wrong message id does not cause a spurious
// presence registration.
func (ch *Channel) callSessionBound(ctx context.Context, fn func() (string, error)) (string, error) {
	result, err := fn()
	if err == nil {
		return result, nil
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound || se.ErrCode != "unknown_session" {
		return result, err
	}
	// 404 with code="unknown_session": serve doesn't know our session.
	// Register presence (ch.Client may start serve) then retry once.
	if presErr := ch.presence(ctx, ch.Client); presErr != nil {
		return "", err // return the original 404 error, not the presence error
	}
	return fn()
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
