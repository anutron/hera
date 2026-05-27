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
	resizeCalls []resizeCall
	resizeErr   error

	// taskStatusByID maps argus task id → Status string returned from
	// GetTask. An unset id falls back to "in_progress" (alive).
	taskStatusByID map[string]string
	getTaskErr     error
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

func (f *fakeFetcher) ResizeTask(_ context.Context, taskID string, cols, rows int) error {
	f.mu.Lock()
	f.resizeCalls = append(f.resizeCalls, resizeCall{TaskID: taskID, Cols: cols, Rows: rows})
	err := f.resizeErr
	f.mu.Unlock()
	return err
}

func (f *fakeFetcher) GetTask(_ context.Context, taskID string) (*argus.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getTaskErr != nil {
		return nil, f.getTaskErr
	}
	status, ok := f.taskStatusByID[taskID]
	if !ok {
		status = "in_progress"
	}
	return &argus.Task{ID: taskID, Status: status}, nil
}

type resizeCall struct {
	TaskID string
	Cols   int
	Rows   int
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

// TestProxyManager_ResizeTaskDispatchesToFetcher pins that ResizeTask
// hits the fetcher's ResizeTask with the requested taskID and dimensions.
// The manager dispatches on a goroutine, so the test polls until the
// fake records the call.
func TestProxyManager_ResizeTaskDispatchesToFetcher(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 145, 50)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ff.mu.Lock()
		n := len(ff.resizeCalls)
		ff.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ff.mu.Lock()
	defer ff.mu.Unlock()
	if len(ff.resizeCalls) != 1 {
		t.Fatalf("resize calls = %d, want 1", len(ff.resizeCalls))
	}
	got := ff.resizeCalls[0]
	if got != (resizeCall{TaskID: "task-A", Cols: 145, Rows: 50}) {
		t.Fatalf("call = %+v, want {task-A, 145, 50}", got)
	}
}

// TestProxyManager_ResizeTaskDedupesRepeatedDims pins that repeated
// ResizeTask calls for the same (taskID, cols, rows) short-circuit
// locally rather than spamming argus.
func TestProxyManager_ResizeTaskDedupesRepeatedDims(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 145, 50)
	m.ResizeTask(context.Background(), "task-A", 145, 50)
	m.ResizeTask(context.Background(), "task-A", 145, 50)

	// Let any goroutines drain. Only the first call should have been
	// dispatched; the others must short-circuit.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		ff.mu.Lock()
		n := len(ff.resizeCalls)
		ff.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // give late dispatches a chance

	ff.mu.Lock()
	defer ff.mu.Unlock()
	if len(ff.resizeCalls) != 1 {
		t.Fatalf("resize calls = %d, want 1 (dedup failed)", len(ff.resizeCalls))
	}
}

// TestProxyManager_ResizeTaskDispatchesOnDimChange pins that changing
// the dimensions for a previously-resized task triggers a new dispatch.
func TestProxyManager_ResizeTaskDispatchesOnDimChange(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 145, 50)
	m.ResizeTask(context.Background(), "task-A", 100, 40)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ff.mu.Lock()
		n := len(ff.resizeCalls)
		ff.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ff.mu.Lock()
	defer ff.mu.Unlock()
	if len(ff.resizeCalls) != 2 {
		t.Fatalf("resize calls = %d, want 2", len(ff.resizeCalls))
	}
}

// TestProxyManager_ResizeTaskIgnoresBadInputs pins that empty taskID or
// non-positive dimensions are dropped locally without dispatch.
func TestProxyManager_ResizeTaskIgnoresBadInputs(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	defer m.Close()

	m.ResizeTask(context.Background(), "", 100, 40)
	m.ResizeTask(context.Background(), "task-A", 0, 40)
	m.ResizeTask(context.Background(), "task-A", 100, 0)
	m.ResizeTask(context.Background(), "task-A", -5, 40)

	time.Sleep(50 * time.Millisecond) // give any stray dispatch time

	ff.mu.Lock()
	defer ff.mu.Unlock()
	if len(ff.resizeCalls) != 0 {
		t.Fatalf("resize calls = %d, want 0 (bad inputs should drop)", len(ff.resizeCalls))
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

func TestProxyManager_IsTaskAlive(t *testing.T) {
	ff := &fakeFetcher{
		taskStatusByID: map[string]string{
			"alive":     "in_progress",
			"unknown":   "some-new-status",
			"completed": "complete",
			"failed":    "failed",
			"archived":  "archived",
		},
	}
	m := NewProxyManager(ff, nil)
	ctx := context.Background()
	cases := []struct {
		taskID string
		want   bool
	}{
		{"alive", true},
		{"unknown", true}, // unknown statuses default to alive (conservative)
		{"completed", false},
		{"failed", false},
		{"archived", false},
	}
	for _, c := range cases {
		got := m.IsTaskAlive(ctx, c.taskID)
		if got != c.want {
			t.Errorf("IsTaskAlive(%q): got %v, want %v", c.taskID, got, c.want)
		}
	}
	// Empty taskID is dead.
	if m.IsTaskAlive(ctx, "") {
		t.Errorf("IsTaskAlive(\"\"): expected false")
	}
}
