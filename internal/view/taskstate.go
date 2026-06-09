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

// TaskInfoProvider is an optional capability on a PaneSource: it returns the
// full ArgusTaskInfo (name, project, worktree, elapsed, state) for a task id.
// populateRail type-asserts for it to populate coord-level metadata
// (CoordArgusName, CoordWorktreePath) that TaskStateProvider does not expose.
type TaskInfoProvider interface {
	TaskInfo(taskID string) (ArgusTaskInfo, bool)
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

	mu      sync.RWMutex
	states  map[string]ArgusTaskState
	infos   []ArgusTaskInfo            // full snapshot, render order = argus list order
	infoMap map[string]ArgusTaskInfo   // keyed by task ID for O(1) TaskInfo lookups
	ready   bool                       // true after the first successful poll

	// optimistic holds predicted statuses applied by the mutation bridge at the
	// START of a status-step goroutine (BUG-032: optimistic render). Each entry
	// overrides the polled status in Get until the next poll confirms the new
	// value (poll auto-clears confirmed entries) or the mutation bridge clears it
	// on write failure (ClearOptimistic). Guarded by optMu, separate from mu so
	// poll reads and optimistic writes do not contend.
	optMu      sync.RWMutex
	optimistic map[string]string // taskID → predicted status

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
		lister:     lister,
		interval:   interval,
		log:        log,
		states:     map[string]ArgusTaskState{},
		infoMap:    map[string]ArgusTaskInfo{},
		optimistic: map[string]string{},
		subs:       map[chan struct{}]struct{}{},
	}
}

// Get returns the cached state for taskID. ok is false when the task is not
// in the latest argus snapshot (unknown / not yet polled). When an optimistic
// status override exists for the task (set by SetOptimistic), the returned
// state's Status field reflects the predicted value rather than the polled one.
func (c *ArgusStateCache) Get(taskID string) (ArgusTaskState, bool) {
	c.mu.RLock()
	st, ok := c.states[taskID]
	c.mu.RUnlock()
	if !ok {
		return ArgusTaskState{}, false
	}
	c.optMu.RLock()
	optStatus, hasOpt := c.optimistic[taskID]
	c.optMu.RUnlock()
	if hasOpt {
		st.Status = optStatus
	}
	return st, true
}

// SetOptimistic stores an expected status for taskID so the rail reflects the
// new status immediately while the argus write is still in flight (BUG-032).
// Get returns this value in place of the polled status until the next poll
// confirms the same value (auto-cleared by poll) or ClearOptimistic is called.
// No-op for empty taskID.
func (c *ArgusStateCache) SetOptimistic(taskID, status string) {
	if taskID == "" {
		return
	}
	c.optMu.Lock()
	c.optimistic[taskID] = status
	c.optMu.Unlock()
}

// ClearOptimistic removes the optimistic status override for taskID so the rail
// reverts to the polled value on the next repopulate. Called by the mutation
// bridge when the argus write fails. No-op for empty taskID.
func (c *ArgusStateCache) ClearOptimistic(taskID string) {
	if taskID == "" {
		return
	}
	c.optMu.Lock()
	delete(c.optimistic, taskID)
	c.optMu.Unlock()
}

// GetInfo returns the full ArgusTaskInfo for taskID from the latest poll.
// ok is false when the task is not in the snapshot (unknown / not yet polled).
// Unlike Get, this returns identity and worktree fields in addition to state.
func (c *ArgusStateCache) GetInfo(taskID string) (ArgusTaskInfo, bool) {
	c.mu.RLock()
	info, ok := c.infoMap[taskID]
	c.mu.RUnlock()
	return info, ok
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
	nextInfoMap := make(map[string]ArgusTaskInfo, len(tasks))
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
		info := ArgusTaskInfo{
			ID:           t.ID,
			Name:         t.Name,
			Project:      t.Project,
			Elapsed:      t.Elapsed,
			State:        st,
			WorktreePath: t.WorktreePath,
			PRState:      t.PRState,
		}
		infos = append(infos, info)
		nextInfoMap[t.ID] = info
	}
	c.mu.Lock()
	changed := !sameStates(c.states, next) || !c.ready
	c.states = next
	c.infos = infos
	c.infoMap = nextInfoMap
	c.ready = true
	c.mu.Unlock()

	// Auto-clear optimistic entries that the poll has now confirmed, keeping
	// the map compact. An entry whose polled status matches the predicted value
	// is done — the optimistic is no longer needed. Entries whose polled status
	// differs are either mid-flight (transient) or were never applied (failure
	// cleared them already); leave them alone so the rail keeps showing the
	// predicted icon until the write round-trip finishes.
	c.optMu.Lock()
	for taskID, optStatus := range c.optimistic {
		if st, ok := next[taskID]; ok && st.Status == optStatus {
			delete(c.optimistic, taskID)
		}
	}
	c.optMu.Unlock()

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
