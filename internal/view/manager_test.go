package view

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// errNoActiveSession mimics argus's 404 from POST /api/tasks/{id}/resize
// while a worker session is down (e.g., mid-kick-restart).
var errNoActiveSession = errors.New("argus: no active session")

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

// resizeCallCount returns the number of recorded resize dispatches.
func (f *fakeFetcher) resizeCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.resizeCalls)
}

// setResizeErr swaps the error returned by subsequent ResizeTask calls.
func (f *fakeFetcher) setResizeErr(err error) {
	f.mu.Lock()
	f.resizeErr = err
	f.mu.Unlock()
}

// waitForResizeCalls polls until the fake has recorded at least n resize
// dispatches or the deadline passes. Returns the recorded calls.
func waitForResizeCalls(t *testing.T, ff *fakeFetcher, n int, deadline time.Duration) []resizeCall {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if ff.resizeCallCount() >= n {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	ff.mu.Lock()
	defer ff.mu.Unlock()
	return append([]resizeCall(nil), ff.resizeCalls...)
}

// newResizeTestManager builds a ProxyManager with fast resize timings so
// tests exercising the coalesce/retry dispatcher don't sit through the
// production debounce/retry intervals.
func newResizeTestManager(ff *fakeFetcher) *ProxyManager {
	m := NewProxyManager(ff, nil)
	m.resizeDebounce = 5 * time.Millisecond
	m.resizeRetryDelay = 5 * time.Millisecond
	return m
}

// TestProxyManager_ResizeTaskDispatchesToFetcher pins that ResizeTask
// hits the fetcher's ResizeTask with the requested taskID and dimensions.
// The manager dispatches on a goroutine after a debounce window, so the
// test polls until the fake records the call.
func TestProxyManager_ResizeTaskDispatchesToFetcher(t *testing.T) {
	ff := &fakeFetcher{}
	m := newResizeTestManager(ff)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 145, 50)

	calls := waitForResizeCalls(t, ff, 1, time.Second)
	if len(calls) != 1 {
		t.Fatalf("resize calls = %d, want 1", len(calls))
	}
	if calls[0] != (resizeCall{TaskID: "task-A", Cols: 145, Rows: 50}) {
		t.Fatalf("call = %+v, want {task-A, 145, 50}", calls[0])
	}
}

// TestProxyManager_ResizeTaskDedupesRepeatedDims pins that repeated
// ResizeTask calls for the same (taskID, cols, rows) short-circuit
// locally rather than spamming argus.
func TestProxyManager_ResizeTaskDedupesRepeatedDims(t *testing.T) {
	ff := &fakeFetcher{}
	m := newResizeTestManager(ff)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 145, 50)
	calls := waitForResizeCalls(t, ff, 1, time.Second)
	if len(calls) != 1 {
		t.Fatalf("resize calls = %d, want 1", len(calls))
	}

	// Repeats after a successful dispatch must short-circuit.
	m.ResizeTask(context.Background(), "task-A", 145, 50)
	m.ResizeTask(context.Background(), "task-A", 145, 50)
	time.Sleep(50 * time.Millisecond) // give late dispatches a chance

	if n := ff.resizeCallCount(); n != 1 {
		t.Fatalf("resize calls = %d, want 1 (dedup failed)", n)
	}
}

// TestProxyManager_ResizeTaskDispatchesOnDimChange pins that changing
// the dimensions for a previously-resized task triggers a new dispatch.
func TestProxyManager_ResizeTaskDispatchesOnDimChange(t *testing.T) {
	ff := &fakeFetcher{}
	m := newResizeTestManager(ff)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 145, 50)
	if calls := waitForResizeCalls(t, ff, 1, time.Second); len(calls) != 1 {
		t.Fatalf("resize calls = %d, want 1", len(calls))
	}

	m.ResizeTask(context.Background(), "task-A", 100, 40)
	calls := waitForResizeCalls(t, ff, 2, time.Second)
	if len(calls) != 2 {
		t.Fatalf("resize calls = %d, want 2", len(calls))
	}
	if calls[1] != (resizeCall{TaskID: "task-A", Cols: 100, Rows: 40}) {
		t.Fatalf("second call = %+v, want {task-A, 100, 40}", calls[1])
	}
}

// TestProxyManager_ResizeTaskCoalescesTransientDims pins the fix for the
// narrow-wrap bug: when a pane draws at the pre-resize-envelope 80x24
// default surface (inner 20x21) and the real envelope lands within the
// debounce window, the transient 20x21 must NEVER reach argus — argus
// would kick-rerender the worker's Claude at 20 cols and bake ~20-char
// wrapped output into the session history. Only the settled size is sent.
func TestProxyManager_ResizeTaskCoalescesTransientDims(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	m.resizeDebounce = 60 * time.Millisecond
	m.resizeRetryDelay = 5 * time.Millisecond
	defer m.Close()

	// First frame at the default 80x24 surface…
	m.ResizeTask(context.Background(), "task-A", 20, 21)
	// …superseded milliseconds later by the real resize envelope.
	m.ResizeTask(context.Background(), "task-A", 83, 45)

	calls := waitForResizeCalls(t, ff, 1, time.Second)
	time.Sleep(80 * time.Millisecond) // allow any (wrong) second dispatch
	ff.mu.Lock()
	calls = append([]resizeCall(nil), ff.resizeCalls...)
	ff.mu.Unlock()

	if len(calls) != 1 {
		t.Fatalf("resize calls = %d, want 1 (coalesce failed): %+v", len(calls), calls)
	}
	if calls[0] != (resizeCall{TaskID: "task-A", Cols: 83, Rows: 45}) {
		t.Fatalf("call = %+v, want the settled {task-A, 83, 45}", calls[0])
	}
}

