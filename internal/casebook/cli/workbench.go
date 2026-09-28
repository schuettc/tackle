package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
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
	openBrowser      = localweb.OpenBrowser
	startDetached    = defaultStartDetached
	newSettledClient = func() *channel.Client { return channel.NewClient() }
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
			fs.String("shown", "", "comma-separated delivery ids the agent was shown this turn (bad values ignored)")
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
				var shownIDs []int64
				// Parse --shown flag: comma-separated ids; bad values ignored.
				if s := strFlag(fs, "shown"); s != "" {
					for _, part := range strings.Split(s, ",") {
						if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil {
							shownIDs = append(shownIDs, id)
						}
					}
				}
				if strFlag(fs, "harness") == "claude" {
					type readResult struct {
						b []byte
					}
					done := make(chan readResult, 1)
					go func() {
						b, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
						done <- readResult{b}
					}()
					select {
					case res := <-done:
						cap := harness.FromHookPayload(res.b)
						session = cap.SessionID
						// Scan the transcript for casebook deliveries the agent was shown.
						if cap.TranscriptPath != "" {
							if ids := shownInTranscript(ctx, cap.TranscriptPath); len(ids) > 0 {
								shownIDs = append(shownIDs, ids...)
							}
						}
					case <-ctx.Done():
						return nil
					}
				}
				if session == "" {
					return nil
				}
				c := newSettledClient()
				_, _ = c.Do(ctx, http.MethodPost, "/api/agent/settled", map[string]any{"session": session, "shown": shownIDs}, nil)
				return nil
			},
		},
		{Name: "channel", Group: "plumbing", Summary: "the MCP channel an agent session runs (stdio; self-routed)"},
	}
}

// chanTagRE matches a <channel ...> opening tag; deliveryRE extracts delivery="N";
// casebookSourceRE checks for source="casebook" within the tag attributes.
var (
	chanTagRE        = regexp.MustCompile(`<channel\s([^>]*)>`)
	deliveryAttrRE   = regexp.MustCompile(`delivery="(\d+)"`)
	casebookSourceRE = regexp.MustCompile(`source="casebook"`)
)

// shownInTranscript reads at most the last 8 MiB of transcriptPath (a Claude
// Code JSONL transcript) and returns the delivery ids of casebook deliveries the
// agent was shown. Entries of type "queue-operation" are excluded (those only
// mean queued, not shown). Any failure returns nil silently.
func shownInTranscript(ctx context.Context, transcriptPath string) []int64 {
	if transcriptPath == "" {
		return nil
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Read at most the last 8 MiB.
	const maxBytes = 8 << 20
	if fi, err := f.Stat(); err == nil && fi.Size() > maxBytes {
		if _, err := f.Seek(-maxBytes, io.SeekEnd); err != nil {
			return nil
		}
	}

	type entry struct {
		Type    string `json:"type"`
		Message *struct {
			Content string `json:"content"`
		} `json:"message"`
		Attachment *struct {
			Type   string `json:"type"`
			Prompt string `json:"prompt"`
		} `json:"attachment"`
	}

	seen := map[int64]bool{}
	sc := bufio.NewScanner(io.LimitReader(f, maxBytes))
	sc.Buffer(make([]byte, 256*1024), 256*1024)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return deliveryList(seen)
		default:
		}
		line := sc.Bytes()
		if !bytes.Contains(line, []byte("casebook")) {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		switch e.Type {
		case "queue-operation":
			// These only mean queued, not shown — skip.
			continue
		case "user":
			if e.Message != nil {
				extractCasebookDeliveries(e.Message.Content, seen)
			}
		case "attachment":
			if e.Attachment != nil && e.Attachment.Type == "queued_command" {
				extractCasebookDeliveries(e.Attachment.Prompt, seen)
			}
		}
	}
	return deliveryList(seen)
}

// extractCasebookDeliveries scans text for <channel source="casebook" ...> tags
// and collects their delivery="N" values into seen.
func extractCasebookDeliveries(text string, seen map[int64]bool) {
	for _, m := range chanTagRE.FindAllStringSubmatch(text, -1) {
		attrs := m[1]
		if !casebookSourceRE.MatchString(attrs) {
			continue
		}
		if dm := deliveryAttrRE.FindStringSubmatch(attrs); dm != nil {
			if id, err := strconv.ParseInt(dm[1], 10, 64); err == nil {
				seen[id] = true
			}
		}
	}
}

// deliveryList converts a seen map to a sorted (insertion-order-independent) slice.
func deliveryList(seen map[int64]bool) []int64 {
	if len(seen) == 0 {
		return nil
	}
	out := make([]int64, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// channelMain is `casebook channel`: MCP on stdin/stdout, diagnostics on stderr.
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
