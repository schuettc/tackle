package serve

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schuettc/tackle/internal/cull/store"
	"github.com/schuettc/tools-common/localweb"
	"github.com/schuettc/tools-common/localweb/page"
)

//go:embed assets
var assets embed.FS

// Server is cull's API over the store.
type Server struct {
	st *store.Store

	events       eventLog
	activity     atomic.Int64 // unix ms of the last API request
	streamWG     sync.WaitGroup
	streams      atomic.Int32 // open event streams
	writeTimeout time.Duration

	mu   sync.Mutex
	life context.Context // Run's context; nil outside Run
	stop context.CancelFunc
}

// New returns a Server over st.
func New(st *store.Store) *Server {
	s := &Server{st: st}
	s.events.init()
	s.activity.Store(time.Now().UnixMilli())
	return s
}

// Options configures Run.
type Options struct {
	Port      int           // 0: remembered, else free
	Idle      time.Duration // shut down after this long with no API traffic (0: never)
	Version   string
	Log       io.Writer // one line per start/stop
	Ready     func(url string)
	PollEvery time.Duration // run-change poll; default 2s
}

// Run serves until ctx ends, /api/stop, or the idle limit. It writes the
// advert while running and removes it on the way out.
func Run(ctx context.Context, st *store.Store, o Options) error {
	if o.Log == nil {
		o.Log = io.Discard
	}
	if o.PollEvery <= 0 {
		o.PollEvery = 2 * time.Second
	}
	if adv, err := Running(); err == nil {
		return fmt.Errorf("cull serve is already running (pid %d, %s)", adv.PID, adv.Base)
	}
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := New(st)
	s.mu.Lock()
	s.life, s.stop = ctx, cancel
	s.mu.Unlock()
	assetsFS, err := fs.Sub(assets, "assets")
	if err != nil {
		return err
	}
	srv, err := localweb.Start(ctx, localweb.Config{Tool: "cull", Assets: page.With(assetsFS), API: s.Handler(), Port: o.Port})
	if err != nil {
		return err
	}
	adv := Advert{URL: srv.URL, Base: "http://" + srv.Addr(), Token: srv.Token, PID: os.Getpid(), Version: o.Version, StartedAt: time.Now().UTC()}
	if err := writeAdvert(adv); err != nil {
		cancel()
		_ = srv.Wait()
		return err
	}
	defer removeAdvert(adv.PID)
	_, _ = fmt.Fprintf(o.Log, "cull serve: started %s (pid %d)\n", adv.Base, adv.PID)
	defer func() { _, _ = fmt.Fprintf(o.Log, "cull serve: stopped (pid %d)\n", adv.PID) }()
	go s.Watch(ctx, o.PollEvery)
	if o.Idle > 0 {
		go s.idle(ctx, o.Idle)
	}
	if o.Ready != nil {
		o.Ready(srv.URL)
	}
	err = srv.Wait()
	cancel()
	s.streamWG.Wait() // streams end with ctx
	return err
}

func (s *Server) idle(ctx context.Context, limit time.Duration) {
	every := min(max(limit/4, 5*time.Millisecond), time.Minute)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(time.UnixMilli(s.activity.Load())) > limit {
				s.stopNow()
				return
			}
		}
	}
}

func (s *Server) stopNow() {
	s.mu.Lock()
	stop := s.stop
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// Watch polls the store every `every` for new runs and emits a run event for
// each project whose latest run id changed (the first read only records).
func (s *Server) Watch(ctx context.Context, every time.Duration) {
	var last map[int64]int64
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		cur, err := s.st.LatestRunIDs(ctx)
		if err == nil {
			if last != nil {
				for p, id := range cur {
					if last[p] != id {
						s.events.emit("run", map[string]int64{"project": p, "run": id})
					}
				}
			}
			last = cur
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
