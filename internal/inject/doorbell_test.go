package inject

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/db"
)

// -- fakes --

type fakeNudgeStore struct {
	mu       sync.Mutex
	stale    []*db.Message
	recorded [][]int64
	staleErr error
	nudgeErr error
}

func (f *fakeNudgeStore) UnreadIdleSubmitStale(_ context.Context, _, _ time.Time, _ int) ([]*db.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*db.Message(nil), f.stale...), f.staleErr
}

func (f *fakeNudgeStore) RecordNudge(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, append([]int64(nil), ids...))
	return f.nudgeErr
}

func (f *fakeNudgeStore) totalRecorded() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int64
	for _, ids := range f.recorded {
		out = append(out, ids...)
	}
	return out
}

type fakeBindingLookup struct {
	mu       sync.Mutex
	bindings map[int64]*db.Binding
}

func (f *fakeBindingLookup) GetLiveByRole(_ context.Context, roleID int64) (*db.Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.bindings[roleID]; ok {
		return b, nil
	}
	return nil, db.ErrNotFound
}

// -- tests --

func TestFormatDoorbell_Singular(t *testing.T) {
	msgs := []*db.Message{{ID: 5, Tldr: "ping from worker"}}
	got := FormatDoorbell(msgs)
	want := "[hera doorbell] msg #5 — ping from worker — call hera_inbox\r"
	if got != want {
		t.Fatalf("FormatDoorbell(1 msg) = %q, want %q", got, want)
	}
}

func TestFormatDoorbell_Plural(t *testing.T) {
	msgs := []*db.Message{{ID: 1}, {ID: 2}, {ID: 3}}
	got := FormatDoorbell(msgs)
	if !strings.HasPrefix(got, "[hera doorbell] 3 unread messages") {
		t.Fatalf("FormatDoorbell(3 msgs) = %q, want plural", got)
	}
	if !strings.HasSuffix(got, "\r") {
		t.Fatalf("FormatDoorbell should end with CR, got %q", got)
	}
}

func TestFormatDoorbell_DoesNotContainPayload(t *testing.T) {
	msgs := []*db.Message{{ID: 1}, {ID: 2}}
	got := FormatDoorbell(msgs)
	if strings.Contains(got, "body") || strings.Contains(got, "from") {
		t.Fatalf("FormatDoorbell should not include message body or sender, got %q", got)
	}
}

func TestDeliveryWatcher_FiresDoorbellForStaleMsg(t *testing.T) {
	staleMsg := &db.Message{ID: 10, ToRoleID: 42}
	store := &fakeNudgeStore{stale: []*db.Message{staleMsg}}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{
		42: {ArgusTaskID: "task-42"},
	}}
	pty := &fakePTY{}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	if err := w.scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(pty.writes) != 1 {
		t.Fatalf("expected 1 PTY write, got %d", len(pty.writes))
	}
	got := string(pty.writes[0])
	if !strings.HasPrefix(got, "[hera doorbell]") {
		t.Fatalf("PTY write should be a doorbell, got %q", got)
	}
	if !strings.HasSuffix(got, "\r") {
		t.Fatalf("doorbell should end with CR, got %q", got)
	}
	if pty.taskIDs[0] != "task-42" {
		t.Fatalf("wrote to wrong task: %q", pty.taskIDs[0])
	}
	recorded := store.totalRecorded()
	if len(recorded) != 1 || recorded[0] != 10 {
		t.Fatalf("RecordNudge called with %v, want [10]", recorded)
	}
}

func TestDeliveryWatcher_DoesNotReInjectOriginalBody(t *testing.T) {
	staleMsg := &db.Message{ID: 10, ToRoleID: 42, Body: "super secret payload"}
	store := &fakeNudgeStore{stale: []*db.Message{staleMsg}}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{
		42: {ArgusTaskID: "task-42"},
	}}
	pty := &fakePTY{}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	if err := w.scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	for _, write := range pty.writes {
		if strings.Contains(string(write), "super secret payload") {
			t.Fatalf("doorbell must not re-inject original body; got %q", string(write))
		}
	}
}

