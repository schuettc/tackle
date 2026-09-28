package serve

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/bus"
	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/rules"
	"github.com/schuettc/tools-common/localweb"
)

//go:embed assets
var assets embed.FS

// Server is a running casebook serve.
type Server struct {
	App   *app.App
	DB    *db.DB
	Queue *deliver.Queue
	Props *propose.Store
	Bus   *bus.Bus
	Index *Index
	Now   func() time.Time

	// Wait is the long-poll timeout cap and WatchEvery the HEAD poll; tests
	// shorten them.
	Wait       time.Duration
	WatchEvery time.Duration

	mu        sync.Mutex
	rebuildMu sync.Mutex               // serializes rebuild: one at a time, including rule evaluation
	waiters   map[string]chan struct{} // session → wake
	activity  atomic.Int64             // unix ms of the last API request
	streams   atomic.Int32             // open page event streams (connected tabs)
	streamWG  sync.WaitGroup           // Run waits for streams to end before closing the database
	rebuilds  atomic.Int32             // count of rebuild calls; exposed for tests to verify no loops
	life      context.Context          // Run's context; done while shutting down
	stop      context.CancelFunc
	// openPage opens the page in Court's browser at a route fragment ("" for
	// the front, "#/item/<key>", "#/attention/<view>") for casebook_open; nil
	// when serve can't open a browser.
	openPage func(fragment string) error
}

// The page_open flag in meta records whether a tab was connected when serve
// went away. It is set while any page event stream is open and cleared when
// the last one closes because the tab closed. A stop or a crash leaves it set,
// which is true: the page was open. The next serve reads it at start, reopens
// the page if set, and clears it; the new tab sets it again when it connects.
func (s *Server) setPageOpen(ctx context.Context, open bool) {
	v := "0"
	if open {
		v = "1"
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES ('page_open', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, v)
}

// takePageOpen reports whether a tab was connected when the previous serve
// went away, and clears the flag.
func (s *Server) takePageOpen(ctx context.Context) bool {
	var v string
	_ = s.DB.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'page_open'").Scan(&v)
	s.setPageOpen(ctx, false)
	return v == "1"
}

// pageStream wraps the page's event stream: it counts connected tabs, keeps
// the flag, and keeps serve from idling out under an open page.
func (s *Server) pageStream(w http.ResponseWriter, r *http.Request) {
	s.streamWG.Add(1)
	defer s.streamWG.Done()
	// End the stream when serve stops: http.Server.Shutdown doesn't cancel
	// open streams, so without this they outlive Run and the database.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.life != nil {
		defer context.AfterFunc(s.life, cancel)()
	}
	r = r.WithContext(ctx)
	if s.streams.Add(1) == 1 {
		s.setPageOpen(r.Context(), true)
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.activity.Store(time.Now().UnixMilli())
			}
		}
	}()
	defer func() {
		close(done)
		// A stream that ends because serve is stopping doesn't mean the tab
		// closed: leave the flag set so the next serve reopens the page.
		if s.streams.Add(-1) == 0 && (s.life == nil || s.life.Err() == nil) {
			s.setPageOpen(context.Background(), false)
		}
	}()
	s.Bus.ServeSSE(w, r)
}

// PageOpen reports whether a tab is connected now.
func (s *Server) PageOpen() bool { return s.streams.Load() > 0 }

// New wires a server over an opened app and database. It marks deliveries a
// previous serve left in flight as interrupted and builds the index.
func New(ctx context.Context, a *app.App, d *db.DB) (*Server, error) {
	s := &Server{App: a, DB: d, Queue: deliver.New(d), Props: propose.New(d), Bus: bus.New(d), Index: &Index{},
		Now: time.Now, Wait: 60 * time.Second, WatchEvery: 5 * time.Second, waiters: map[string]chan struct{}{}}
	if n, err := s.Queue.Interrupt(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		s.Bus.Publish(ctx, "interrupted", map[string]int{"deliveries": n})
	}
	if err := s.rebuild(ctx); err != nil {
		return nil, err
	}
	s.activity.Store(time.Now().UnixMilli())
	return s, nil
}

