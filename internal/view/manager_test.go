package view

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// fakeFetcher is a minimal proxy.Fetcher that records snapshot + stream
// invocations per argus task id. Streams block until ctx is canceled —
// production-shaped behavior the ProxyManager relies on for shutdown.
type fakeFetcher struct {
	mu          sync.Mutex
	snapshotIDs []string
	streamIDs   []string
}

func (f *fakeFetcher) GetTaskOutput(_ context.Context, taskID string) (argus.TaskOutputSnapshot, error) {
	f.mu.Lock()
	f.snapshotIDs = append(f.snapshotIDs, taskID)
	f.mu.Unlock()
	return argus.TaskOutputSnapshot{}, nil
}

func (f *fakeFetcher) StreamTaskOutput(ctx context.Context, taskID string, _ uint64, _ argus.TaskOutputHandler) error {
	f.mu.Lock()
	f.streamIDs = append(f.streamIDs, taskID)
	f.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeFetcher) GetTaskSize(_ context.Context, _ string) (int, int, error) {
	return 0, 0, nil
}

// TestProxyManager_SeedCreatesOnePerTaskID pins the seed path: every
// taskID gets a Subscription, and the fetcher sees one snapshot per id.
func TestProxyManager_SeedCreatesOnePerTaskID(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Seed(ctx, []string{"task-A", "task-B", "task-C"})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ff.mu.Lock()
		n := len(ff.snapshotIDs)
		ff.mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ff.mu.Lock()
	gotSnap := append([]string(nil), ff.snapshotIDs...)
	ff.mu.Unlock()
	seen := map[string]bool{}
	for _, id := range gotSnap {
		seen[id] = true
	}
	for _, want := range []string{"task-A", "task-B", "task-C"} {
		if !seen[want] {
			t.Errorf("no snapshot for %q (got %+v)", want, gotSnap)
		}
	}

	tids := m.TaskIDs()
	if len(tids) != 3 {
		t.Errorf("TaskIDs len = %d, want 3", len(tids))
	}
}

// TestProxyManager_EnsureIdempotent pins that repeat Ensure calls for the
// same id return the same Subscription instance — no double-subscribe.
func TestProxyManager_EnsureIdempotent(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub1 := m.Ensure(ctx, "T1")
	sub2 := m.Ensure(ctx, "T1")
	if sub1 != sub2 {
		t.Fatalf("Ensure(T1) returned different subscriptions on repeat call")
	}
	if got := m.TaskIDs(); len(got) != 1 {
		t.Fatalf("TaskIDs len = %d, want 1", len(got))
	}
}

// TestProxyManager_CloseClearsAndReleases pins Close: every subscription is
// closed and the manager's internal map is empty afterward.
func TestProxyManager_CloseClearsAndReleases(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Seed(ctx, []string{"a", "b"})

	m.Close()
	if got := m.TaskIDs(); len(got) != 0 {
		t.Fatalf("TaskIDs after Close = %+v, want empty", got)
	}
	// Calling Close again must not panic / double-close.
	m.Close()
}