func TestDeliveryWatcher_AggregatesMultipleMsgsSameRecipient(t *testing.T) {
	msgs := []*db.Message{
		{ID: 1, ToRoleID: 42},
		{ID: 2, ToRoleID: 42},
		{ID: 3, ToRoleID: 42},
	}
	store := &fakeNudgeStore{stale: msgs}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{
		42: {ArgusTaskID: "task-42"},
	}}
	pty := &fakePTY{}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	if err := w.scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// One PTY write for three messages (aggregated per recipient).
	if len(pty.writes) != 1 {
		t.Fatalf("expected 1 PTY write (aggregated), got %d", len(pty.writes))
	}
	// Doorbell count = 3.
	if !strings.Contains(string(pty.writes[0]), "3 unread") {
		t.Fatalf("doorbell count should be 3, got %q", string(pty.writes[0]))
	}
	// RecordNudge called once with all 3 IDs.
	recorded := store.totalRecorded()
	if len(recorded) != 3 {
		t.Fatalf("RecordNudge should record 3 IDs, got %v", recorded)
	}
}

func TestDeliveryWatcher_SeparateDoorbellsPerRecipient(t *testing.T) {
	msgs := []*db.Message{
		{ID: 1, ToRoleID: 10},
		{ID: 2, ToRoleID: 20},
	}
	store := &fakeNudgeStore{stale: msgs}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{
		10: {ArgusTaskID: "task-10"},
		20: {ArgusTaskID: "task-20"},
	}}
	pty := &fakePTY{}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	if err := w.scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(pty.writes) != 2 {
		t.Fatalf("expected 2 PTY writes (one per recipient), got %d", len(pty.writes))
	}
	taskSet := map[string]bool{}
	for _, id := range pty.taskIDs {
		taskSet[id] = true
	}
	if !taskSet["task-10"] || !taskSet["task-20"] {
		t.Fatalf("expected writes to task-10 and task-20, got %v", pty.taskIDs)
	}
}

func TestDeliveryWatcher_SkipsUnboundRecipients(t *testing.T) {
	staleMsg := &db.Message{ID: 10, ToRoleID: 99} // role 99 has no live binding
	store := &fakeNudgeStore{stale: []*db.Message{staleMsg}}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{}} // empty
	pty := &fakePTY{}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	if err := w.scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(pty.writes) != 0 {
		t.Fatalf("should not write when recipient unbound, got %d writes", len(pty.writes))
	}
	if len(store.totalRecorded()) != 0 {
		t.Fatalf("should not record nudge when recipient unbound")
	}
}

func TestDeliveryWatcher_NoStaleMsgs_NoWrite(t *testing.T) {
	store := &fakeNudgeStore{stale: nil}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{}}
	pty := &fakePTY{}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	if err := w.scan(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(pty.writes) != 0 {
		t.Fatalf("expected no writes with empty stale list, got %d", len(pty.writes))
	}
}

func TestDeliveryWatcher_PTYErrorContinues(t *testing.T) {
	msgs := []*db.Message{
		{ID: 1, ToRoleID: 10},
		{ID: 2, ToRoleID: 20},
	}
	store := &fakeNudgeStore{stale: msgs}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{
		10: {ArgusTaskID: "task-10"},
		20: {ArgusTaskID: "task-20"},
	}}
	// PTY fails on first write, succeeds on second.
	callN := 0
	pty := &fakePTYFunc{fn: func(taskID string, _ []byte) error {
		callN++
		if callN == 1 {
			return fmt.Errorf("boom")
		}
		return nil
	}}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	// Should not return error even when one PTY write fails.
	if err := w.scan(context.Background()); err != nil {
		t.Fatalf("scan should not propagate PTY errors, got: %v", err)
	}
	// The successful write still records its nudge.
	if len(store.recorded) != 1 {
		t.Fatalf("expected 1 RecordNudge call (for successful PTY write), got %d", len(store.recorded))
	}
}

func TestDeliveryWatcher_RunExitsOnCtxCancel(t *testing.T) {
	store := &fakeNudgeStore{}
	bindings := &fakeBindingLookup{bindings: map[int64]*db.Binding{}}
	pty := &fakePTY{}
	w := NewDeliveryWatcher(store, bindings, pty, 30*time.Second, 30*time.Second, 5, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit after ctx cancel within 3s")
	}
}

// fakePTYFunc lets tests supply a custom PostTaskInput implementation.
type fakePTYFunc struct {
	mu      sync.Mutex
	taskIDs []string
	fn      func(taskID string, bytes []byte) error
}

func (f *fakePTYFunc) PostTaskInput(_ context.Context, taskID string, bytes []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskIDs = append(f.taskIDs, taskID)
	if f.fn != nil {
		if err := f.fn(taskID, bytes); err != nil {
			return 0, err
		}
	}
	return len(bytes), nil
}
