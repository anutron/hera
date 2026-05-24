package idle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anutron/ludwig/internal/argus"
	"github.com/anutron/ludwig/internal/events"
)

// fixedClock returns a clock that can be advanced explicitly.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTrackerWithClock(t *testing.T, debounce time.Duration) (*Tracker, *fixedClock) {
	clock := &fixedClock{t: time.Now()}
	tr := NewWithDebounce(debounce)
	tr.SetClock(clock.now)
	return tr, clock
}

func TestIsIdle_FalseWhenUnknown(t *testing.T) {
	tr := New()
	if tr.IsIdle("unknown-task") {
		t.Fatalf("IsIdle(unknown) should be false")
	}
}

func TestIsIdle_WithinDebounce_False(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 2*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	clock.advance(1 * time.Second)
	if tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be false within debounce window")
	}
}

func TestIsIdle_AfterDebounce_True(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 2*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	clock.advance(3 * time.Second)
	if !tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be true after debounce elapsed")
	}
}

func TestIsIdle_SessionStartedAfterIdle_False(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 1*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	clock.advance(2 * time.Second)
	if !tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be true before session.started")
	}
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionStarted, TaskID: "t1"})
	clock.advance(5 * time.Second)
	if tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be false after session.started, regardless of elapsed time")
	}
}

func TestIsIdle_SessionExitedAfterIdle_False(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 1*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	clock.advance(2 * time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionExited, TaskID: "t1"})
	clock.advance(5 * time.Second)
	if tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be false after session.exited")
	}
}

func TestIsIdle_IdleAfterStarted_DebouncesAgain(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 2*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionStarted, TaskID: "t1"})
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	clock.advance(1 * time.Second)
	if tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be false during fresh debounce window")
	}
	clock.advance(2 * time.Second)
	if !tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be true once new debounce elapses")
	}
}

func TestHandleEvent_IgnoresUnrelatedTypes(t *testing.T) {
	tr := New()
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeTaskCreated, TaskID: "t1"})
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeLinkCreated, TaskID: "t1"})
	if _, _, ok := tr.Lookup("t1"); ok {
		t.Fatalf("non-session events should not populate tracker state")
	}
}

func TestHandleEvent_PerTaskIsolation(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 1*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionStarted, TaskID: "t2"})
	clock.advance(2 * time.Second)
	if !tr.IsIdle("t1") {
		t.Fatalf("t1 should be idle")
	}
	if tr.IsIdle("t2") {
		t.Fatalf("t2 should NOT be idle")
	}
}
