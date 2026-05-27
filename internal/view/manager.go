package view

import (
	"context"
	"log/slog"
	"sync"

	"github.com/anutron/hera/internal/view/proxy"
)

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

	mu   sync.Mutex
	subs map[string]*proxy.Subscription
}

// NewProxyManager constructs a ProxyManager. fetcher is the source for
// snapshot + SSE fetches (production code passes an *argus.Client).
func NewProxyManager(fetcher proxy.Fetcher, log *slog.Logger) *ProxyManager {
	if log == nil {
		log = slog.Default()
	}
	return &ProxyManager{
		fetcher: fetcher,
		log:     log,
		subs:    make(map[string]*proxy.Subscription),
	}
}

// Ensure starts (or returns) a Subscription for the given argus task id.
// Repeated calls for the same id return the same Subscription. The parent
// context bounds the upstream lifetime; cancellation is equivalent to
// Close on every subscription.
func (m *ProxyManager) Ensure(ctx context.Context, taskID string) *proxy.Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sub, ok := m.subs[taskID]; ok {
		return sub
	}
	sub := proxy.NewSubscription(ctx, m.fetcher, taskID)
	m.subs[taskID] = sub
	return sub
}

// Seed opens a Subscription per taskID. Existing subscriptions are reused.
func (m *ProxyManager) Seed(ctx context.Context, taskIDs []string) {
	for _, id := range taskIDs {
		m.Ensure(ctx, id)
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
