package view

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// fakeArgusLister is a minimal argusLister for cache tests.
type fakeArgusLister struct {
	tasks []argus.Task
	err   error
}

func (f *fakeArgusLister) ListTasksAll(_ context.Context) ([]argus.Task, error) {
	return f.tasks, f.err
}

func newTestCache(tasks []argus.Task) *ArgusStateCache {
	lister := &fakeArgusLister{tasks: tasks}
	c := NewArgusStateCache(lister, time.Second, nil)
	c.poll(context.Background())
	return c
}

// --- Snapshot freshness (BUG-002) ---

func TestArgusStateCache_Fresh_TrueRightAfterSuccessfulPoll(t *testing.T) {
	c := newTestCache([]argus.Task{{ID: "T1", Status: "in_progress"}})
	if !c.Fresh() {
		t.Fatal("Fresh must be true immediately after a successful poll")
	}
}

func TestArgusStateCache_Fresh_FalseBeforeFirstPoll(t *testing.T) {
	c := NewArgusStateCache(&fakeArgusLister{}, time.Second, nil)
	if c.Fresh() {
		t.Fatal("Fresh must be false before any successful poll (cold cache)")
	}
}

// The headline BUG-002 condition at the cache layer: polling stops succeeding,
// the frozen snapshot is retained, Ready stays latched true — but Fresh must
// flip false once the staleness window elapses with no fresh success.
func TestArgusStateCache_Fresh_FalseWhenPollsStopSucceeding(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	lister := &fakeArgusLister{tasks: []argus.Task{{ID: "T1", Status: "in_progress"}}}
	c := NewArgusStateCache(lister, time.Second, nil)
	c.now = func() time.Time { return base }

	c.poll(context.Background()) // success at base
	if !c.Fresh() {
		t.Fatal("Fresh must be true right after the successful poll")
	}

	// Argus is now down: polls fail and the snapshot freezes.
	lister.err = errors.New("argus unreachable")
	c.now = func() time.Time { return base.Add(c.staleAfter + time.Second) }
	c.poll(context.Background()) // fails, retains the frozen snapshot

	if !c.Ready() {
		t.Fatal("Ready must stay latched true after a failed poll")
	}
	if c.Fresh() {
		t.Fatal("Fresh must be false once the staleness window elapses with no fresh success")
	}
	// The frozen snapshot is still readable — a stale cache is not an empty one.
	if _, ok := c.Get("T1"); !ok {
		t.Fatal("stale cache must still serve its last good snapshot")
	}
}

// A brief blip (one fast-failing poll well within the window) must NOT flip the
// cache stale — the staleness tolerance exists precisely to ride out blips.
func TestArgusStateCache_Fresh_TolerantOfBriefBlip(t *testing.T) {
	base := time.Unix(2_000_000, 0)
	lister := &fakeArgusLister{tasks: []argus.Task{{ID: "T1", Status: "in_progress"}}}
	c := NewArgusStateCache(lister, time.Second, nil)
	c.now = func() time.Time { return base }
	c.poll(context.Background())

	lister.err = errors.New("transient")
	c.now = func() time.Time { return base.Add(c.staleAfter / 2) } // within the window
	c.poll(context.Background())

	if !c.Fresh() {
		t.Fatal("a brief blip within the staleness window must keep the cache fresh")
	}
}

// --- Optimistic overlay (BUG-032) ---

func TestArgusStateCache_Get_NoOptimistic_ReturnsPolled(t *testing.T) {
	c := newTestCache([]argus.Task{
		{ID: "T1", Status: "pending"},
	})
	st, ok := c.Get("T1")
	if !ok {
		t.Fatal("Get: task not found")
	}
	if st.Status != "pending" {
		t.Fatalf("Get: want status %q, got %q", "pending", st.Status)
	}
}

func TestArgusStateCache_SetOptimistic_OverridesStatus(t *testing.T) {
	c := newTestCache([]argus.Task{
		{ID: "T1", Status: "pending"},
	})
	c.SetOptimistic("T1", "in_progress")

	st, ok := c.Get("T1")
	if !ok {
		t.Fatal("Get: task not found after SetOptimistic")
	}
	if st.Status != "in_progress" {
		t.Fatalf("Get: want optimistic status %q, got %q", "in_progress", st.Status)
	}
}

