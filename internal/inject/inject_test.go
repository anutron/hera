package inject

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/anutron/hera/internal/db"
)

type fakePTY struct {
	mu        sync.Mutex
	writes    [][]byte
	taskIDs   []string
	returnErr error
}

func (f *fakePTY) PostTaskInput(ctx context.Context, taskID string, bytes []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.returnErr != nil {
		return 0, f.returnErr
	}
	f.writes = append(f.writes, append([]byte(nil), bytes...))
	f.taskIDs = append(f.taskIDs, taskID)
	return len(bytes), nil
}

type fakeIdle struct{ idle bool }

func (f fakeIdle) IsIdle(taskID string) bool { return f.idle }

func TestFormatBody(t *testing.T) {
	got := FormatBody("foo-coord", "please review")
	want := "[hera from foo-coord] please review"
	if got != want {
		t.Fatalf("FormatBody = %q, want %q", got, want)
	}
}

func TestInject_IdleSubmits(t *testing.T) {
	pty := &fakePTY{}
	idle := fakeIdle{idle: true}
	in := New(pty, idle)

	mode, err := in.Inject(context.Background(), "task-1", "foo-coord", "ping")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if mode != db.DeliveryIdleSubmit {
		t.Fatalf("mode = %s, want %s", mode, db.DeliveryIdleSubmit)
	}
	if len(pty.writes) != 1 {
		t.Fatalf("got %d writes, want 1", len(pty.writes))
	}
	got := string(pty.writes[0])
	want := "[hera from foo-coord] ping\r"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if pty.taskIDs[0] != "task-1" {
		t.Fatalf("taskID = %q", pty.taskIDs[0])
	}
}

func TestInject_BusyBuffersWithoutNewline(t *testing.T) {
	pty := &fakePTY{}
	idle := fakeIdle{idle: false}
	in := New(pty, idle)

	mode, err := in.Inject(context.Background(), "task-1", "foo-coord", "ping")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if mode != db.DeliveryBusyBuffer {
		t.Fatalf("mode = %s, want %s", mode, db.DeliveryBusyBuffer)
	}
	got := string(pty.writes[0])
	want := "[hera from foo-coord] ping"
	if got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestInject_PropagatesPTYErrors(t *testing.T) {
	pty := &fakePTY{returnErr: errors.New("boom")}
	in := New(pty, fakeIdle{idle: true})
	mode, err := in.Inject(context.Background(), "task-1", "foo", "ping")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if mode != db.DeliveryPending {
		t.Fatalf("on error, mode should remain pending, got %s", mode)
	}
}

// Default constructor value for autoInjectEnabled is true (v1 behavior).
func TestInject_DefaultAutoInjectEnabledIsTrue(t *testing.T) {
	pty := &fakePTY{}
	in := New(pty, fakeIdle{idle: true})
	mode, err := in.Inject(context.Background(), "task-1", "foo", "ping")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if mode != db.DeliveryIdleSubmit {
		t.Fatalf("default auto-inject should be true (idle_submit), got %s", mode)
	}
	if got := string(pty.writes[0]); got != "[hera from foo] ping\r" {
		t.Fatalf("default auto-inject should terminate idle path with CR; got %q", got)
	}
}

// Spec scenario: "Auto-inject off, idle recipient still busy-buffers".
// With auto-inject disabled, an idle recipient must receive the formatted
// body without a trailing newline, and the delivery mode must be busy_buffer.
func TestInject_AutoInjectDisabledForcesBusyBufferEvenWhenIdle(t *testing.T) {
	pty := &fakePTY{}
	in := New(pty, fakeIdle{idle: true})
	in.SetAutoInjectEnabled(false)

	mode, err := in.Inject(context.Background(), "task-1", "foo-coord", "ping")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if mode != db.DeliveryBusyBuffer {
		t.Fatalf("with auto-inject off and idle recipient, mode should be busy_buffer, got %s", mode)
	}
	if got := string(pty.writes[0]); got != "[hera from foo-coord] ping" {
		t.Fatalf("body should have no trailing newline; got %q", got)
	}
}

// Spec scenario: "Auto-inject toggles back to true, behavior restored".
func TestInject_AutoInjectReEnabledRestoresIdleSubmit(t *testing.T) {
	pty := &fakePTY{}
	in := New(pty, fakeIdle{idle: true})

	in.SetAutoInjectEnabled(false)
	mode, err := in.Inject(context.Background(), "task-1", "foo-coord", "first")
	if err != nil {
		t.Fatalf("Inject (off): %v", err)
	}
	if mode != db.DeliveryBusyBuffer {
		t.Fatalf("first call (auto-inject off) should be busy_buffer, got %s", mode)
	}

	in.SetAutoInjectEnabled(true)
	mode, err = in.Inject(context.Background(), "task-1", "foo-coord", "second")
	if err != nil {
		t.Fatalf("Inject (back-on): %v", err)
	}
	if mode != db.DeliveryIdleSubmit {
		t.Fatalf("after re-enabling, mode should be idle_submit, got %s", mode)
	}
	if got := string(pty.writes[1]); got != "[hera from foo-coord] second\r" {
		t.Fatalf("re-enabled body should have trailing CR; got %q", got)
	}
}

// Auto-inject ON but recipient busy still busy_buffers (unchanged v1 path).
func TestInject_AutoInjectOnButBusyStillBuffers(t *testing.T) {
	pty := &fakePTY{}
	in := New(pty, fakeIdle{idle: false})
	in.SetAutoInjectEnabled(true)

	mode, err := in.Inject(context.Background(), "task-1", "foo", "ping")
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if mode != db.DeliveryBusyBuffer {
		t.Fatalf("busy recipient should always busy_buffer, got %s", mode)
	}
	if got := string(pty.writes[0]); got != "[hera from foo] ping" {
		t.Fatalf("busy path body should have no newline; got %q", got)
	}
}
