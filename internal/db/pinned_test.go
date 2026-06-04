package db

import (
	"context"
	"testing"
	"time"
)

// Tests for the Pin / Unpin DAO verbs and the pin/archive mutual-exclusion
// invariant (Story 1 of rail-sections): Pin sets pinned_at + clears
// archived_at, Archive clears pinned_at, Unpin clears pinned_at.

func TestOrchestrators_Pin_SetsPinnedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")

	before := time.Now().UTC()
	if err := d.Orchestrators.Pin(ctx, orch.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	got, err := d.Orchestrators.GetByID(ctx, orch.ID)
	if err != nil {
		t.Fatalf("GetByID after Pin: %v", err)
	}
	if got.PinnedAt == nil {
		t.Fatalf("pinned_at should be non-nil after Pin")
	}
	if got.PinnedAt.Before(before.Add(-time.Second)) || got.PinnedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("pinned_at = %v, want within the last second", got.PinnedAt)
	}
}

func TestOrchestrators_Pin_ClearsArchivedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := d.Orchestrators.Pin(ctx, orch.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	got, _ := d.Orchestrators.GetByID(ctx, orch.ID)
	if got.PinnedAt == nil {
		t.Fatalf("pinned_at should be set after Pin")
	}
	if got.ArchivedAt != nil {
		t.Fatalf("archived_at must be cleared by Pin (mutual exclusivity), got %v", got.ArchivedAt)
	}
}

func TestOrchestrators_Archive_ClearsPinnedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Pin(ctx, orch.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := d.Orchestrators.Archive(ctx, orch.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, _ := d.Orchestrators.GetByID(ctx, orch.ID)
	if got.ArchivedAt == nil {
		t.Fatalf("archived_at should be set after Archive")
	}
	if got.PinnedAt != nil {
		t.Fatalf("pinned_at must be cleared by Archive (mutual exclusivity), got %v", got.PinnedAt)
	}
}

func TestOrchestrators_Unpin_ClearsPinnedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Pin(ctx, orch.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := d.Orchestrators.Unpin(ctx, orch.ID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	got, _ := d.Orchestrators.GetByID(ctx, orch.ID)
	if got.PinnedAt != nil {
		t.Fatalf("pinned_at should be nil after Unpin, got %v", got.PinnedAt)
	}
}

func TestOrchestrators_Pin_PreservesIdempotencyTimestamp(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	if err := d.Orchestrators.Pin(ctx, orch.ID); err != nil {
		t.Fatalf("first Pin: %v", err)
	}
	first, _ := d.Orchestrators.GetByID(ctx, orch.ID)
	original := *first.PinnedAt

	time.Sleep(10 * time.Millisecond)
	if err := d.Orchestrators.Pin(ctx, orch.ID); err != nil {
		t.Fatalf("re-Pin: %v", err)
	}
	again, _ := d.Orchestrators.GetByID(ctx, orch.ID)
	if !again.PinnedAt.Equal(original) {
		t.Fatalf("re-Pin changed pinned_at: was %v, now %v", original, again.PinnedAt)
	}
}

func newTestRole(t *testing.T, d *DB, orchID int64, name string) *Role {
	t.Helper()
	r, err := d.Roles.Create(context.Background(), CreateRoleInput{
		OrchestratorID: orchID, Name: name, Kind: KindWorker, ArgusProject: "Hera",
	})
	if err != nil {
		t.Fatalf("create role %q: %v", name, err)
	}
	return r
}

func TestRoles_Pin_SetsPinnedAtAndClearsArchived(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role := newTestRole(t, d, orch.ID, "w1")

	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := d.Roles.Pin(ctx, role.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	got, _ := d.Roles.GetByID(ctx, role.ID)
	if got.PinnedAt == nil {
		t.Fatalf("pinned_at should be set after Pin")
	}
	if got.ArchivedAt != nil {
		t.Fatalf("archived_at must be cleared by Pin, got %v", got.ArchivedAt)
	}
}

func TestRoles_Archive_ClearsPinnedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role := newTestRole(t, d, orch.ID, "w1")

	if err := d.Roles.Pin(ctx, role.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := d.Roles.Archive(ctx, role.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got, _ := d.Roles.GetByID(ctx, role.ID)
	if got.ArchivedAt == nil {
		t.Fatalf("archived_at should be set after Archive")
	}
	if got.PinnedAt != nil {
		t.Fatalf("pinned_at must be cleared by Archive, got %v", got.PinnedAt)
	}
}

func TestRoles_Unpin_ClearsPinnedAt(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role := newTestRole(t, d, orch.ID, "w1")

	if err := d.Roles.Pin(ctx, role.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if err := d.Roles.Unpin(ctx, role.ID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	got, _ := d.Roles.GetByID(ctx, role.ID)
	if got.PinnedAt != nil {
		t.Fatalf("pinned_at should be nil after Unpin, got %v", got.PinnedAt)
	}
}

// TestRoles_Pin_RoundtripsThroughListInclusive proves the pinned_at column is
// selected + scanned by the list path the rail uses (ListByOrchestratorInclusive).
func TestRoles_Pin_RoundtripsThroughListInclusive(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role := newTestRole(t, d, orch.ID, "w1")
	if err := d.Roles.Pin(ctx, role.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	roles, err := d.Roles.ListByOrchestratorInclusive(ctx, orch.ID)
	if err != nil {
		t.Fatalf("ListInclusive: %v", err)
	}
	if len(roles) != 1 || roles[0].PinnedAt == nil {
		t.Fatalf("pinned_at not carried through ListByOrchestratorInclusive: %+v", roles)
	}
}
