package view

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// ArgusTaskState is the per-task state hera reads from argus to render the
// rail accurately — status, idle/needs-input runtime flags, and the archived
// bit. It is the "argus reality" the rail reflects, as opposed to hera's own
// binding bookkeeping.
type ArgusTaskState struct {
	Status     string // pending | in_progress | in_review | complete
	Idle       bool
	NeedsInput bool
	Archived   bool
}

// TaskStateProvider is an optional capability on a PaneSource: it returns the
// argus-reported state for a task id. populateRail type-asserts the rail's
// PaneSource for it (mirroring the optional TaskAliveChecker) and, when
// present, drives rail icons + archived grouping from argus state. Absent in
// tests, where the rail falls back to binding-based rendering.
type TaskStateProvider interface {
	TaskState(taskID string) (ArgusTaskState, bool)
}

// argusLister is the subset of *argus.Client the cache needs.
type argusLister interface {
	ListTasks(ctx context.Context) ([]argus.Task, error)
}

// DefaultArgusPollInterval is how often the state cache re-lists argus tasks.
// The rail isn't latency-critical for status; a few seconds keeps it live
// without hammering the local daemon.
const DefaultArgusPollInterval = 2 * time.Second

// ArgusStateCache polls argus's task list and caches each task's state so the
// rail can read it without blocking the tview event loop. It notifies
// subscribers when the snapshot changes so the rail can repaint to reflect
// status transitions (working → idle → complete, needs-input, archive).
type ArgusStateCache struct {
	lister   argusLister
	interval time.Duration
	log      *slog.Logger

	mu     sync.RWMutex
	states map[string]ArgusTaskState

	submu sync.Mutex
	subs  map[chan struct{}]struct{}
}

// NewArgusStateCache constructs a cache over the given lister. Call Run to
// start polling; Get / Subscribe are safe before Run (they just return empty
// / never fire until the first poll lands).
func NewArgusStateCache(lister argusLister, interval time.Duration, log *slog.Logger) *ArgusStateCache {
	if interval <= 0 {
		interval = DefaultArgusPollInterval
	}
	if log == nil {
		log = slog.Default()
	}
	return &ArgusStateCache{
		lister:   lister,
		interval: interval,
		log:      log,
		states:   map[string]ArgusTaskState{},
		subs:     map[chan struct{}]struct{}{},
	}
}

// Get returns the cached state for taskID. ok is false when the task is not
// in the latest argus snapshot (unknown / not yet polled).
func (c *ArgusStateCache) Get(taskID string) (ArgusTaskState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st, ok := c.states[taskID]
	return st, ok
}

// Subscribe returns a channel that receives a (coalesced) signal whenever the
// cached snapshot changes, plus a cancel func to unsubscribe. The channel has
// depth 1 and drops when full, so a slow consumer never blocks the poller.
func (c *ArgusStateCache) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	c.submu.Lock()
	c.subs[ch] = struct{}{}
	c.submu.Unlock()
	return ch, func() {
		c.submu.Lock()
		if _, ok := c.subs[ch]; ok {
			delete(c.subs, ch)
		}
		c.submu.Unlock()
	}
}

// Run polls argus on the configured interval until ctx is done. It does one
// immediate poll on start so the cache is warm before the first rail render.
func (c *ArgusStateCache) Run(ctx context.Context) {
	c.poll(ctx)
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.poll(ctx)
		}
	}
}

func (c *ArgusStateCache) poll(ctx context.Context) {
	tasks, err := c.lister.ListTasks(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("argus state cache: list tasks", "err", err)
		}
		return
	}
	next := make(map[string]ArgusTaskState, len(tasks))
	for _, t := range tasks {
		next[t.ID] = ArgusTaskState{
			Status:     t.Status,
			Idle:       t.Idle,
			NeedsInput: t.NeedsInput,
			Archived:   t.Archived,
		}
	}
	c.mu.Lock()
	changed := !sameStates(c.states, next)
	c.states = next
	c.mu.Unlock()
	if changed {
		c.notify()
	}
}

func (c *ArgusStateCache) notify() {
	c.submu.Lock()
	defer c.submu.Unlock()
	for ch := range c.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func sameStates(a, b map[string]ArgusTaskState) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || va != vb {
			return false
		}
	}
	return true
}
