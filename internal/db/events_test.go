package db

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBroadcaster_FanOutToMultipleSubscribers(t *testing.T) {
	b := NewBroadcaster()
	defer b.Close()

	ch1, cancel1 := b.Subscribe()
	defer cancel1()
	ch2, cancel2 := b.Subscribe()
	defer cancel2()

	want := Event{Entity: EntityOrchestrator, Op: OpInsert, ID: 42}
	b.Emit(want)

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("subscriber %d got %+v, want %+v", i, got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d timed out waiting for event", i)
		}
	}
}

func TestBroadcaster_SlowSubscriberDoesNotBlockProducer(t *testing.T) {
	b := NewBroadcaster()
	defer b.Close()

	// Register a subscriber but never read from it. With buffer size
	// 16 (default), the 17th Emit would block if drop-on-full weren't
	// implemented.
	_, cancel := b.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			b.Emit(Event{Entity: EntityRole, Op: OpInsert, ID: int64(i)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked on slow subscriber; want non-blocking drop semantics")
	}
}

func TestBroadcaster_CancelClosesChannel(t *testing.T) {
	b := NewBroadcaster()
	defer b.Close()

	ch, cancel := b.Subscribe()
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after cancel; got value")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not close channel within deadline")
	}
}

func TestBroadcaster_CancelStopsDelivery(t *testing.T) {
	b := NewBroadcaster()
	defer b.Close()

	ch, cancel := b.Subscribe()
	cancel()

	b.Emit(Event{Entity: EntityRole, Op: OpInsert, ID: 1})

	// After cancel, channel is closed; subsequent receives return zero value.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel; got an event after cancel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel not closed after cancel + emit")
	}
}

func TestBroadcaster_CloseClosesAllSubscribers(t *testing.T) {
	b := NewBroadcaster()
	ch1, _ := b.Subscribe()
	ch2, _ := b.Subscribe()

	b.Close()

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("subscriber %d not closed after Broadcaster.Close", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d not closed within deadline", i)
		}
	}
}

func TestBroadcaster_EmitAfterCloseIsNoop(t *testing.T) {
	b := NewBroadcaster()
	b.Close()
	// Must not panic.
	b.Emit(Event{Entity: EntityBinding, Op: OpInsert, ID: 1})
}

func TestBroadcaster_ConcurrentSubscribeAndEmit(t *testing.T) {
	b := NewBroadcaster()
	defer b.Close()

	var wg sync.WaitGroup
	const N = 50

	// Concurrent subscribers
	cancels := make([]func(), N)
	for i := 0; i < N; i++ {
		ch, cancel := b.Subscribe()
		cancels[i] = cancel
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range ch {
				// drain
			}
		}()
	}

	// Concurrent emitters
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Emit(Event{Entity: EntityRole, Op: OpUpdate, ID: int64(id*1000 + j)})
			}
		}(i)
	}

	// Once emitters finish, cancel subscribers so their goroutines exit.
	go func() {
		time.Sleep(200 * time.Millisecond)
		for _, c := range cancels {
			c()
		}
	}()

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent emit/subscribe deadlocked")
	}
}

func TestDB_OrchestratorCreateEmitsInsertEvent(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	ch, cancel := d.Events.Subscribe()
	defer cancel()

	orch, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := receiveOne(t, ch)
	if got.Entity != EntityOrchestrator || got.Op != OpInsert || got.ID != orch.ID {
		t.Fatalf("event = %+v; want orchestrator/insert/%d", got, orch.ID)
	}
}

func TestDB_OrchestratorCreateIdempotentDoesNotEmit(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if _, err := d.Orchestrators.Create(ctx, "foo"); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	ch, cancel := d.Events.Subscribe()
	defer cancel()

	if _, err := d.Orchestrators.Create(ctx, "foo"); err != nil {
		t.Fatalf("second Create: %v", err)
	}

	expectNoEvent(t, ch, 100*time.Millisecond)
}

func TestDB_RoleCreateEmitsInsertEvent(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")

	ch, cancel := d.Events.Subscribe()
	defer cancel()

	role, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "p",
	})
	if err != nil {
		t.Fatalf("Create role: %v", err)
	}

	got := receiveOne(t, ch)
	if got.Entity != EntityRole || got.Op != OpInsert || got.ID != role.ID {
		t.Fatalf("event = %+v; want role/insert/%d", got, role.ID)
	}
}

func TestDB_RoleCreateIdempotentDoesNotEmit(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if _, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "p",
	}); err != nil {
		t.Fatalf("first Create role: %v", err)
	}

	ch, cancel := d.Events.Subscribe()
	defer cancel()

	if _, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "p",
	}); err != nil {
		t.Fatalf("second Create role: %v", err)
	}

	expectNoEvent(t, ch, 100*time.Millisecond)
}

func TestDB_BindingCreateEmitsInsertEvent(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "p",
	})

	ch, cancel := d.Events.Subscribe()
	defer cancel()

	bnd, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T-1", WorktreePath: "/tmp/wt",
	})
	if err != nil {
		t.Fatalf("Create binding: %v", err)
	}

	got := receiveOne(t, ch)
	if got.Entity != EntityBinding || got.Op != OpInsert || got.ID != bnd.ID {
		t.Fatalf("event = %+v; want binding/insert/%d", got, bnd.ID)
	}
}

func TestDB_BindingEndEmitsUpdateEvent(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T-1", WorktreePath: "/tmp/wt",
	})

	ch, cancel := d.Events.Subscribe()
	defer cancel()

	if err := d.Bindings.End(ctx, bnd.ID, "user_deleted"); err != nil {
		t.Fatalf("End: %v", err)
	}

	got := receiveOne(t, ch)
	if got.Entity != EntityBinding || got.Op != OpUpdate || got.ID != bnd.ID {
		t.Fatalf("event = %+v; want binding/update/%d", got, bnd.ID)
	}
}

func TestDB_BindingEndOnAlreadyEndedDoesNotEmit(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coordinator", Kind: KindCoordinator,
		ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T-1", WorktreePath: "/tmp/wt",
	})
	_ = d.Bindings.End(ctx, bnd.ID, "first")

	ch, cancel := d.Events.Subscribe()
	defer cancel()

	if err := d.Bindings.End(ctx, bnd.ID, "second"); err == nil {
		t.Fatal("expected ErrNotFound on double-end")
	}

	expectNoEvent(t, ch, 100*time.Millisecond)
}

func receiveOne(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	return Event{}
}

func expectNoEvent(t *testing.T, ch <-chan Event, within time.Duration) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("expected no event within %s; got %+v", within, e)
	case <-time.After(within):
	}
}

// Sanity check that pre-existing tests' use of atomic.Int32 will compile
// in this package — purely a build-time guard.
var _ atomic.Int32
