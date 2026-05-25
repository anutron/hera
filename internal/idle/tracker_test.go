package idle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/events"
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

// SetDebounce hot-reload: an event recorded 1.5s ago is NOT idle under a
// 2s debounce, but becomes idle the moment the debounce is shortened to 1s.
// Spec scenario: "Debounce hot-reload changes behavior immediately".
func TestSetDebounce_LoweringMakesIdleImmediately(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 2*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	clock.advance(1500 * time.Millisecond)
	if tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be false under 2s debounce with 1.5s elapsed")
	}
	tr.SetDebounce(1 * time.Second)
	if !tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be true immediately after shortening debounce to 1s")
	}
}

// SetDebounce raising the threshold pulls a previously-idle task back
// into the busy set without a new event.
func TestSetDebounce_RaisingPullsTaskOutOfIdle(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 1*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	clock.advance(2 * time.Second)
	if !tr.IsIdle("t1") {
		t.Fatalf("IsIdle should be true under 1s debounce with 2s elapsed")
	}
	tr.SetDebounce(5 * time.Second)
	if tr.IsIdle("t1") {
		t.Fatalf("IsIdle should flip to false after raising debounce to 5s")
	}
}

// Spec scenario: "Zero debounce, idle event makes task immediately eligible".
// A zero debounce does NOT mean "always idle" — session.started/.exited still
// gate it. Verify both halves.
func TestSetDebounce_ZeroMakesIdleEventImmediatelyEligible(t *testing.T) {
	tr, clock := newTrackerWithClock(t, 2*time.Second)
	tr.SetDebounce(0)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	if !tr.IsIdle("t1") {
		t.Fatalf("with 0 debounce, an idle event MUST make task immediately eligible")
	}
	// session.started overrides idle even under zero debounce.
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionStarted, TaskID: "t1"})
	if tr.IsIdle("t1") {
		t.Fatalf("session.started must override idle even with 0 debounce")
	}
	// Subsequent session.idle re-enables eligibility immediately.
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	if !tr.IsIdle("t1") {
		t.Fatalf("subsequent idle event must re-enable eligibility under 0 debounce")
	}
	_ = clock
}

// Negative debounce values clamp to zero (never go negative).
func TestSetDebounce_NegativeClampsToZero(t *testing.T) {
	tr, _ := newTrackerWithClock(t, 2*time.Second)
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
	tr.SetDebounce(-5 * time.Second)
	// Should behave exactly like 0 (idle event makes task immediately eligible).
	if !tr.IsIdle("t1") {
		t.Fatalf("negative debounce should clamp to 0 and treat idle event as eligible")
	}
}

// Concurrent SetDebounce + IsIdle + HandleEvent must be race-free under -race.
func TestSetDebounce_RaceFreeUnderConcurrentReads(t *testing.T) {
	tr := New()
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = tr.IsIdle("t1")
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t1"})
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				select {
				case <-stop:
					return
				default:
					tr.SetDebounce(time.Duration(j%5) * time.Millisecond)
				}
			}
		}(i)
	}

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}
