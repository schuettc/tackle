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
	"net/url"
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
	"github.com/schuettc/tackle/internal/casebook/apply"
	"github.com/schuettc/tackle/internal/casebook/bus"
	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/db"
	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/rules"
	"github.com/schuettc/tools-common/localweb"
	"github.com/schuettc/tools-common/localweb/page"
)

//go:embed assets
var assets embed.FS

// Server is a running casebook serve.
type Server struct {
	App    *app.App
	DB     *db.DB
	Queue  *deliver.Queue
	Props  *propose.Store
	Apply  *apply.Store
	Bus    *bus.Bus
	Index  *Index
	Now    func() time.Time
	Runner apply.Runner // for the casebook lane (nil: no-op runner)
	// Log receives serve's non-fatal error lines (nil: os.Stderr); tests
	// capture it.
	Log io.Writer

	// Wait is the long-poll timeout cap and WatchEvery the HEAD poll; tests
	// shorten them.
	Wait       time.Duration
	WatchEvery time.Duration

	mu        sync.Mutex
	rebuildMu sync.Mutex               // serializes rebuild: one at a time, including rule evaluation
	waiters   map[string]chan struct{} // session → wake
	streamMu  sync.Mutex               // guards streams counter + setPageOpen decision (one critical section)

	laneMu            sync.Mutex        // guards laneRun
	laneRun           map[int64]bool    // job id → a casebook lane is running for it
	onLaneStart       func(jobID int64) // test hook: called once per lane that actually starts
	onBeforeLaneExit  func(jobID int64) // test hook: called just before the lane goroutine clears laneRun
	activity          atomic.Int64      // unix ms of the last API request
	streams           atomic.Int32      // open page event streams (connected tabs)
	streamWG          sync.WaitGroup    // Run waits for streams to end before closing the database
	rebuilds          atomic.Int32      // count of rebuild calls; exposed for tests to verify no loops
	ruleCountRuns     atomic.Int32      // rules counted (MatchAll runs) for the rules list; for tests
	ruleCountMu       sync.Mutex
	ruleCounts        map[string]ruleCount            // rule id → its match count for one content + index
	syncMu            sync.Mutex                      // guards syncing
	planMu            sync.Mutex                      // one plan or approve at a time: the overlap check and its write are one step
	afterOverlapCheck func()                          // test hook: runs between the overlap check and the create/approve
	syncing           bool                            // a sync POST /api/sync started is running
	runSync           func(ctx context.Context) error // test seam for syncNow (nil: App.Sync)
	// The background push (push.go). remoteMu is serve's own serialization
	// of remote git work on casebook-data (the push, POST /api/sync's sync);
	// pushMu guards the pusher's state and is never held across git.
	remoteMu    sync.Mutex
	pushMu      sync.Mutex
	pushRunning bool                                   // a push is in flight (or about to start)
	pushAgain   bool                                   // a decide landed during it: push once more after it
	pushFailed  bool                                   // the last push ended offline or failed
	pushError   string                                 // why the last push failed, when not offline (cleared by a push that succeeds, or nothing queued)
	pushStopped bool                                   // serve is stopping: schedulePush starts nothing
	pushWG      sync.WaitGroup                         // Run waits for the pusher before closing the database
	runPush     func(ctx context.Context) error        // test seam for App.Push
	after       func(d time.Duration) <-chan time.Time // serve's clock for the pusher's retries (nil: time.After)
	life        context.Context                        // Run's context; done while shutting down
	stop        context.CancelFunc
	// openPage opens the page in Court's browser for casebook_open: suffix
	// is appended to the page URL (…/?t=<token>), an optional
	// SessionQuery then an optional route fragment ("#/item/<key>",
	// "#/attention/<view>"). nil when serve can't open a browser.
	openPage func(suffix string) error
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
	// Increment and the flag write are one critical section: a concurrent
	// decrement+clear between our Add and setPageOpen would otherwise leave the
	// flag stuck at "1" after the last tab closes.
	connCtx := r.Context()
	s.streamMu.Lock()
	if s.streams.Add(1) == 1 {
		s.setPageOpen(connCtx, true)
	}
	s.streamMu.Unlock()
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
		// Decrement and the flag write are one critical section (mirrors the
		// increment path above).
		s.streamMu.Lock()
		if s.streams.Add(-1) == 0 && (s.life == nil || s.life.Err() == nil) {
			s.setPageOpen(context.Background(), false)
		}
		s.streamMu.Unlock()
	}()
	s.Bus.ServeSSE(w, r)
}