func (s *Server) build(ctx context.Context) (engine.Result, error) {
	r, _, err := s.App.Build(ctx)
	return r, err
}

func (s *Server) repoHead(ctx context.Context) string {
	h, _ := gitx.Run(ctx, s.App.Repo.Dir, "rev-parse", "HEAD")
	return h
}

// rebuild recomputes the index from the current repo state, evaluates active
// rules and announces the new state. It is the only trigger for rule
// evaluation: a sync moves HEAD, serve's watch sees it and calls rebuild, and
// serve evaluates rules on its first build. The CLI sync never opens the
// database and never evaluates rules (spec
// §4.2 updated: rules run in serve, not in casebook sync).
//
// rebuild is serialized by rebuildMu so that rule evaluation never runs twice
// at once. Rule-error notices are set atomically with the rebuilt result under
// one Index lock (via Index.set), so a concurrent reader never sees a new Head
// without the accompanying notices.
func (s *Server) rebuild(ctx context.Context) error {
	// Bug 2 fix: serialize rebuilds so rule evaluation never overlaps.
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()

	s.rebuilds.Add(1)

	// Build the engine result first (outside any Index lock).
	now := s.Now()
	head := s.repoHead(ctx)
	res, err := s.build(ctx)
	if err != nil {
		return err
	}

	// Load rules and evaluate active ones. Rule-load and validation errors are
	// collected as notices; they never abort the rebuild.
	allRules, ruleErrs := s.App.Repo.Rules()
	var notices []string
	for _, re := range ruleErrs {
		notices = append(notices, "rule: "+re.Error())
	}
	var active []rules.Rule
	for _, r := range allRules {
		if err := r.Validate(); err != nil {
			notices = append(notices, "rule "+r.ID+": "+err.Error())
			continue
		}
		if r.Status == rules.StatusActive {
			active = append(active, r)
		}
	}

	// Evaluate active rules against the freshly built result. Proposals are
	// written to SQLite only; they do not move HEAD and cannot trigger another
	// rebuild (the watch loop only rebuilds on a HEAD change).
	//
	// Bug 1: EvaluateActive now returns rule-level Propose errors (joined).
	// Surface them as index notices so the page can display them, and log
	// to stderr for diagnostics.
	var proposed int
	if len(active) > 0 {
		var evalErr error
		proposed, evalErr = rules.EvaluateActive(ctx, active, res, now, s.Props)
		if evalErr != nil {
			fmt.Fprintf(os.Stderr, "casebook serve: rules evaluate: %v\n", evalErr)
			notices = append(notices, "rules: "+evalErr.Error())
		}
	}

	// Bug 3 fix: set the index result and all its notices atomically under one
	// Index lock. Append rule notices to the engine result's own notices so
	// that a reader who sees the new Head also sees all notices immediately.
	res.Notices = append(res.Notices, notices...)
	s.Index.set(res, head, now)

	pending, _ := s.Props.Pending(ctx)
	if _, err := s.Bus.Publish(ctx, "index", map[string]any{"counts": s.Index.Counts(pending), "head": s.Index.Head()}); err != nil {
		return err
	}
	if proposed > 0 {
		_, err := s.Bus.Publish(ctx, "rules", map[string]int{"proposed": proposed})
		return err
	}
	return nil
}

// watch rebuilds whenever the casebook repo's HEAD moves (a sync or a CLI
// decision elsewhere), and trims the event log daily.
func (s *Server) watch(ctx context.Context) {
	t := time.NewTicker(s.WatchEvery)
	defer t.Stop()
	lastTrim := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if h := s.repoHead(ctx); h != "" && h != s.Index.Head() {
				if err := s.rebuild(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "casebook serve: rebuild: %v\n", err)
				}
			}
			if time.Since(lastTrim) > 24*time.Hour {
				_ = s.Bus.Trim(ctx, 7*24*time.Hour)
				lastTrim = time.Now()
			}
		}
	}
}

