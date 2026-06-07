package view

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/view/proxy"
)

// Resize-dispatch tuning. The debounce absorbs transient pane geometries —
// most importantly the first frame(s) a session draws at the SDK wsTty
// default 80x24 surface before argus's resize envelope is processed. At
// 80x24 the panes' inner rects come out to 20x21; dispatching that to
// argus resizes the worker PTY to 20 cols AND kick-rerenders the agent's
// Claude there (stop + --resume), permanently baking ~20-char-wrapped
// output into the session history. The real envelope lands within
// milliseconds, so a short debounce means the bogus size never leaves
// hera. The retry interval/cap heal the opposite failure: a correction
// that reaches argus while the worker is mid-kick-restart 404s ("no
// active session") and must be re-sent once the session is back.
const (
	defaultResizeDebounce    = 250 * time.Millisecond
	defaultResizeRetryDelay  = time.Second
	defaultResizeMaxAttempts = 5
)

// resizeState tracks the per-task resize dispatcher. desired is the most
// recent size any pane asked for; applied is the last size argus
// acknowledged. running guards the single dispatch goroutine per task.
// All fields are protected by ProxyManager.resizeMu.
type resizeState struct {
	desiredCols, desiredRows int
	appliedCols, appliedRows int
	applied                  bool
	running                  bool
}

// ProxyManager owns one PTY proxy.Subscription per live argus task. The
// daemon seeds it at startup from the bindings table and closes every
// subscription at shutdown. Subscriptions fan out to the per-connection
// view session via Subscribe.
//
// The manager itself does not poll for new bindings; the higher-level
// rail-event broadcaster (Stage I) is the source for binding lifecycle
// events. Until that's wired, the manager only exposes Seed/Close — Stage
// J's contract is "snapshot of bindings at daemon startup."
type ProxyManager struct {
	fetcher proxy.Fetcher
	log     *slog.Logger

	// lifetimeCtx bounds every subscription's upstream SSE loop. It is the
	// DAEMON's context, not any view session's — subscriptions must outlive
	// the connection that first opened them (the rings they fill are shared
	// across all current and future sessions). Binding a subscription to a
	// session ctx was the "frozen pane after reconnect" bug: the first
	// session to view an un-seeded task (a freelancer, or a binding created
	// after startup) created the subscription bound to its own ctx; when that
	// session closed, the SSE loop died but the dead subscription stayed
	// cached, so every later session got a snapshot that never advanced.
	lifetimeCtx context.Context

	mu   sync.Mutex
	subs map[string]*proxy.Subscription

	// resizes holds the per-task resize dispatcher state (see
	// resizeState). ResizeTask short-circuits when the requested
	// dimensions match the last size argus ACKNOWLEDGED — argus also
	// caches redundant calls, but the local dedup avoids waking a
	// goroutine and burning an HTTP roundtrip on every Draw.
	resizeMu sync.Mutex
	resizes  map[string]*resizeState

	// Resize dispatcher knobs; production uses the defaultResize*
	// constants, tests shrink them so they don't sit through real
	// debounce/retry intervals.
	resizeDebounce    time.Duration
	resizeRetryDelay  time.Duration
	resizeMaxAttempts int
}

// NewProxyManager constructs a ProxyManager. ctx is the DAEMON-lifetime
// context that bounds every subscription's upstream loop (see lifetimeCtx);
// pass the daemon's long-lived proxy context, never a per-session ctx. A nil
// ctx falls back to context.Background() so tests need not thread one through.
// fetcher is the source for snapshot + SSE fetches (production code passes an
// *argus.Client).
func NewProxyManager(ctx context.Context, fetcher proxy.Fetcher, log *slog.Logger) *ProxyManager {
	if log == nil {
		log = slog.Default()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &ProxyManager{
		fetcher:           fetcher,
		log:               log,
		lifetimeCtx:       ctx,
		subs:              make(map[string]*proxy.Subscription),
		resizes:           make(map[string]*resizeState),
		resizeDebounce:    defaultResizeDebounce,
		resizeRetryDelay:  defaultResizeRetryDelay,
		resizeMaxAttempts: defaultResizeMaxAttempts,
	}
}

// Ensure starts (or returns) a Subscription for the given argus task id.
// Repeated calls for the same id return the same Subscription. Every
// subscription is bound to the manager's daemon-lifetime context (NOT the
// calling session's), so it keeps streaming into its ring after the session
// that opened it closes — and stays usable by future sessions. The whole set
// is torn down only by Close (daemon shutdown).
func (m *ProxyManager) Ensure(taskID string) *proxy.Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub, ok := m.subs[taskID]; ok {
		return sub
	}
	sub := proxy.NewSubscription(m.lifetimeCtx, m.fetcher, taskID)
	m.subs[taskID] = sub
	return sub
}