// PageOpen reports whether a tab is connected now.
func (s *Server) PageOpen() bool { return s.streams.Load() > 0 }

// New wires a server over an opened app and database. It marks deliveries a
// previous serve left in flight as interrupted and builds the index.
func New(ctx context.Context, a *app.App, d *db.DB) (*Server, error) {
	q := deliver.New(d)
	if sa := os.Getenv("CASEBOOK_STUCK_AFTER"); sa != "" {
		if d, err := time.ParseDuration(sa); err == nil && d > 0 {
			q.StuckAfter = d
		}
	}
	if la := os.Getenv("CASEBOOK_LEFT_AFTER"); la != "" {
		if d, err := time.ParseDuration(la); err == nil && d > 0 {
			q.LeftAfter = d
		}
	}
	watchEvery := 5 * time.Second
	if we := os.Getenv("CASEBOOK_WATCH_EVERY"); we != "" {
		if d, err := time.ParseDuration(we); err == nil && d > 0 {
			watchEvery = d
		}
	}
	s := &Server{App: a, DB: d, Queue: q, Props: propose.New(d), Apply: apply.NewStore(d), Bus: bus.New(d), Index: &Index{},
		Now: time.Now, Wait: 60 * time.Second, WatchEvery: watchEvery, waiters: map[string]chan struct{}{}, laneRun: map[int64]bool{}}
	// Record the serve lifetime context now so lanes started during New (a
	// restart resume) are cancelled when serve stops. Run passes the same
	// cancelable context and re-records it alongside s.stop.
	s.life = ctx
	s.Runner = apply.Runner{
		Repo:   a.Repo,
		Gh:     a.Gh,
		RunGit: gitx.Run,
		Now:    s.Now,
		Notify: s.publish,
	}
	if n, err := s.Queue.Interrupt(ctx); err != nil {
		return nil, err
	} else if n > 0 {
		s.publish(ctx, "interrupted", map[string]int{"deliveries": n})
	}
	if err := s.rebuild(ctx); err != nil {
		return nil, err
	}
	// Resume jobs a previous serve left mid-run: casebook-lane steps stuck in
	// running go back to pending and their lanes relaunch (each step re-checks
	// the world first, so this is safe).
	if err := s.resumeInterruptedJobs(ctx); err != nil {
		return nil, err
	}
	s.pruneSessions(ctx)
	s.activity.Store(time.Now().UnixMilli())
	return s, nil
}

// laneCtx is the context casebook lanes run under: serve's lifetime, so lanes
// are cancelled when serve stops (never context.Background()).
func (s *Server) laneCtx() context.Context {
	if s.life != nil {
		return s.life
	}
	return context.Background()
}

// publish fires a bus event and, if the INSERT fails, logs the kind and error
// to stderr. Bus.Publish fails only when the events INSERT fails, after the
// change it announces is already committed — so we log and continue rather
// than failing the enclosing request.
func (s *Server) publish(ctx context.Context, kind string, payload any) {
	if _, err := s.Bus.Publish(ctx, kind, payload); err != nil {
		w := s.Log
		if w == nil {
			w = os.Stderr
		}
		_, _ = fmt.Fprintf(w, "casebook serve: publish %s: %v\n", kind, err)
	}
}

// settleJob calls Store.Settle for jobID and, when the state changes,
// publishes a "job" event on the bus so watchers see the terminal state.
// Errors are logged to stderr (non-fatal: the step state is already persisted).
func (s *Server) settleJob(ctx context.Context, jobID int64) {
	newState, changed, err := s.Apply.Settle(ctx, jobID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "casebook serve: Settle job %d: %v\n", jobID, err)
		return
	}
	if changed {
		s.publish(ctx, "job", map[string]any{"id": jobID, "state": newState})
	}
}

