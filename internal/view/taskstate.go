package view

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// ArgusTaskState is the per-task state hera reads from argus to render the
// rail accurately — status, idle/needs-input runtime flags, the archived bit,
// and the GitHub PR review state. It is the "argus reality" the rail reflects,
// as opposed to hera's own binding bookkeeping.
type ArgusTaskState struct {
	Status     string // pending | in_progress | in_review | complete
	Idle       bool
	NeedsInput bool
	Archived   bool
	// PRState is the GitHub PR review state string from argus's daemon poll.
	// Empty when argus does not populate the field (older daemons or no PR).
	PRState string
}

// ArgusTaskInfo is the full per-task snapshot the cache retains so the rail
// can render freelance rows (unmanaged argus tasks) directly from argus's
// task list: identity (ID/Name), grouping key (Project, the repo), the
// pre-formatted Elapsed age, plus the embedded runtime State for the icon.
type ArgusTaskInfo struct {
	ID      string
	Name    string
	Project string
	Elapsed string
	State   ArgusTaskState
	// WorktreePath is argus's per-task worktree directory. Carried so a
	// freelance row can open a PR straight from the argus task's worktree
	// (`^p`) without a hera binding. Already present in the polled task list,
	// so reading it costs no extra network. Absent on old daemons.
	WorktreePath string
	// PRState mirrors ArgusTaskState.PRState for freelance rows.
	PRState string
}

// TaskStateProvider is an optional capability on a PaneSource: it returns the
// argus-reported state for a task id. populateRail type-asserts the rail's
// PaneSource for it (mirroring the optional TaskAliveChecker) and, when
// present, drives rail icons + archived grouping from argus state. Absent in
// tests, where the rail falls back to binding-based rendering.
type TaskStateProvider interface {
	TaskState(taskID string) (ArgusTaskState, bool)
}

// FreelanceProvider is an optional capability on a PaneSource: it returns
// the full live argus task list so populateRail can surface unmanaged tasks
// (freelancers) in the rail. Mirrors the optional TaskStateProvider pattern;
// absent in tests, where the rail simply renders no Freelance section.
type FreelanceProvider interface {
	LiveTasks() []ArgusTaskInfo
}

// argusLister is the subset of *argus.Client the cache needs. It lists ALL
// tasks (including archived) so the cache can tell archived tasks apart — the
// default task list excludes them.
type argusLister interface {
	ListTasksAll(ctx context.Context) ([]argus.Task, error)
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
	infos  []ArgusTaskInfo // full snapshot, render order = argus list order
	ready  bool            // true after the first successful poll

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

// List returns a copy of the full task snapshot from the latest poll, in
// argus's list order. Used by the rail to enumerate freelance candidates
// (non-archived tasks not bound by hera). Empty before the first poll.
func (c *ArgusStateCache) List() []ArgusTaskInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ArgusTaskInfo, len(c.infos))
	copy(out, c.infos)
	return out
}

// Ready reports whether at least one successful poll has completed. Callers
// use it to avoid treating a cold-cache miss as "task gone".
func (c *ArgusStateCache) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
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
		delete(c.subs, ch)
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
	tasks, err := c.lister.ListTasksAll(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn("argus state cache: list tasks", "err", err)
		}
		return
	}
	next := make(map[string]ArgusTaskState, len(tasks))
	infos := make([]ArgusTaskInfo, 0, len(tasks))
	for _, t := range tasks {
		st := ArgusTaskState{
			Status:     t.Status,
			Idle:       t.Idle,
			NeedsInput: t.NeedsInput,
			Archived:   t.Archived,
			PRState:    t.PRState,
		}
		next[t.ID] = st
		infos = append(infos, ArgusTaskInfo{
			ID:           t.ID,
			Name:         t.Name,
			Project:      t.Project,
			Elapsed:      t.Elapsed,
			State:        st,
			WorktreePath: t.WorktreePath,
			PRState:      t.PRState,
		})
	}
	c.mu.Lock()
	changed := !sameStates(c.states, next) || !c.ready
	c.states = next
	c.infos = infos
	c.ready = true
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
