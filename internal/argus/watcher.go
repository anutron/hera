package argus

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultWatcherInterval is the polling cadence used by the daemon. Tests
// override the field directly with a much smaller value.
const DefaultWatcherInterval = 1 * time.Second

// Watcher polls argus's pid file mtime and socket ping on a fixed interval
// and invokes OnRestart whenever either signal indicates a restart. Concurrent
// triggers are coalesced via single-flight: while a callback is in flight,
// further polling ticks do not invoke OnRestart again. The next tick after
// the callback completes re-checks both signals.
//
// Field order and names follow the spec's struct sketch so daemon wiring
// can populate the struct literally.
type Watcher struct {
	PidPath   string
	Ping      func(ctx context.Context) error
	Interval  time.Duration
	OnRestart func(context.Context)
	Log       *slog.Logger

	stop     chan struct{}
	wg       sync.WaitGroup
	inflight atomic.Bool

	once sync.Once
}

// Start launches the polling goroutine. It returns immediately. Calling
// Start more than once is a no-op past the first call.
//
// ctx bounds the lifetime of the polling loop AND every restart callback
// it spawns. Cancelling ctx stops the loop and propagates cancellation
// to any in-flight OnRestart invocation.
func (w *Watcher) Start(ctx context.Context) {
	w.once.Do(func() {
		w.stop = make(chan struct{})
		interval := w.Interval
		if interval <= 0 {
			interval = DefaultWatcherInterval
		}

		w.wg.Add(1)
		go w.loop(ctx, interval)
	})
}

// Stop signals the polling loop to exit and waits for it (and any in-flight
// callback) to finish, bounded by ctx. Safe to call more than once.
func (w *Watcher) Stop(ctx context.Context) {
	if w.stop == nil {
		return
	}
	// Idempotent close — guard against double-Stop.
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// Caller's deadline elapsed before the loop drained. Recovery
		// goroutines (if any) keep running with the original ctx; they
		// will finish or be cancelled by that ctx's own cancellation.
	}
}

// loop drives the polling. Baseline mtime is captured before the first
// tick so the watcher does not fire a spurious restart on startup just
// because the pid file already exists.
func (w *Watcher) loop(ctx context.Context, interval time.Duration) {
	defer w.wg.Done()

	var lastMtime time.Time
	if fi, err := os.Stat(w.PidPath); err == nil {
		lastMtime = fi.ModTime()
	} else if w.Log != nil {
		w.Log.Debug("watcher: initial pid stat failed", "path", w.PidPath, "err", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			lastMtime = w.tick(ctx, lastMtime)
		}
	}
}

// tick runs one pass over the two restart signals. It returns the mtime
// that should be carried into the next iteration so a stat error this
// tick does not lose the baseline for the next.
func (w *Watcher) tick(ctx context.Context, lastMtime time.Time) time.Time {
	trigger := false

	if fi, err := os.Stat(w.PidPath); err == nil {
		if !fi.ModTime().Equal(lastMtime) {
			trigger = true
			lastMtime = fi.ModTime()
		}
	} else if w.Log != nil {
		w.Log.Debug("watcher: pid stat failed", "path", w.PidPath, "err", err)
	}

	if err := w.Ping(ctx); err != nil {
		trigger = true
		if w.Log != nil {
			w.Log.Debug("watcher: ping failed", "err", err)
		}
	}

	if !trigger {
		return lastMtime
	}
	if !w.inflight.CompareAndSwap(false, true) {
		// A previous OnRestart is still running; suppress this tick.
		return lastMtime
	}

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer w.inflight.Store(false)
		w.OnRestart(ctx)
	}()
	return lastMtime
}
