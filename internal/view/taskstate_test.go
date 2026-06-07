package view

import (
	"context"
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