// Seed opens a Subscription per taskID. Existing subscriptions are reused.
func (m *ProxyManager) Seed(taskIDs []string) {
	for _, id := range taskIDs {
		m.Ensure(id)
	}
	m.log.Info("pty proxy seeded", "subscriptions", len(taskIDs))
}

// TaskIDs returns the argus task ids the manager is currently serving.
// Order is unspecified.
func (m *ProxyManager) TaskIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.subs))
	for id := range m.subs {
		out = append(out, id)
	}
	return out
}

// TaskSize asks argus for the worker PTY's current cols/rows. Errors
// (404 from the API, transport failures) collapse to (0, 0) so the
// caller can fall back to a default surface size without unwrapping.
func (m *ProxyManager) TaskSize(ctx context.Context, taskID string) (cols, rows int) {
	if m == nil || m.fetcher == nil || taskID == "" {
		return 0, 0
	}
	c, r, err := m.fetcher.GetTaskSize(ctx, taskID)
	if err != nil {
		return 0, 0
	}
	return c, r
}

// IsTaskAlive reports whether argus considers taskID still active. An
// alive task has a session that can serve /output and /input; a dead one
// (status "complete" / "failed" / "halted" / "killed" / "archived" /
// "stopped" / "cancelled") returns 404 from /output and is a poor
// initial-selection candidate.
//
// Argus 404 on GetTask means the task is gone — treat as dead. Other
// errors (transport, 5xx) are inconclusive: assume alive so a transient
// argus hiccup doesn't strand the operator on an empty rail.
func (m *ProxyManager) IsTaskAlive(ctx context.Context, taskID string) bool {
	if m == nil || m.fetcher == nil || taskID == "" {
		return false
	}
	t, err := m.fetcher.GetTask(ctx, taskID)
	if err != nil {
		var he *argus.HTTPError
		if errors.As(err, &he) && he.StatusCode == 404 {
			return false
		}
		return true
	}
	if t == nil {
		return false
	}
	return taskStatusAlive(t.Status)
}

// taskStatusAlive maps an argus task.Status string to a coarse alive/dead
// classification. Conservative: any unknown status counts as alive so a
// new argus state doesn't silently filter every binding out of the rail.
func taskStatusAlive(status string) bool {
	switch status {
	case "complete", "completed",
		"complete:success", "complete:failure",
		"failed", "halted", "killed",
		"archived", "stopped",
		"cancelled", "canceled":
		return false
	}
	return true
}

// ResizeTask asks argus to resize the worker PTY for taskID to (cols, rows).
// The HTTP call is dispatched on a background goroutine so Draw paths
// stay non-blocking. Dispatching is coalesced per task behind a short
// debounce window (latest size wins): the first frames of a session can
// draw at the SDK's default 80x24 surface before argus's resize envelope
// is processed, and the 20x21 pane allocation that geometry produces
// must never reach argus — argus would kick-rerender the worker's Claude
// at 20 cols and permanently bake narrow-wrapped output into the session
// history. Within the debounce, the real envelope re-layout supersedes
// the transient size.
//
// Calls that repeat the dimensions argus last ACKNOWLEDGED short-circuit
// locally (argus also caches redundant calls, but skipping the goroutine
// and the HTTP roundtrip reduces churn during a resize-drag). A failed
// dispatch — typically a 404 while the worker is mid-kick-restart from a
// previous resize — is retried with the latest desired size at
// resizeRetryDelay intervals, bounded by resizeMaxAttempts, so the
// correction lands once the session is back instead of stranding the PTY
// at a stale size.
//
// ctx bounds the dispatcher goroutine and its HTTP requests. Callers
// typically pass the session-scoped context so pending resizes are
// abandoned when the hera-view session ends.
func (m *ProxyManager) ResizeTask(ctx context.Context, taskID string, cols, rows int) {
	if m == nil || m.fetcher == nil || taskID == "" {
		return
	}
	if cols <= 0 || rows <= 0 {
		return
	}
	m.resizeMu.Lock()
	st, ok := m.resizes[taskID]
	if !ok {
		st = &resizeState{}
		m.resizes[taskID] = st
	}
	st.desiredCols, st.desiredRows = cols, rows
	if st.running || (st.applied && st.appliedCols == cols && st.appliedRows == rows) {
		// A live dispatcher will pick up the new desired size; an
		// already-applied size needs no dispatch at all.
		m.resizeMu.Unlock()
		return
	}
	st.running = true
	m.resizeMu.Unlock()

	go m.runResizeDispatch(ctx, taskID, st)
}

