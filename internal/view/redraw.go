package view

import (
	"sync"
	"time"
)

// DefaultRedrawInterval is the coalescing window for pane repaints (~30 fps).
// A pane's OnNeedRedraw hook fires once per ingested PTY chunk; argus's worker
// PTY delivers output in <=4 KiB readLoop chunks and re-emits its entire screen
// (often its whole scrollback) on a SIGWINCH resize, so a single pane-entry can
// drive dozens-to-hundreds of chunks back-to-back. Drawing per chunk paints
// partial, un-settled emulator frames (the "scroll-through history + garbled
// cells" regression). 33ms lets the consumer goroutine drain a burst into the
// emulator between draws so each painted frame is settled, while staying fast
// enough to feel live for echoed keystrokes and autonomous output.
const DefaultRedrawInterval = 33 * time.Millisecond

// redrawCoalescer rate-limits pane repaints. Schedule marks the surface dirty
// (non-blocking, callable from any goroutine — including the terminalpane
// consume goroutine); a single ticker goroutine flushes at most one draw per
// interval, and only when something is dirty.
//
// This mirrors argus's own task-terminal repaint cadence (internal/tui/app.go's
// spinnerLoop: a 100ms ticker that QueueUpdateDraws only when there's live
// work) rather than argus's plugin-pane path (one QueueUpdateDraw per chunk).
// Hera's panes render a chatty PTY with scrollback — the task-terminal workload,
// not the discrete-full-frame plugin workload — so the task-terminal cadence is
// the correct mirror.
//
// Coalescing logic (Schedule + drawIfDirty) is decoupled from timing (the
// ticker goroutine started by start) so it can be unit-tested deterministically
// without sleeping: call Schedule N times, then drawIfDirty once, and exactly
// one draw fires.
type redrawCoalescer struct {
	draw     func()
	interval time.Duration

	mu    sync.Mutex
	dirty bool

	stopOnce sync.Once
	stop     chan struct{}
}

// newRedrawCoalescer builds a coalescer that calls draw (typically a
// tview.QueueUpdateDraw bounce) at most once per interval when dirty. A nil
// draw or non-positive interval yields a usable but inert coalescer (Schedule
// is a no-op-ish flag flip; drawIfDirty never calls a nil draw) so tests and
// the no-session daemon path stay safe.
func newRedrawCoalescer(draw func(), interval time.Duration) *redrawCoalescer {
	if interval <= 0 {
		interval = DefaultRedrawInterval
	}
	return &redrawCoalescer{
		draw:     draw,
		interval: interval,
		stop:     make(chan struct{}),
	}
}

// Schedule marks the surface dirty so the next tick flushes a draw. Safe to
// call from any goroutine; never blocks on the event loop.
func (c *redrawCoalescer) Schedule() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.dirty = true
	c.mu.Unlock()
}

// drawIfDirty flushes one draw if the surface is dirty since the last flush,
// and reports whether it drew. The dirty flag is cleared BEFORE draw runs (and
// the lock is released before calling draw, which may block on the event loop)
// so a chunk that lands during the draw re-arms the flag for the next tick
// rather than being lost.
func (c *redrawCoalescer) drawIfDirty() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	dirty := c.dirty
	c.dirty = false
	c.mu.Unlock()
	if !dirty || c.draw == nil {
		return false
	}
	c.draw()
	return true
}

// start launches the ticker goroutine that flushes coalesced draws. Idempotent
// callers should call it once; the goroutine runs until Stop. No-op when draw
// is nil (nothing to flush).
func (c *redrawCoalescer) start() {
	if c == nil || c.draw == nil {
		return
	}
	go c.loop()
}

func (c *redrawCoalescer) loop() {
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			// No final flush on teardown: draw() bounces through
			// tview.QueueUpdateDraw, which can block once the event loop has
			// stopped (Close tears down the app). A missed final paint at
			// shutdown is harmless; a leaked goroutine blocked on a dead event
			// loop is not.
			return
		case <-t.C:
			c.drawIfDirty()
		}
	}
}

// Stop halts the ticker goroutine. Idempotent; safe to call when start was
// never invoked (the goroutine simply was never spawned, so done never closes —
// guard the wait on whether we started).
func (c *redrawCoalescer) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() { close(c.stop) })
}
