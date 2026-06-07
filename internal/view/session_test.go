package view

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gdamore/tcell/v2"
)

// newTestSimScreen returns a tcell SimulationScreen initialised to the given
// dimensions. Callers must call Fini() when done.
func newTestSimScreen(t *testing.T, cols, rows int) tcell.SimulationScreen {
	t.Helper()
	scr := tcell.NewSimulationScreen("")
	if err := scr.Init(); err != nil {
		t.Fatalf("SimulationScreen.Init: %v", err)
	}
	scr.SetSize(cols, rows)
	return scr
}

// TestMakeViewportGuard_SkipsSentinelSize verifies that the viewport guard
// returns true (skip draw) for small sentinel viewports and false (allow draw)
// once a real-sized viewport is seen (BUG-049).
func TestMakeViewportGuard_SkipsSentinelSize(t *testing.T) {
	guard := makeViewportGuard(nil)

	sentinel := newTestSimScreen(t, 13, 8)
	defer sentinel.Fini()

	// Sentinel-sized screen: guard must skip the draw.
	if !guard(sentinel) {
		t.Error("guard must return true (skip) at sentinel size 13×8")
	}

	// A second sentinel-sized call: still skipping.
	if !guard(sentinel) {
		t.Error("guard must continue returning true (skip) at sentinel size before seeing real viewport")
	}

	real := newTestSimScreen(t, 120, 40)
	defer real.Fini()

	// Real-sized screen: guard must allow the draw (return false).
	if guard(real) {
		t.Error("guard must return false (allow) at real size 120×40")
	}

	// After seeing a real viewport the guard is one-shot — subsequent draws at
	// any size, including sentinel size, must be allowed.
	if guard(sentinel) {
		t.Error("guard must return false (allow) for all draws after first real-viewport draw")
	}
	if guard(real) {
		t.Error("guard must return false (allow) for repeated real-viewport draws")
	}
}

// TestMakeViewportGuard_BoundaryExactly verifies that a viewport exactly at
// the minimum threshold (minPluginViewCols × minPluginViewRows) is still
// treated as a sentinel (≤ not >).
func TestMakeViewportGuard_BoundaryExactly(t *testing.T) {
	guard := makeViewportGuard(nil)

	boundary := newTestSimScreen(t, minPluginViewCols, minPluginViewRows)
	defer boundary.Fini()

	// Exactly at the minimum: must still be treated as sentinel.
	if !guard(boundary) {
		t.Errorf("guard must skip draws at boundary size %d×%d (threshold is strictly greater-than)",
			minPluginViewCols, minPluginViewRows)
	}

	// One column over: must be allowed.
	oneOver := newTestSimScreen(t, minPluginViewCols+1, minPluginViewRows+1)
	defer oneOver.Fini()

	if guard(oneOver) {
		t.Errorf("guard must allow draws at size %d×%d (one over threshold)",
			minPluginViewCols+1, minPluginViewRows+1)
	}
}

// TestMakeViewportGuard_OnFirstRealCallback verifies that onFirstReal is
// called exactly once — when the guard first transitions from "skip" to
// "allow" at a real viewport size — and never again on subsequent draws
// (BUG-049 take 2).
func TestMakeViewportGuard_OnFirstRealCallback(t *testing.T) {
	var calls atomic.Int32
	called := make(chan struct{}, 1)
	guard := makeViewportGuard(func() {
		calls.Add(1)
		select {
		case called <- struct{}{}:
		default:
		}
	})

	sentinel := newTestSimScreen(t, 13, 8)
	defer sentinel.Fini()
	guard(sentinel) // skip — callback must not fire

	real := newTestSimScreen(t, 120, 40)
	defer real.Fini()
	guard(real) // first real: callback fires in goroutine

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("onFirstReal not called within 1s of first real-viewport draw")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("onFirstReal called %d times; want 1", got)
	}

	// Subsequent draws (real or sentinel) must not retrigger the callback.
	guard(real)
	guard(sentinel)
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Errorf("onFirstReal called %d times after subsequent draws; want still 1", got)
	}
}

// TestSession_RailRefresherSubscribedAndStoppedOnClose asserts that a
// live websocket session subscribes to the DAO broadcaster (so DAO
// writes drive rail refreshes) and tears the subscription down when
// the session closes.
func TestSession_RailRefresherSubscribedAndStoppedOnClose(t *testing.T) {
	d := openTestDB(t)
	runner := NewSessionFunc(d, nil, nil, nil, slog.Default())
	srv := NewServer(nil, runner)
	t.Cleanup(srv.Stop)

	_, wsURL := newTestHTTPServer(t, srv)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()

	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	// Wait for the runner to bootstrap (NewRailRefresher.Subscribe).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.Events.SubscriberCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := d.Events.SubscriberCount(); got < 1 {
		t.Fatalf("no broadcaster subscribers after session bootstrap; want >= 1")
	}

	// Close the conn to trigger the runner's teardown chain (rail.Stop
	// unsubscribes from the broadcaster).
	_ = conn.CloseNow()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.Events.SubscriberCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("rail refresher not unsubscribed after session close; subs = %d", d.Events.SubscriberCount())
}
