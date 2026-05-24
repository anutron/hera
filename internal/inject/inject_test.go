package inject

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/anutron/ludwig/internal/db"
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
	want := "[ludwig from foo-coord] please review"
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
	want := "[ludwig from foo-coord] ping\n"
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
	want := "[ludwig from foo-coord] ping"
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
