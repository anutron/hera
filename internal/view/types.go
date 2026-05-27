package view

// PaneSource is the read-only interface the view needs from the PTY proxy
// substrate: per-argus-task byte streams plus a cancel hook. Production
// wire-up adapts Stage B's `*proxy.Subscription` registry to satisfy this
// interface; tests can supply a fake.
//
// SubscribeTask returns the current ring snapshot plus a channel of
// subsequent byte chunks for the given argus task ID. The unsub function,
// when non-nil, releases the listener registration so the upstream
// goroutine can stop fanning out to it.
//
// If no subscription exists for the requested task, all return values MAY
// be zero-valued (nil snapshot, nil channel, nil unsub) — callers must
// tolerate a not-found path so unbound rail rows render a placeholder
// rather than panic.
type PaneSource interface {
	SubscribeTask(taskID string) (snapshot []byte, bytes <-chan []byte, unsub func())

	// TaskSize returns the worker PTY's current cols/rows. When argus has
	// no live session for taskID (or the source has no upstream wired)
	// implementations MUST return (0, 0); callers fall back to a default
	// surface size in that case.
	TaskSize(taskID string) (cols, rows int)

	// ResizeTask asks the underlying argus task to resize its worker PTY
	// to (cols, rows). Implementations dispatch the call asynchronously
	// and dedupe redundant requests; failures are logged but not
	// surfaced to the caller. taskID == "" or non-positive dimensions
	// are no-ops.
	ResizeTask(taskID string, cols, rows int)
}

// TaskAliveChecker is an optional capability some PaneSource (or other)
// values may also implement. When the runtime PaneSource passed to
// BuildApp satisfies it, findInitialSelection filters worker bindings to
// only those whose argus task is still alive. A nil checker (i.e. the
// PaneSource doesn't satisfy the interface) means "no aliveness info";
// callers fall back to treating every live binding in the DB as live,
// which matches the pre-stage-K behavior the tests depend on.
type TaskAliveChecker interface {
	IsTaskAlive(taskID string) bool
}

// nilPaneSource is the do-nothing source used when no proxy is wired
// (e.g., tests that only exercise layout, or daemon startup before any
// bindings exist). Its SubscribeTask returns zero values for every taskID.
type nilPaneSource struct{}

// SubscribeTask satisfies PaneSource. Always returns zero values.
func (nilPaneSource) SubscribeTask(string) ([]byte, <-chan []byte, func()) {
	return nil, nil, nil
}

// TaskSize satisfies PaneSource. Always returns (0, 0) so callers letterbox
// to the default surface size.
func (nilPaneSource) TaskSize(string) (int, int) { return 0, 0 }

// ResizeTask satisfies PaneSource. No-op for the nil source; tests and
// daemon startup paths without a live argus connection use this.
func (nilPaneSource) ResizeTask(string, int, int) {}