// runResizeDispatch is the per-task resize dispatcher goroutine: wait out
// the debounce, send the latest desired size, retry on failure, exit once
// desired == applied (or the attempt budget / ctx runs out). At most one
// dispatcher runs per task (guarded by resizeState.running).
func (m *ProxyManager) runResizeDispatch(ctx context.Context, taskID string, st *resizeState) {
	stop := func() {
		m.resizeMu.Lock()
		st.running = false
		m.resizeMu.Unlock()
	}

	wait := m.resizeDebounce
	attempts := 0
	// triedCols/triedRows start at zero on purpose: valid dims are always
	// >= 1, so the first pass through the loop always differs and seeds a
	// fresh attempt budget. The zero value is the "nothing tried yet"
	// sentinel.
	var triedCols, triedRows int
	for {
		select {
		case <-ctx.Done():
			stop()
			return
		case <-time.After(wait):
		}

		m.resizeMu.Lock()
		cols, rows := st.desiredCols, st.desiredRows
		if st.applied && st.appliedCols == cols && st.appliedRows == rows {
			st.running = false
			m.resizeMu.Unlock()
			return
		}
		m.resizeMu.Unlock()

		// A new desired size gets a fresh attempt budget.
		if cols != triedCols || rows != triedRows {
			attempts = 0
			triedCols, triedRows = cols, rows
		}

		err := m.fetcher.ResizeTask(ctx, taskID, cols, rows)
		if err == nil {
			m.resizeMu.Lock()
			st.applied = true
			st.appliedCols, st.appliedRows = cols, rows
			m.resizeMu.Unlock()
			// Loop once more: the desired size may have moved while the
			// HTTP call was in flight.
			wait = m.resizeDebounce
			continue
		}

		// 404 (no active session) is expected for inactive workers and for
		// workers mid-kick-restart; log at debug so we don't spam on every
		// pane swap.
		attempts++
		m.log.Debug("argus resize task failed", "task_id", taskID, "cols", cols, "rows", rows, "attempt", attempts, "err", err)
		if attempts >= m.resizeMaxAttempts {
			// Before giving up, re-check desired ATOMICALLY with the
			// running-flag clear: a ResizeTask call that landed while this
			// attempt was in flight saw running=true and only updated
			// desired — exiting now would orphan that size until some
			// later layout change. A changed desired instead gets a fresh
			// attempt budget on the next iteration.
			m.resizeMu.Lock()
			if st.desiredCols != cols || st.desiredRows != rows {
				m.resizeMu.Unlock()
				wait = m.resizeRetryDelay
				continue
			}
			st.running = false
			m.resizeMu.Unlock()
			m.log.Debug("argus resize task gave up", "task_id", taskID, "cols", cols, "rows", rows, "attempts", attempts)
			return
		}
		wait = m.resizeRetryDelay
	}
}

// ResetApplied clears the "already applied" flag for taskID, forcing the next
// ResizeTask dispatch to reach argus even when the requested dimensions match
// the size sent to the previous session. Called after an argus task session
// restarts (BUG-053): the new session starts at the argus default (80×24) but
// ProxyManager still thinks the previous session's allocation was in effect.
// Without this reset the dedup guard silently skips the next resize call,
// leaving the new session at the wrong size until the operator manually
// resizes the terminal.
func (m *ProxyManager) ResetApplied(taskID string) {
	if m == nil || taskID == "" {
		return
	}
	m.resizeMu.Lock()
	if st, ok := m.resizes[taskID]; ok {
		st.applied = false
	}
	m.resizeMu.Unlock()
}

// Close tears down every subscription. Safe to call multiple times; after
// Close, the manager's subscription map is empty.
func (m *ProxyManager) Close() {
	m.mu.Lock()
	subs := m.subs
	m.subs = make(map[string]*proxy.Subscription)
	m.mu.Unlock()
	for _, s := range subs {
		s.Close()
	}
}