// startCasebookLane launches the casebook lane for a job at most once at a time
// (a per-job single-flight guard). If a lane is already running for the job the
// call is a no-op. The guard entry is cleared when the lane goroutine returns.
func (s *Server) startCasebookLane(job apply.Job) {
	s.laneMu.Lock()
	if s.laneRun == nil {
		s.laneRun = map[int64]bool{}
	}
	if s.laneRun[job.ID] {
		s.laneMu.Unlock()
		return
	}
	s.laneRun[job.ID] = true
	s.laneMu.Unlock()

	if s.onLaneStart != nil {
		s.onLaneStart(job.ID)
	}

	go func() {
		bgCtx := s.laneCtx()
		env := s.buildEnv()
		pauseFn := func() bool {
			j, err := s.Apply.Get(bgCtx, job.ID)
			return err == nil && j.Paused
		}
		defer func() {
			// Lost-wakeup guard: a resume that arrived while this lane was still
			// registered (laneRun[id]=true) would have been a no-op. After we
			// clear the entry, re-check whether the job has pending casebook
			// steps and is not paused; if so, restart a new lane so those steps
			// are not left stranded.
			if s.onBeforeLaneExit != nil {
				s.onBeforeLaneExit(job.ID)
			}
			s.laneMu.Lock()
			delete(s.laneRun, job.ID)
			s.laneMu.Unlock()
			if j, err := s.Apply.Get(bgCtx, job.ID); err == nil {
				if !j.Paused && (j.State == apply.JobRunning || j.State == apply.JobApproved) {
					for _, st := range j.Steps {
						if st.Lane == apply.LaneCasebook && st.State == apply.StepPending {
							s.startCasebookLane(j)
							break
						}
					}
				}
			}
		}()
		if err := s.Apply.RunCasebookLane(bgCtx, job, s.Runner, env, pauseFn); err != nil {
			fmt.Fprintf(os.Stderr, "casebook serve: RunCasebookLane job %d: %v\n", job.ID, err)
		}
		// After the casebook lane drains, check whether the job has reached a
		// terminal state (all steps done or nothing left to run).
		s.settleJob(bgCtx, job.ID)
	}()
}

// resumeInterruptedJobs relaunches casebook lanes for jobs a previous serve
// left running (or approved with casebook steps). Casebook-lane steps stuck in
// running are requeued to pending; agent-lane steps are left as they are (the
// agent reports them).
func (s *Server) resumeInterruptedJobs(ctx context.Context) error {
	jobs, err := s.Apply.List(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.State != apply.JobRunning && job.State != apply.JobApproved {
			continue
		}
		var hasCasebook bool
		for _, st := range job.Steps {
			if st.Lane == apply.LaneCasebook {
				hasCasebook = true
				break
			}
		}
		if !hasCasebook {
			continue
		}
		if _, err := s.Apply.RequeueRunningCasebookSteps(ctx, job.ID); err != nil {
			return err
		}
		fresh, err := s.Apply.Get(ctx, job.ID)
		if err != nil {
			return err
		}
		s.startCasebookLane(fresh)
	}
	return nil
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
// at once. It reads casebook-data under the store's shared lock (Repo.Read),
// so every caller (a decide, a rule write, a sync, the watch loop) sees one
// whole tree. Rule-error notices are set atomically with the rebuilt result under
// one Index lock (via Index.set), so a concurrent reader never sees a new Head
// without the accompanying notices.
func (s *Server) rebuild(ctx context.Context) error {
	// Bug 2 fix: serialize rebuilds so rule evaluation never overlaps.
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()

	s.rebuilds.Add(1)

	// Build the engine result first (outside any Index lock), from one whole
	// tree: the head, the build and the rules are read under casebook-data's
	// shared lock, so no rebase (serve's push, another process's sync) or
	// write+commit is halfway meanwhile. Mid-rebase the tree is the remote's
	// commits without the local ones: a decision just made would read as
	// undecided, and an active rule would propose it.
	now := s.Now()
	var (
		head     string
		res      engine.Result
		allRules []rules.Rule
		ruleErrs []error
	)
	if err := s.App.Repo.Read(ctx, func() error {
		head = s.repoHead(ctx)
		var err error
		if res, err = s.build(ctx); err != nil {
			return err
		}
		allRules, ruleErrs = s.App.Repo.Rules()
		return nil
	}); err != nil {
		return err
	}

	// Rule-load and validation errors are collected as notices; they never
	// abort the rebuild.
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
	// The index is set: announcing it can no longer fail the rebuild (a
	// caller would report an error for a change that happened).
	s.publish(ctx, "index", map[string]any{"counts": s.Index.Counts(pending), "head": s.Index.Head()})
	if proposed > 0 {
		s.publish(ctx, "rules", map[string]int{"proposed": proposed})
	}
	return nil
}

// checkLeftCrossings detects sessions that have just crossed the left-threshold
// in either direction and publishes one "sessions" event per crossing.
// knownLeft is caller-owned state (a map from session ID to its last-known
// left flag); it is mutated in place. Returns true when an event was emitted.
// Called on each watch tick so no second polling system is needed.
func (s *Server) checkLeftCrossings(ctx context.Context, knownLeft map[string]bool) bool {
	sessions, err := s.Queue.Sessions(ctx)
	if err != nil {
		return false
	}
	currentLeft := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		currentLeft[sess.ID] = sess.Left
	}
	changed := false
	for id, wasLeft := range knownLeft {
		if _, exists := currentLeft[id]; !exists || currentLeft[id] != wasLeft {
			changed = true
			break
		}
	}
	if !changed {
		for id, isLeft := range currentLeft {
			if _, seen := knownLeft[id]; !seen {
				// New session — only matters if it's already left.
				if isLeft {
					changed = true
					break
				}
			}
		}
	}
	// Rebuild knownLeft to match current state.
	for k := range knownLeft {
		delete(knownLeft, k)
	}
	for k, v := range currentLeft {
		knownLeft[k] = v
	}
	if changed {
		s.publish(ctx, "sessions", map[string]any{})
	}
	return changed
}