// wake releases a session's pending long-poll.
func (s *Server) wake(session string) {
	s.mu.Lock()
	ch := s.waiters[session]
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Server) waiter(session string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.waiters[session]
	if ch == nil {
		ch = make(chan struct{}, 1)
		s.waiters[session] = ch
	}
	return ch
}

// Handler is the /api/ handler localweb mounts behind its token check.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	// live events (the kit's live() contract)
	m.HandleFunc("GET /api/state", s.Bus.ServePoll)
	m.HandleFunc("GET /api/events", s.pageStream)
	// page
	m.HandleFunc("GET /api/summary", s.getSummary)
	m.HandleFunc("GET /api/items", s.getItems)
	m.HandleFunc("GET /api/item", s.getItem)
	m.HandleFunc("POST /api/decide", s.postDecide)
	m.HandleFunc("POST /api/proposals/accept", s.postAccept)
	m.HandleFunc("POST /api/proposals/change", s.postChange)
	m.HandleFunc("POST /api/proposals/reject", s.postReject)
	m.HandleFunc("GET /api/sessions", s.getSessions)
	m.HandleFunc("GET /api/threads", s.getThreads)
	m.HandleFunc("POST /api/threads", s.postThread)
	m.HandleFunc("POST /api/threads/move", s.postMoveThread)
	m.HandleFunc("GET /api/messages", s.getMessages)
	m.HandleFunc("POST /api/messages", s.postMessage)
	m.HandleFunc("POST /api/messages/resend", s.postResend)
	m.HandleFunc("POST /api/drafts/edit", s.postEditDraft)
	m.HandleFunc("POST /api/drafts/remove", s.postRemoveDraft)
	m.HandleFunc("POST /api/batches/reorder", s.postReorder)
	m.HandleFunc("POST /api/batches/send", s.postSendBatch)
	m.HandleFunc("POST /api/deliveries/release", s.postRelease)
	m.HandleFunc("POST /api/deliveries/move", s.postMoveDelivery)
	m.HandleFunc("POST /api/stop", s.postStop)
	// agent (casebook channel)
	m.HandleFunc("POST /api/agent/presence", s.agentPresence)
	m.HandleFunc("GET /api/agent/wait", s.agentWait)
	m.HandleFunc("POST /api/agent/reply", s.agentReply)
	m.HandleFunc("POST /api/agent/propose", s.agentPropose)
	m.HandleFunc("POST /api/agent/evidence", s.agentEvidence)
	m.HandleFunc("POST /api/agent/progress", s.agentProgress)
	m.HandleFunc("GET /api/agent/status", s.agentStatus)
	m.HandleFunc("POST /api/agent/open", s.agentOpen)
	m.HandleFunc("POST /api/agent/settled", s.agentSettled)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.activity.Store(time.Now().UnixMilli())
		m.ServeHTTP(w, r)
	})
}

// httpError carries a status out of a handler helper.
// errCode, when non-empty, is written as a "code" field in the JSON error
// response for machine-readable disambiguation (e.g. "unknown_session").
type httpError struct {
	code    int
	msg     string
	errCode string
}

func (e httpError) Error() string { return e.msg }

