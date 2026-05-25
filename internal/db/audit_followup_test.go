package db

import (
	"context"
	"strings"
	"testing"
)

// These tests close the gaps the data+argus ralph reviewer identified:
// spec scenarios that had implementations but no asserting tests, plus
// the new partial-unique-index defense.

func TestBindings_CoordinatorTaskArchived_RoleSurvives(t *testing.T) {
	// Spec scenario: "Coordinator task archived, role survives"
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	coord, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "c", Kind: KindCoordinator, ArgusProject: "p",
		Mission: "build it", Constraints: "by friday",
	})
	worker, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/tmp/coord",
	})
	// Seed a message addressed to the coordinator so we can verify the
	// inbox is preserved across the binding end.
	worker2coord := coord.ID
	msg, _ := d.Messages.Create(ctx, CreateMessageInput{
		FromRoleID: worker.ID, ToRoleID: worker2coord, Body: "hi",
	})

	// End the binding (simulating task.archived).
	if err := d.Bindings.End(ctx, bnd.ID, "argus_archived"); err != nil {
		t.Fatalf("Bindings.End: %v", err)
	}

	// Role row must still exist with original attributes.
	gotRole, err := d.Roles.GetByID(ctx, coord.ID)
	if err != nil {
		t.Fatalf("Roles.GetByID: %v", err)
	}
	if gotRole.Mission != "build it" || gotRole.Constraints != "by friday" {
		t.Fatalf("role attributes wiped: %+v", gotRole)
	}

	// Message must still exist and still be unread.
	gotMsg, err := d.Messages.GetByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("Messages.GetByID: %v", err)
	}
	if gotMsg.ReadAt != nil {
		t.Fatalf("message read state should be unchanged after binding end")
	}
}

func TestBindings_SameRoleReboundAcrossIncarnations(t *testing.T) {
	// Spec scenario: "Same role rebound across multiple incarnations"
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})

	// First incarnation.
	bnd1, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T1", WorktreePath: "/tmp/wt1",
	})
	if err := d.Bindings.End(ctx, bnd1.ID, "argus_archived"); err != nil {
		t.Fatalf("End: %v", err)
	}

	// Second incarnation for the same role.
	bnd2, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "T2", WorktreePath: "/tmp/wt2",
	})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}

	all, err := d.Bindings.ListByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListByRole: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 binding rows, got %d", len(all))
	}
	// ListByRole orders by started_at desc; bnd2 should be first.
	if all[0].ID != bnd2.ID {
		t.Fatalf("expected newer binding first, got %d", all[0].ID)
	}
	if all[0].EndedAt != nil {
		t.Fatalf("newer binding should still be live")
	}
	if all[1].EndedAt == nil {
		t.Fatalf("older binding should be ended")
	}
}

func TestRoles_ArgusProjectPreservedAcrossCreate(t *testing.T) {
	// Spec scenario: "Role's argus_project preserved across incarnation"
	// Roles.Create is documented as write-once on argus_project. Verify
	// that calling Create a second time with a different argus_project
	// returns the existing row with its original project intact.
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	r1, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "frontend",
	})
	r2, err := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "backend",
	})
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if r1.ID != r2.ID {
		t.Fatalf("expected same row, got different ids")
	}
	if r2.ArgusProject != "frontend" {
		t.Fatalf("argus_project = %q, want 'frontend' (write-once)", r2.ArgusProject)
	}
}

func TestBindings_PartialUniqueIndex_PreventsTwoLiveForSameTask(t *testing.T) {
	// Migration 0002 added a partial unique index on
	// bindings(argus_task_id) WHERE ended_at IS NULL. Verify that a
	// second live binding for the same argus task fails at the SQL layer.
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	r1, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r1", Kind: KindWorker, ArgusProject: "p",
	})
	r2, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r2", Kind: KindWorker, ArgusProject: "p",
	})
	if _, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: r1.ID, ArgusTaskID: "shared-task", WorktreePath: "/tmp/wt1",
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: r2.ID, ArgusTaskID: "shared-task", WorktreePath: "/tmp/wt2",
	})
	if err == nil {
		t.Fatalf("expected UNIQUE constraint failure for second live binding on same task")
	}
	if !strings.Contains(err.Error(), "UNIQUE") && !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("expected UNIQUE/constraint error, got: %v", err)
	}
}

func TestBindings_PartialUniqueIndex_AllowsRebindAfterEnd(t *testing.T) {
	// The partial unique index uses WHERE ended_at IS NULL. After the
	// first binding is ended, a fresh live binding for the same role
	// (and even the same argus_task_id) must be allowed.
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "foo")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	bnd1, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t", WorktreePath: "/tmp/wt",
	})
	if err := d.Bindings.End(ctx, bnd1.ID, "ended"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t", WorktreePath: "/tmp/wt",
	}); err != nil {
		t.Fatalf("re-Create after end should succeed: %v", err)
	}
}
