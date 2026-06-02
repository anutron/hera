package view

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRedrawCoalescer_BurstCoalescesToSingleDraw is the core coalescing
// guarantee: many Schedule calls between flushes collapse into exactly one
// draw. This is the seam that fixes the regression — a snapshot blob or a
// chatty burst arrives as N chunks (N Schedules) but paints one settled frame.
func TestRedrawCoalescer_BurstCoalescesToSingleDraw(t *testing.T) {
	var draws atomic.Int64
	c := newRedrawCoalescer(func() { draws.Add(1) }, DefaultRedrawInterval)

	// Simulate a burst of 200 ingested chunks with no flush in between.
	for i := 0; i < 200; i++ {
		c.Schedule()
	}

	// A single flush paints the settled frame exactly once.
	if drew := c.drawIfDirty(); !drew {
		t.Fatal("drawIfDirty: expected a draw after a burst of Schedule calls")
	}
	if got := draws.Load(); got != 1 {
		t.Fatalf("burst of 200 chunks: expected exactly 1 draw, got %d", got)
	}

	// No further dirty state: a second flush is a no-op (no wasted repaint).
	if drew := c.drawIfDirty(); drew {
		t.Fatal("drawIfDirty: expected no draw when nothing was scheduled since the last flush")
	}
	if got := draws.Load(); got != 1 {
		t.Fatalf("idle flush should not draw: expected 1 total, got %d", got)
	}
}

// TestRedrawCoalescer_SnapshotIngestSchedulesSingleSettledDraw models pane
// bind: the settled snapshot is fed and the latest output is painted once at
// the tail, not once per intermediate chunk.
func TestRedrawCoalescer_SnapshotIngestSchedulesSingleSettledDraw(t *testing.T) {
	var draws atomic.Int64
	c := newRedrawCoalescer(func() { draws.Add(1) }, DefaultRedrawInterval)

	// The snapshot is delivered as one chunk today, but even if it were split
	// across several, the flush must coalesce to a single settled draw.
	c.Schedule() // snapshot chunk
	c.Schedule() // (defensive) a follow-on chunk in the same window

	c.drawIfDirty()
	if got := draws.Load(); got != 1 {
		t.Fatalf("snapshot ingest: expected a single settled draw, got %d", got)
	}
}

// TestRedrawCoalescer_EachWindowDrawsOnce verifies a continuous stream still
// paints — one draw per flush window — rather than starving (the failure mode
// of a pure trailing debounce that resets on every chunk).
func TestRedrawCoalescer_EachWindowDrawsOnce(t *testing.T) {
	var draws atomic.Int64
	c := newRedrawCoalescer(func() { draws.Add(1) }, DefaultRedrawInterval)

	// Three windows, each with several chunks, each flushed once.
	for window := 0; window < 3; window++ {
		c.Schedule()
		c.Schedule()
		c.drawIfDirty()
	}
	if got := draws.Load(); got != 3 {
		t.Fatalf("3 flush windows each with output: expected 3 draws, got %d", got)
	}
}

// TestRedrawCoalescer_DirtyClearedBeforeDraw guards against losing a chunk that
// lands while a draw is in flight: the flag must be cleared before draw runs so
// a concurrent Schedule re-arms it for the next flush.
func TestRedrawCoalescer_DirtyClearedBeforeDraw(t *testing.T) {
	var draws atomic.Int64
	var c *redrawCoalescer
	c = newRedrawCoalescer(func() {
		draws.Add(1)
		// A chunk lands during the draw — must survive to the next flush.
		if draws.Load() == 1 {
			c.Schedule()
		}
	}, DefaultRedrawInterval)

	c.Schedule()
	c.drawIfDirty() // draw #1, re-arms dirty from inside
	if drew := c.drawIfDirty(); !drew {
		t.Fatal("a Schedule during the draw must re-arm dirty for the next flush")
	}
	if got := draws.Load(); got != 2 {
		t.Fatalf("expected 2 draws (the in-flight chunk painted on the next flush), got %d", got)
	}
}

// TestRedrawCoalescer_TickerLoopFlushes exercises the production timing path:
// start spins the ticker goroutine, Schedule arms it, and a draw lands within a
// bounded wait. Uses a short interval to keep the test fast.
func TestRedrawCoalescer_TickerLoopFlushes(t *testing.T) {
	var mu sync.Mutex
	var draws int
	c := newRedrawCoalescer(func() {
		mu.Lock()
		draws++
		mu.Unlock()
	}, 5*time.Millisecond)
	c.start()
	defer c.Stop()

	c.Schedule()

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		mu.Lock()
		n := draws
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ticker loop never flushed a scheduled draw")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRedrawCoalescer_NilSafe verifies the nil-coalescer and nil-draw paths
// used by tests and the no-session daemon path don't panic.
func TestRedrawCoalescer_NilSafe(t *testing.T) {
	var c *redrawCoalescer
	c.Schedule()        // must not panic
	c.Stop()            // must not panic
	_ = c.drawIfDirty() // must not panic

	inert := newRedrawCoalescer(nil, DefaultRedrawInterval)
	inert.Schedule()
	inert.start() // no goroutine when draw is nil
	if drew := inert.drawIfDirty(); drew {
		t.Fatal("a nil-draw coalescer must report no draw")
	}
	inert.Stop()
}