func bad(format string, a ...any) error {
	return httpError{code: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

// decode reads a JSON body, rejecting unknown fields and oversized bodies.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return bad("bad request body: %v", err)
	}
	return nil
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		var he httpError
		switch {
		case errors.As(err, &he):
			code = he.code
		case errors.Is(err, deliver.ErrNotFound), errors.Is(err, propose.ErrNotFound):
			code = http.StatusNotFound
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		body := map[string]string{"error": err.Error()}
		if errors.As(err, &he) && he.errCode != "" {
			body["code"] = he.errCode
		}
		json.NewEncoder(w).Encode(body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	json.NewEncoder(w).Encode(v)
}

// Options configures Run.
type Options struct {
	Version string
	Port    int           // 0: remembered, else free
	Idle    time.Duration // shut down after this long with no API traffic (0: never)
	// Ready is called once listening. pageWasOpen: a tab was connected when
	// the previous serve went away (a stop, an upgrade or a crash), so the
	// caller should open the page again; the old tab's token died with it.
	Ready   func(url string, pageWasOpen bool)
	Open    func(url string) error // opens the page in the browser (casebook_open); nil: can't
	Log     io.Writer              // diagnostics
	Assets  fs.FS                  // nil: the embedded placeholder
	Started func(*Server, Advert)  // test hook
}

// Run serves until ctx ends, /api/stop, or the idle limit. It writes the
// advert while running and removes it on the way out.
func Run(ctx context.Context, a *app.App, o Options) error {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if adv, err := Running(); err == nil {
		return fmt.Errorf("casebook serve is already running (pid %d, %s)", adv.PID, adv.Base)
	}
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return err
	}
	d, err := db.Open(ctx, config.StateDir()+"/casebook.db")
	if err != nil {
		return err
	}
	defer d.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s, err := New(ctx, a, d)
	if err != nil {
		return err
	}
	s.stop, s.life = cancel, ctx
	wasOpen := s.takePageOpen(ctx)
	assetsFS := o.Assets
	if assetsFS == nil {
		assetsFS, _ = fs.Sub(assets, "assets")
	}
	srv, err := localweb.Start(ctx, localweb.Config{Tool: config.Tool, Assets: assetsFS, API: s.Handler(), Port: o.Port})
	if err != nil {
		return err
	}
	// Reopened records that a tab was connected when the previous serve went
	// away, whether or not a Ready callback is set to open the page again.
	adv := Advert{URL: srv.URL, Base: "http://" + srv.Addr(), Token: srv.Token, PID: os.Getpid(), Version: o.Version, StartedAt: time.Now().UTC(),
		Reopened: wasOpen}
	if o.Open != nil {
		s.openPage = func(fragment string) error { return o.Open(srv.URL + fragment) }
	}
	if err := writeAdvert(adv); err != nil {
		cancel()
		return err
	}
	defer removeAdvert(adv.PID)
	fmt.Fprintf(o.Log, "casebook serve: %s (pid %d)\n", adv.Base, adv.PID)
	go s.watch(ctx)
	if o.Idle > 0 {
		go s.idle(ctx, o.Idle)
	}
	if o.Started != nil {
		o.Started(s, adv)
	}
	if o.Ready != nil {
		o.Ready(srv.URL, wasOpen)
	}
	err = srv.Wait()
	s.streamWG.Wait() // streams end with ctx; the database closes after them
	return err
}

func (s *Server) idle(ctx context.Context, limit time.Duration) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(time.UnixMilli(s.activity.Load())) > limit {
				s.stop()
				return
			}
		}
	}
}

// Stop asks the live serve to exit.
func Stop(ctx context.Context) error {
	adv, err := Running()
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, adv.Base+"/api/stop", strings.NewReader("{}"))
	req.Header.Set(localweb.TokenHeader, adv.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	for i := 0; i < 50; i++ {
		if _, err := Running(); errors.Is(err, ErrNotRunning) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("casebook serve (pid %d) did not stop", adv.PID)
}

func (s *Server) postStop(w http.ResponseWriter, r *http.Request) {
	reply(w, nil, nil)
	if s.stop != nil {
		go func() { time.Sleep(50 * time.Millisecond); s.stop() }()
	}
}

// Start runs exe as `casebook serve --foreground --no-open` in its own process
// group, logging to StateDir/serve.log, and waits for its advert. The CLI and
// every agent tool call use it: serve starts on demand, never by hand.
func Start(exe string, port int) (Advert, error) {
	if exe == "" {
		return Advert{}, errors.New("can't find the casebook binary to start the server")
	}
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return Advert{}, err
	}
	logPath := filepath.Join(config.StateDir(), "serve.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Advert{}, err
	}
	defer logf.Close()
	args := []string{"serve", "--foreground", "--no-open"}
	if port > 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return Advert{}, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	for i := 0; i < 100; i++ {
		if adv, err := Running(); err == nil && adv.PID == pid {
			return adv, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return Advert{}, fmt.Errorf("casebook serve (pid %d) didn't start; see %s", pid, logPath)
}
