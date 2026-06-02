package db

import (
	"context"
	"errors"
	"testing"
)

// GetLatestByRole backs the role-ops latest-binding fallback: archiving a
// task ENDS its binding (end_reason='argus_archived') while preserving the
// argus_task_id, so `s`/`S` and unarchive on archived rows must resolve the
// most recent binding regardless of ended_at.

func TestBindings_GetLatestByRole_ReturnsLiveBinding(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-1", WorktreePath: "/tmp/wt",
	})
	if err != nil {
		t.Fatalf("Bindings.Create: %v", err)
	}

	got, err := d.Bindings.GetLatestByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetLatestByRole: %v", err)
	}
	if got.ID != bnd.ID || got.ArgusTaskID != "task-1" {
		t.Fatalf("GetLatestByRole = %+v, want binding %d task-1", got, bnd.ID)
	}
	if got.EndedAt != nil {
		t.Fatalf("live binding should have ended_at nil, got %v", got.EndedAt)
	}
}

func TestBindings_GetLatestByRole_FallsBackToEndedBinding(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-1", WorktreePath: "/tmp/wt",
	})
	if err := d.Bindings.End(ctx, bnd.ID, "argus_archived"); err != nil {
		t.Fatalf("End: %v", err)
	}

	// The live lookup now misses — the latest lookup must still resolve
	// the ended binding's argus task.
	if _, err := d.Bindings.GetLiveByRole(ctx, role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLiveByRole after End: want ErrNotFound, got %v", err)
	}
	got, err := d.Bindings.GetLatestByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetLatestByRole: %v", err)
	}
	if got.ID != bnd.ID || got.ArgusTaskID != "task-1" {
		t.Fatalf("GetLatestByRole = %+v, want ended binding %d task-1", got, bnd.ID)
	}
	if got.EndedAt == nil || got.EndReason != "argus_archived" {
		t.Fatalf("ended binding fields not surfaced: %+v", got)
	}
}

func TestBindings_GetLatestByRole_PicksMostRecentOfSeveralEnded(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})
	first, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-old", WorktreePath: "/tmp/wt1",
	})
	if err := d.Bindings.End(ctx, first.ID, "completed"); err != nil {
		t.Fatalf("End first: %v", err)
	}
	second, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-new", WorktreePath: "/tmp/wt2",
	})
	if err := d.Bindings.End(ctx, second.ID, "argus_archived"); err != nil {
		t.Fatalf("End second: %v", err)
	}

	got, err := d.Bindings.GetLatestByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("GetLatestByRole: %v", err)
	}
	if got.ArgusTaskID != "task-new" {
		t.Fatalf("GetLatestByRole picked %q, want most recent task-new", got.ArgusTaskID)
	}
}

func TestBindings_GetLatestByRole_NoBindingReturnsErrNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: KindWorker, ArgusProject: "p",
	})

	if _, err := d.Bindings.GetLatestByRole(ctx, role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLatestByRole with no bindings: want ErrNotFound, got %v", err)
	}
}
