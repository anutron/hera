package db

import (
	"context"
	"errors"
	"testing"
)

// The multi-binding contract introduced by migration 0004 lets a single
// argus task hold one live binding per orchestrator. These tests pin
// the new DAO surface: derived orchestrator_id, ListLiveByTaskID,
// GetLiveByTaskAndOrchestrator, ErrAmbiguous on the single-row
// variant, and the (task, orchestrator) partial-unique-index.

func TestBindings_CreateDerivesOrchestratorIDFromRole(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-1", WorktreePath: "/wt-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if bnd.OrchestratorID != orch.ID {
		t.Fatalf("OrchestratorID: got %d, want %d (derived from role)", bnd.OrchestratorID, orch.ID)
	}
}

func TestBindings_MultiBindingSameTaskDifferentOrchestrators(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	orchA, _ := d.Orchestrators.Create(ctx, "A")
	orchB, _ := d.Orchestrators.Create(ctx, "B")
	roleA, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "worker", Kind: KindWorker, ArgusProject: "p",
	})
	roleB, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchB.ID, Name: "coord", Kind: KindCoordinator, ArgusProject: "p",
	})

	bndA, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: roleA.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	bndB, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: roleB.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})
	if err != nil {
		t.Fatalf("Create B (same task, different orch): %v", err)
	}
	if bndA.ID == bndB.ID {
		t.Fatalf("expected distinct binding rows")
	}

	all, err := d.Bindings.ListLiveByTaskID(ctx, "t")
	if err != nil {
		t.Fatalf("ListLiveByTaskID: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListLiveByTaskID: got %d, want 2", len(all))
	}
}

func TestBindings_GetLiveByTaskIDReturnsAmbiguousWhenMulti(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orchA, _ := d.Orchestrators.Create(ctx, "A")
	orchB, _ := d.Orchestrators.Create(ctx, "B")
	rA, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "r", Kind: KindWorker, ArgusProject: "p",
	})
	rB, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchB.ID, Name: "r", Kind: KindCoordinator, ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: rA.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})
	_, _ = d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: rB.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})

	_, err := d.Bindings.GetLiveByTaskID(ctx, "t")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("GetLiveByTaskID with 2 bindings: got %v, want ErrAmbiguous", err)
	}
}

func TestBindings_GetLiveByTaskAndOrchestrator(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orchA, _ := d.Orchestrators.Create(ctx, "A")
	orchB, _ := d.Orchestrators.Create(ctx, "B")
	rA, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "r", Kind: KindWorker, ArgusProject: "p",
	})
	rB, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchB.ID, Name: "r", Kind: KindCoordinator, ArgusProject: "p",
	})
	bA, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: rA.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})
	bB, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: rB.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})

	gotA, err := d.Bindings.GetLiveByTaskAndOrchestrator(ctx, "t", orchA.ID)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	if gotA.ID != bA.ID {
		t.Fatalf("Get A: got binding %d, want %d", gotA.ID, bA.ID)
	}
	gotB, err := d.Bindings.GetLiveByTaskAndOrchestrator(ctx, "t", orchB.ID)
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}
	if gotB.ID != bB.ID {
		t.Fatalf("Get B: got binding %d, want %d", gotB.ID, bB.ID)
	}

	_, err = d.Bindings.GetLiveByTaskAndOrchestrator(ctx, "t", 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get unknown orch: got %v, want ErrNotFound", err)
	}
}

func TestBindings_SecondBindingSameTaskSameOrchestratorRejected(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "A")
	r1, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r1", Kind: KindWorker, ArgusProject: "p",
	})
	r2, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r2", Kind: KindWorker, ArgusProject: "p",
	})
	if _, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: r1.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Second binding to the same task under the same orchestrator
	// (different role) must violate the partial unique index.
	_, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: r2.ID, ArgusTaskID: "t", WorktreePath: "/wt2",
	})
	if err == nil {
		t.Fatalf("expected unique-index violation, got nil")
	}
}

func TestBindings_GetLiveByTaskID_SingleStillReturnsRow(t *testing.T) {
	// Back-compat: with exactly one binding, the orchestrator-agnostic
	// single-row getter still returns the row (no ambiguity).
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "A")
	r, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: r.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})
	got, err := d.Bindings.GetLiveByTaskID(ctx, "t")
	if err != nil {
		t.Fatalf("GetLiveByTaskID: %v", err)
	}
	if got.ID != bnd.ID {
		t.Fatalf("id mismatch: got %d, want %d", got.ID, bnd.ID)
	}
}

func TestMigration0004_BackfillsOrchestratorID(t *testing.T) {
	// Simulates the pre-0004 state: a binding row whose orchestrator_id is
	// NULL. The migration's UPDATE clause should populate it from the
	// role's orchestrator_id.
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "A")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t", WorktreePath: "/wt",
	})
	// Force the column to NULL, then re-run the migration's backfill SQL.
	if _, err := d.Raw().ExecContext(ctx,
		`UPDATE bindings SET orchestrator_id = NULL WHERE id = ?`, bnd.ID,
	); err != nil {
		t.Fatalf("force NULL: %v", err)
	}
	if _, err := d.Raw().ExecContext(ctx,
		`UPDATE bindings
		    SET orchestrator_id = (SELECT orchestrator_id FROM roles WHERE roles.id = bindings.role_id)`,
	); err != nil {
		t.Fatalf("rerun backfill: %v", err)
	}
	var got int64
	if err := d.Raw().QueryRowContext(ctx,
		`SELECT orchestrator_id FROM bindings WHERE id = ?`, bnd.ID,
	).Scan(&got); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got != orch.ID {
		t.Fatalf("backfilled orchestrator_id = %d, want %d", got, orch.ID)
	}
}

func TestBindings_ListLiveByTaskID_EmptyForUnknownTask(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	got, err := d.Bindings.ListLiveByTaskID(ctx, "no-such-task")
	if err != nil {
		t.Fatalf("ListLiveByTaskID: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d rows", len(got))
	}
}
