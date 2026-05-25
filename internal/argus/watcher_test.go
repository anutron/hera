package argus

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writePid creates a pid file with deterministic mtime.
func writePid(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("1234\n"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestWatcher_PidMtimeChange_FiresRestart(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")
	writePid(t, pid, time.Now().Add(-time.Hour))

	var calls atomic.Int32
	w := &Watcher{
		PidPath:   pid,
		Ping:      func(ctx context.Context) error { return nil },
		Interval:  5 * time.Millisecond,
		OnRestart: func(ctx context.Context) { calls.Add(1) },
		Log:       discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Allow several ticks at the steady-state baseline; nothing should
	// fire because mtime is unchanged and ping is healthy.
	time.Sleep(40 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("watcher fired unexpectedly at baseline: %d invocations", got)
	}

	// Bump mtime to simulate argus rewriting the pid file on restart.
	bump := time.Now().Add(time.Hour)
	if err := os.Chtimes(pid, bump, bump); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got == 0 {
		t.Fatalf("watcher did not fire on mtime change")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	w.Stop(stopCtx)
}

func TestWatcher_PingFailure_FiresRestart(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")
	writePid(t, pid, time.Now())

	var pingShouldFail atomic.Bool
	var calls atomic.Int32

	w := &Watcher{
		PidPath: pid,
		Ping: func(ctx context.Context) error {
			if pingShouldFail.Load() {
				return errors.New("ping refused")
			}
			return nil
		},
		Interval:  5 * time.Millisecond,
		OnRestart: func(ctx context.Context) { calls.Add(1) },
		Log:       discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	time.Sleep(40 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("watcher fired while healthy: %d", got)
	}

	pingShouldFail.Store(true)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got == 0 {
		t.Fatalf("watcher did not fire on ping failure")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	w.Stop(stopCtx)
}

// TestWatcher_SingleFlight_SuppressesConcurrentTriggers verifies that
// while a restart callback is in flight, additional polling-tick triggers
// are suppressed. The blocking callback simulates a slow Recover routine.
func TestWatcher_SingleFlight_SuppressesConcurrentTriggers(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")
	writePid(t, pid, time.Now())

	var calls atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})

	w := &Watcher{
		PidPath: pid,
		// Ping always errors so every tick presents a restart signal.
		Ping:     func(ctx context.Context) error { return errors.New("down") },
		Interval: 5 * time.Millisecond,
		OnRestart: func(ctx context.Context) {
			calls.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		},
		Log: discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Block until the first callback enters.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher never invoked first callback")
	}

	// While the first callback is held, let many polling ticks pass.
	// No additional invocation should occur — single-flight contract.
	time.Sleep(80 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("single-flight violated: %d invocations while in-flight", got)
	}

	close(release)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	w.Stop(stopCtx)
}

// TestWatcher_Stop_WaitsForInFlight asserts that Stop returns only after
// any in-flight callback has completed (within the supplied deadline).
func TestWatcher_Stop_WaitsForInFlight(t *testing.T) {
	dir := t.TempDir()
	pid := filepath.Join(dir, "daemon.pid")
	writePid(t, pid, time.Now())

	var (
		releaseOnce sync.Once
		release     = make(chan struct{})
		entered     = make(chan struct{}, 1)
		exited      atomic.Bool
	)

	w := &Watcher{
		PidPath:  pid,
		Ping:     func(ctx context.Context) error { return errors.New("down") },
		Interval: 5 * time.Millisecond,
		OnRestart: func(ctx context.Context) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			exited.Store(true)
		},
		Log: discardLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("callback never entered")
	}

	stopDone := make(chan struct{})
	go func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		w.Stop(stopCtx)
		close(stopDone)
	}()

	// Stop should not yet have returned because the callback is held.
	select {
	case <-stopDone:
		t.Fatal("Stop returned before the in-flight callback finished")
	case <-time.After(30 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })

	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after callback released")
	}
	if !exited.Load() {
		t.Fatal("callback did not run to completion")
	}
}
