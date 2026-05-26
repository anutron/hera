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
}

// nilPaneSource is the do-nothing source used when no proxy is wired
// (e.g., tests that only exercise layout, or daemon startup before any
// bindings exist). Its SubscribeTask returns zero values for every taskID.
type nilPaneSource struct{}

// SubscribeTask satisfies PaneSource. Always returns zero values.
func (nilPaneSource) SubscribeTask(string) ([]byte, <-chan []byte, func()) {
	return nil, nil, nil
}