// TestProxyManager_ResizeTaskRetriesOnFailure pins the self-heal path:
// a resize that fails (argus 404s while the worker is mid-kick-restart)
// must be retried with the latest desired size until it lands. Without
// the retry, the worker PTY stays at the stale size forever because the
// dedupe cache records the failed dispatch as applied.
func TestProxyManager_ResizeTaskRetriesOnFailure(t *testing.T) {
	ff := &fakeFetcher{}
	ff.setResizeErr(errNoActiveSession)
	m := newResizeTestManager(ff)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 83, 45)

	// Let at least one failing dispatch happen, then bring the session back.
	if calls := waitForResizeCalls(t, ff, 1, time.Second); len(calls) < 1 {
		t.Fatalf("no dispatch attempt recorded")
	}
	ff.setResizeErr(nil)

	calls := waitForResizeCalls(t, ff, 2, time.Second)
	if len(calls) < 2 {
		t.Fatalf("resize calls = %d, want >= 2 (no retry after failure)", len(calls))
	}
	last := calls[len(calls)-1]
	if last != (resizeCall{TaskID: "task-A", Cols: 83, Rows: 45}) {
		t.Fatalf("last call = %+v, want {task-A, 83, 45}", last)
	}

	// Once applied, repeating the same dims must short-circuit again.
	n := ff.resizeCallCount()
	m.ResizeTask(context.Background(), "task-A", 83, 45)
	time.Sleep(50 * time.Millisecond)
	if got := ff.resizeCallCount(); got != n {
		t.Fatalf("resize calls grew %d -> %d after success (dedup failed)", n, got)
	}
}

// TestProxyManager_ResizeTaskRetryUsesLatestDims pins that a retry after
// failure sends the CURRENT desired size, not the size that failed: if
// the pane re-layouts while argus is unreachable, the stale size must be
// dropped in favor of the latest.
func TestProxyManager_ResizeTaskRetryUsesLatestDims(t *testing.T) {
	ff := &fakeFetcher{}
	ff.setResizeErr(errNoActiveSession)
	m := newResizeTestManager(ff)
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 20, 21)
	if calls := waitForResizeCalls(t, ff, 1, time.Second); len(calls) < 1 {
		t.Fatalf("no dispatch attempt recorded")
	}

	// New allocation arrives while argus is still failing.
	m.ResizeTask(context.Background(), "task-A", 83, 45)
	ff.setResizeErr(nil)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ff.mu.Lock()
		var done bool
		if n := len(ff.resizeCalls); n > 0 {
			done = ff.resizeCalls[n-1] == resizeCall{TaskID: "task-A", Cols: 83, Rows: 45}
		}
		ff.mu.Unlock()
		if done {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	ff.mu.Lock()
	defer ff.mu.Unlock()
	t.Fatalf("retry never landed the latest dims; calls = %+v", ff.resizeCalls)
}

// TestProxyManager_ResizeTaskGivesUpAfterMaxAttempts pins the retry
// bound: a task with no live session (argus 404s indefinitely) must not
// be retried forever — the dispatcher stops after resizeMaxAttempts and
// a later ResizeTask call (any dims, even the same) starts a fresh round.
func TestProxyManager_ResizeTaskGivesUpAfterMaxAttempts(t *testing.T) {
	ff := &fakeFetcher{}
	ff.setResizeErr(errNoActiveSession)
	m := newResizeTestManager(ff)
	m.resizeMaxAttempts = 3
	defer m.Close()

	m.ResizeTask(context.Background(), "task-A", 83, 45)

	calls := waitForResizeCalls(t, ff, 3, time.Second)
	time.Sleep(50 * time.Millisecond) // allow any (wrong) extra attempt
	if got := ff.resizeCallCount(); got != 3 {
		t.Fatalf("resize calls = %d, want exactly 3 (max attempts)", got)
	}
	_ = calls

	// A fresh call restarts dispatching because nothing was applied.
	m.ResizeTask(context.Background(), "task-A", 83, 45)
	if got := waitForResizeCalls(t, ff, 4, time.Second); len(got) < 4 {
		t.Fatalf("resize calls = %d, want >= 4 (give-up must not dedupe forever)", len(got))
	}
}

// TestProxyManager_ResizeTaskStopsOnCtxCancel pins that a cancelled
// session context aborts the dispatcher before it sends anything.
func TestProxyManager_ResizeTaskStopsOnCtxCancel(t *testing.T) {
	ff := &fakeFetcher{}
	m := NewProxyManager(ff, nil)
	m.resizeDebounce = 50 * time.Millisecond
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	m.ResizeTask(ctx, "task-A", 83, 45)
	cancel()

	time.Sleep(100 * time.Millisecond)
	if got := ff.resizeCallCount(); got != 0 {
		t.Fatalf("resize calls = %d, want 0 after ctx cancel", got)
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