// watch rebuilds whenever the casebook repo's HEAD moves (a sync or a CLI
// decision elsewhere), and trims the event log daily.
func (s *Server) watch(ctx context.Context) {
	t := time.NewTicker(s.WatchEvery)
	defer t.Stop()
	lastTrim := time.Now()
	knownLeft := map[string]bool{}
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
			s.checkLeftCrossings(ctx, knownLeft)
			if time.Since(lastTrim) > 24*time.Hour {
				_ = s.Bus.Trim(ctx, 7*24*time.Hour)
				if err := s.Props.TrimProgressLog(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "casebook serve: trim progress_log: %v\n", err)
				}
				s.pruneSessions(ctx)
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
	m.HandleFunc("GET /api/decisions/vocabulary", s.getDecisionsVocabulary)
	m.HandleFunc("POST /api/decide", s.postDecide)
	m.HandleFunc("POST /api/proposals/accept", s.postAccept)
	m.HandleFunc("POST /api/proposals/change", s.postChange)
	m.HandleFunc("POST /api/proposals/reject", s.postReject)
	m.HandleFunc("GET /api/sessions", s.getSessions)
	m.HandleFunc("POST /api/sessions/move", s.postMoveSession)
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
	m.HandleFunc("GET /api/session/progress", s.getSessionProgress)
	m.HandleFunc("GET /api/session/delivery", s.getSessionDelivery)
	m.HandleFunc("POST /api/deliveries/release", s.postRelease)
	m.HandleFunc("POST /api/deliveries/move", s.postMoveDelivery)
	m.HandleFunc("POST /api/stop", s.postStop)
	m.HandleFunc("POST /api/sync", s.postSync)
	// agent (casebook channel)
	m.HandleFunc("POST /api/agent/presence", s.agentPresence)
	m.HandleFunc("POST /api/agent/session-info", s.agentSessionInfo)
	m.HandleFunc("GET /api/agent/wait", s.agentWait)
	m.HandleFunc("POST /api/agent/reply", s.agentReply)
	m.HandleFunc("POST /api/agent/propose", s.agentPropose)
	m.HandleFunc("POST /api/agent/evidence", s.agentEvidence)
	m.HandleFunc("POST /api/agent/progress", s.agentProgress)
	m.HandleFunc("GET /api/agent/status", s.agentStatus)
	m.HandleFunc("POST /api/agent/open", s.agentOpen)
	m.HandleFunc("POST /api/agent/settled", s.agentSettled)
	m.HandleFunc("POST /api/agent/rule-draft", s.agentRuleDraft)
	m.HandleFunc("POST /api/agent/job-step", s.agentJobStep)
	m.HandleFunc("POST /api/agent/job-ask", s.agentJobAsk)
	// apply
	m.HandleFunc("POST /api/apply/plan", s.postApplyPlan)
	m.HandleFunc("POST /api/apply/approve", s.postApplyApprove)
	m.HandleFunc("POST /api/apply/cancel", s.postApplyCancel)
	m.HandleFunc("GET /api/jobs", s.getJobs)
	m.HandleFunc("GET /api/job", s.getJob)
	m.HandleFunc("GET /api/needs-you", s.getNeedsYou)
	m.HandleFunc("POST /api/jobs/pause", s.postJobsPause)
	m.HandleFunc("POST /api/jobs/resume", s.postJobsResume)
	m.HandleFunc("POST /api/jobs/undo", s.postJobsUndo)
	m.HandleFunc("POST /api/jobs/answer", s.postJobsAnswer)
	// rules
	m.HandleFunc("GET /api/rules", s.getRules)
	m.HandleFunc("GET /api/rule", s.getRule)
	m.HandleFunc("POST /api/rules/draft", s.postRulesDraft)
	m.HandleFunc("POST /api/rules/preview", s.postRulesPreview)
	m.HandleFunc("POST /api/rules/exclude", s.postRulesExclude)
	m.HandleFunc("POST /api/rules/include", s.postRulesInclude)
	m.HandleFunc("POST /api/rules/propose-once", s.postRulesProposeOnce)
	m.HandleFunc("POST /api/rules/activate", s.postRulesActivate)
	m.HandleFunc("POST /api/rules/deactivate", s.postRulesDeactivate)
	m.HandleFunc("GET /api/rules/vocabulary", s.getRulesVocabulary)
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
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if v == nil {
		v = map[string]bool{"ok": true}
	}
	_ = json.NewEncoder(w).Encode(v)
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
	defer func() { _ = d.Close() }()
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
	srv, err := localweb.Start(ctx, localweb.Config{Tool: config.Tool, Assets: page.With(assetsFS), API: s.Handler(), Port: o.Port})
	if err != nil {
		return err
	}
	// Reopened records that a tab was connected when the previous serve went
	// away, whether or not a Ready callback is set to open the page again.
	adv := Advert{URL: srv.URL, Base: "http://" + srv.Addr(), Token: srv.Token, PID: os.Getpid(), Version: o.Version, StartedAt: time.Now().UTC(),
		Reopened: wasOpen}
	if o.Open != nil {
		// Guard the write: agentOpen reads openPage concurrently once the
		// handler is registered (localweb.Start already began serving above).
		s.mu.Lock()
		s.openPage = func(suffix string) error { return o.Open(srv.URL + suffix) }
		s.mu.Unlock()
	}
	if err := writeAdvert(adv); err != nil {
		cancel()
		return err
	}
	defer removeAdvert(adv.PID)
	_, _ = fmt.Fprintf(o.Log, "casebook serve: %s (pid %d)\n", adv.Base, adv.PID)
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
	cancel()          // serve is stopping: a push in flight ends, its commits stay queued
	s.stopPushes()    // and none starts now (a handler still running may ask)
	s.pushWG.Wait()   // the pusher writes events: the database closes after it
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
	_ = resp.Body.Close()
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
	defer func() { _ = logf.Close() }()
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

// SessionRetention is how long serve keeps an exited session that nothing
// refers to before pruning it: long enough that a session that left for a
// weekend is still there to come back to.
const SessionRetention = 7 * 24 * time.Hour

// pruneSessions drops long-gone sessions nothing refers to
// (deliver.Queue.Prune), at start and daily. The rule drafts' authors are
// references Prune can't see (they live in the casebook repo); a rule file
// serve can't read might name one, so with any unreadable rule nothing is
// pruned this time. A failure is a diagnostic: the table just keeps them.
func (s *Server) pruneSessions(ctx context.Context) {
	all, errs := s.App.Repo.Rules()
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "casebook serve: prune sessions skipped: %d rule(s) unreadable\n", len(errs))
		return
	}
	var spare []string
	for _, r := range all {
		if _, id, ok := strings.Cut(r.CreatedBy, ":"); ok && id != "" {
			spare = append(spare, id)
		}
	}
	n, err := s.Queue.Prune(ctx, SessionRetention, spare...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "casebook serve: prune sessions: %v\n", err)
		return
	}
	if n > 0 {
		s.publish(ctx, "sessions", map[string]int{"pruned": n})
	}
}

// SessionQuery is the page URL's query addition that attaches the page to
// a session: "&session=<id>" (the page URL already carries ?t=<token>;
// localweb keeps other parameters through the token exchange), or "" for
// none.
func SessionQuery(id string) string {
	if id == "" {
		return ""
	}
	return "&session=" + url.QueryEscape(id)
}
