package view

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anutron/hera/internal/db"
)

// An orchestrator insert reaches the rail's refresh callback within
// the debounce window plus a small slop. The design requires ~100 ms.
func TestRailRefresher_InsertOrchestratorTriggersRefresh(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	var refreshes atomic.Int32
	r := NewRailRefresherWith(d.Events, func() {
		refreshes.Add(1)
	}, 50*time.Millisecond)
	defer r.Stop()

	if _, err := d.Orchestrators.Create(ctx, "foo"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if refreshes.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := refreshes.Load(); got < 1 {
		t.Fatalf("refresh count = %d, want at least 1 within 150 ms", got)
	}
}

// A role or binding insert under an existing orchestrator also fires
// the rail refresh — covers all three watched entities.
func TestRailRefresher_RoleAndBindingTriggerRefresh(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")

	var refreshes atomic.Int32
	r := NewRailRefresherWith(d.Events, func() { refreshes.Add(1) }, 30*time.Millisecond)
	defer r.Stop()

	role, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator,
		ArgusProject: "p",
	})
	time.Sleep(80 * time.Millisecond)
	if refreshes.Load() == 0 {
		t.Fatal("role insert did not trigger refresh")
	}

	before := refreshes.Load()
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T", WorktreePath: "/tmp/x",
	})
	time.Sleep(80 * time.Millisecond)
	if refreshes.Load() <= before {
		t.Fatalf("binding insert did not trigger additional refresh; %d -> %d", before, refreshes.Load())
	}
}

// A burst of three writes inside one debounce window should coalesce
// into a single refresh.
func TestRailRefresher_BurstCoalescedIntoOneRefresh(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	var refreshes atomic.Int32
	r := NewRailRefresherWith(d.Events, func() { refreshes.Add(1) }, 100*time.Millisecond)
	defer r.Stop()

	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: db.KindCoordinator,
		ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T-1", WorktreePath: "/tmp/x",
	})

	// Wait long enough for the debounce window to elapse plus slop.
	time.Sleep(200 * time.Millisecond)
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("burst coalesced refresh count = %d, want exactly 1", got)
	}
}

// When the broadcaster sees no events for an extended interval, the
// refresh callback must NOT fire — the rail must not poll the DB on a
// timer. This is the "no polling timer" requirement from the spec.
func TestRailRefresher_IdleProducesNoRefresh(t *testing.T) {
	bc := db.NewBroadcaster()
	defer bc.Close()

	var refreshes atomic.Int32
	r := NewRailRefresherWith(bc, func() { refreshes.Add(1) }, 50*time.Millisecond)
	defer r.Stop()

	time.Sleep(1 * time.Second)

	if got := refreshes.Load(); got != 0 {
		t.Fatalf("idle refresh count = %d, want 0 (rail must not poll)", got)
	}
}

// A second burst that arrives after the first one has fired must also
// produce a refresh. The debounce must not latch.
func TestRailRefresher_SecondBurstFiresAgain(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	var refreshes atomic.Int32
	r := NewRailRefresherWith(d.Events, func() { refreshes.Add(1) }, 40*time.Millisecond)
	defer r.Stop()

	if _, err := d.Orchestrators.Create(ctx, "one"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	first := refreshes.Load()
	if first != 1 {
		t.Fatalf("first burst refresh count = %d, want 1", first)
	}

	if _, err := d.Orchestrators.Create(ctx, "two"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if got := refreshes.Load(); got != 2 {
		t.Fatalf("second burst refresh count = %d, want 2", got)
	}
}

// Stop is idempotent and unblocks any pending refresh.
func TestRailRefresher_StopIsIdempotent(t *testing.T) {
	bc := db.NewBroadcaster()
	defer bc.Close()
	r := NewRailRefresherWith(bc, func() {}, 50*time.Millisecond)
	r.Stop()
	r.Stop() // must not panic or deadlock
}

// Stopping the refresher must NOT call refresh anymore even if events
// land on the channel just before/after Stop.
func TestRailRefresher_StopHaltsFurtherRefreshes(t *testing.T) {
	bc := db.NewBroadcaster()
	defer bc.Close()

	var refreshes atomic.Int32
	r := NewRailRefresherWith(bc, func() { refreshes.Add(1) }, 30*time.Millisecond)
	r.Stop()

	bc.Emit(db.Event{Entity: db.EntityOrchestrator, Op: db.OpInsert, ID: 1})
	time.Sleep(100 * time.Millisecond)

	if got := refreshes.Load(); got != 0 {
		t.Fatalf("post-Stop refresh count = %d, want 0", got)
	}
}
