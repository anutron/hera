package idle

import (
	"context"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/events"
)

// DefaultDebounce is the conservative idle debounce window applied by the
// tracker. The session.idle event must be the most recent session event
// for at least this long before IsIdle returns true. Tunable when argus
// clarifies session.idle semantics (see design D10).
const DefaultDebounce = 2 * time.Second

// sessionEvent records the kind of the most recent session event hera
// saw for a task, plus its timestamp.
type sessionEvent struct {
	kind string // events.TypeSessionIdle | events.TypeSessionStarted | events.TypeSessionExited
	at   time.Time
}

// Tracker maintains per-task idle state from argus's session.* event
// stream. Concurrent-safe.
type Tracker struct {
	mu       sync.RWMutex
	state    map[string]sessionEvent
	debounce time.Duration
	now      func() time.Time // injectable clock for tests
}

// New constructs a Tracker with DefaultDebounce.
func New() *Tracker {
	return NewWithDebounce(DefaultDebounce)
}

// NewWithDebounce constructs a Tracker with a custom debounce duration.
func NewWithDebounce(debounce time.Duration) *Tracker {
	return &Tracker{
		state:    map[string]sessionEvent{},
		debounce: debounce,
		now:      time.Now,
	}
}

// SetClock replaces the tracker's time source. Tests only.
func (t *Tracker) SetClock(now func() time.Time) {
	t.mu.Lock()
	t.now = now
	t.mu.Unlock()
}

// SetDebounce updates the idle-debounce threshold in place. Negative
// values clamp to zero — a debounce can never be negative.
//
// Zero means "the next session.idle event makes the task immediately
// eligible," gated only by the absence of a more-recent session.started
// or session.exited (see Tracker.IsIdle).
func (t *Tracker) SetDebounce(d time.Duration) {
	if d < 0 {
		d = 0
	}
	t.mu.Lock()
	t.debounce = d
	t.mu.Unlock()
}

// HandleEvent implements events.Handler. session.* events update per-task
// state. task.archived drops the entry (so the map doesn't grow unboundedly
// over the daemon's lifetime as tasks come and go). Other event types are
// ignored.
func (t *Tracker) HandleEvent(ctx context.Context, ev argus.Event) {
	switch ev.Type {
	case events.TypeSessionIdle, events.TypeSessionStarted, events.TypeSessionExited:
		if ev.TaskID == "" {
			return
		}
		t.mu.Lock()
		t.state[ev.TaskID] = sessionEvent{kind: ev.Type, at: t.now()}
		t.mu.Unlock()
	case events.TypeTaskArchived:
		if ev.TaskID == "" {
			return
		}
		t.mu.Lock()
		delete(t.state, ev.TaskID)
		t.mu.Unlock()
	}
}

// IsIdle reports whether the task is eligible for auto-submit injection.
// Returns true iff the most recent session event for taskID is
// session.idle AND it was recorded at least debounce ago.
func (t *Tracker) IsIdle(taskID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s, ok := t.state[taskID]
	if !ok {
		return false
	}
	if s.kind != events.TypeSessionIdle {
		return false
	}
	if t.now().Sub(s.at) < t.debounce {
		return false
	}
	return true
}

// Lookup returns the most recently seen session event for a task, mostly
// for diagnostics / status output. ok=false if hera has never seen a
// session event for that task.
func (t *Tracker) Lookup(taskID string) (eventType string, at time.Time, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s, exists := t.state[taskID]
	if !exists {
		return "", time.Time{}, false
	}
	return s.kind, s.at, true
}