func TestArgusStateCache_SetOptimistic_OnlyOverridesStatus(t *testing.T) {
	// Other fields (Idle, NeedsInput, Archived) must come from the polled state,
	// not be fabricated by the optimistic overlay.
	c := newTestCache([]argus.Task{
		{ID: "T1", Status: "pending", Idle: true, NeedsInput: false, Archived: false},
	})
	c.SetOptimistic("T1", "in_progress")

	st, ok := c.Get("T1")
	if !ok {
		t.Fatal("Get after SetOptimistic: task not found")
	}
	if st.Status != "in_progress" {
		t.Fatalf("Status: want in_progress, got %q", st.Status)
	}
	if !st.Idle {
		t.Fatal("Idle must still reflect polled value (true)")
	}
}

func TestArgusStateCache_ClearOptimistic_RestoresPolled(t *testing.T) {
	c := newTestCache([]argus.Task{
		{ID: "T1", Status: "pending"},
	})
	c.SetOptimistic("T1", "in_progress")
	c.ClearOptimistic("T1")

	st, ok := c.Get("T1")
	if !ok {
		t.Fatal("Get after ClearOptimistic: task not found")
	}
	if st.Status != "pending" {
		t.Fatalf("Get: want polled status %q after clear, got %q", "pending", st.Status)
	}
}

func TestArgusStateCache_SetOptimistic_EmptyTaskID_NoOp(t *testing.T) {
	c := newTestCache(nil)
	c.SetOptimistic("", "in_progress") // must not panic or store anything
	c.ClearOptimistic("")              // must not panic
}

func TestArgusStateCache_SetOptimistic_UnknownTask_NotAffectingGet(t *testing.T) {
	c := newTestCache([]argus.Task{
		{ID: "T1", Status: "pending"},
	})
	// Setting optimistic for an unknown task should not make Get return ok=true
	// for a task that was never polled.
	c.SetOptimistic("UNKNOWN", "in_progress")

	_, ok := c.Get("UNKNOWN")
	if ok {
		t.Fatal("Get for unknown task must return ok=false even with optimistic set")
	}
}

func TestArgusStateCache_Poll_AutoClearsConfirmedOptimistic(t *testing.T) {
	lister := &fakeArgusLister{tasks: []argus.Task{
		{ID: "T1", Status: "pending"},
	}}
	c := NewArgusStateCache(lister, time.Second, nil)
	c.poll(context.Background())

	// Apply optimistic: "pending" → "in_progress".
	c.SetOptimistic("T1", "in_progress")
	if st, _ := c.Get("T1"); st.Status != "in_progress" {
		t.Fatalf("before poll confirm: want in_progress, got %q", st.Status)
	}

	// Simulate argus confirming the write: next poll sees "in_progress".
	lister.tasks = []argus.Task{{ID: "T1", Status: "in_progress"}}
	c.poll(context.Background())

	// After confirmation, the optimistic entry is auto-cleared; Get returns the
	// polled value (same, so no visible change, but the map is compact).
	st, ok := c.Get("T1")
	if !ok {
		t.Fatal("Get after confirming poll: task not found")
	}
	if st.Status != "in_progress" {
		t.Fatalf("Get: want in_progress, got %q", st.Status)
	}
	// Confirm the map is now empty (optimistic cleared).
	c.optMu.RLock()
	remaining := len(c.optimistic)
	c.optMu.RUnlock()
	if remaining != 0 {
		t.Fatalf("optimistic map must be empty after poll confirms; got %d entries", remaining)
	}
}

func TestArgusStateCache_Poll_KeepsOptimisticWhileUnconfirmed(t *testing.T) {
	lister := &fakeArgusLister{tasks: []argus.Task{
		{ID: "T1", Status: "pending"},
	}}
	c := NewArgusStateCache(lister, time.Second, nil)
	c.poll(context.Background())

	c.SetOptimistic("T1", "in_progress")

	// Argus still reports "pending" (write in flight or failed).
	// Poll must NOT clear the optimistic — the write may still be in flight.
	lister.tasks = []argus.Task{{ID: "T1", Status: "pending"}}
	c.poll(context.Background())

	st, _ := c.Get("T1")
	if st.Status != "in_progress" {
		t.Fatalf("Get: optimistic must remain while unconfirmed; got %q", st.Status)
	}
	c.optMu.RLock()
	remaining := len(c.optimistic)
	c.optMu.RUnlock()
	if remaining == 0 {
		t.Fatal("optimistic map must not be cleared while argus hasn't confirmed the new status")
	}
}
