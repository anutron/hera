package db

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// These tests pin the worktree-keyed binding lookups added for BUG-059 and
// the raw DAO-level mechanism behind the claim-vs-attach paradox: identity
// keyed by argus_task_id disagrees with the (worktree_path, orchestrator_id)
// uniqueness the INSERT is actually constrained by.

func TestBindings_GetLiveByWorktreeAndOrchestrator_HappyPath(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "o")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	want, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-1", WorktreePath: "/wt",
	})

	got, err := d.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, "/wt", orch.ID)
	if err != nil {
		t.Fatalf("GetLiveByWorktreeAndOrchestrator: %v", err)
	}
	if got.ID != want.ID {
		t.Fatalf("got binding %d, want %d", got.ID, want.ID)
	}
}

func TestBindings_GetLiveByWorktreeAndOrchestrator_NotFoundAndOrchScoped(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orchA, _ := d.Orchestrators.Create(ctx, "A")
	orchB, _ := d.Orchestrators.Create(ctx, "B")
	roleA, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: roleA.ID, ArgusTaskID: "t-1", WorktreePath: "/wt",
	})

	// Same worktree, DIFFERENT orchestrator → must not resolve the orch-A
	// binding. This scoping is what makes the CallerRole worktree fallback
	// safe against a stale binding from another orchestrator.
	if _, err := d.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, "/wt", orchB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-orchestrator lookup: want ErrNotFound, got %v", err)
	}
}

func TestBindings_GetLiveByWorktreeAndOrchestrator_IgnoresEnded(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "o")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	bnd, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-1", WorktreePath: "/wt",
	})
	if err := d.Bindings.End(ctx, bnd.ID, "test"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, err := d.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, "/wt", orch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ended binding must not resolve: got %v", err)
	}
}

func TestBindings_ListLiveByWorktree_MultiOrchAndExcludesEnded(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orchA, _ := d.Orchestrators.Create(ctx, "A")
	orchB, _ := d.Orchestrators.Create(ctx, "B")
	roleA, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	roleB, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orchB.ID, Name: "c", Kind: KindCoordinator, ArgusProject: "p",
	})
	// One live binding per orchestrator at the same worktree (the legitimate
	// multi-binding shape), plus one ended row that must be excluded.
	_, _ = d.Bindings.Create(ctx, CreateBindingInput{RoleID: roleA.ID, ArgusTaskID: "t", WorktreePath: "/wt"})
	_, _ = d.Bindings.Create(ctx, CreateBindingInput{RoleID: roleB.ID, ArgusTaskID: "t", WorktreePath: "/wt"})
	dead, _ := d.Orchestrators.Create(ctx, "dead")
	roleDead, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: dead.ID, Name: "x", Kind: KindWorker, ArgusProject: "p",
	})
	deadBnd, _ := d.Bindings.Create(ctx, CreateBindingInput{RoleID: roleDead.ID, ArgusTaskID: "t-old", WorktreePath: "/wt"})
	_ = d.Bindings.End(ctx, deadBnd.ID, "argus_archived")

	got, err := d.Bindings.ListLiveByWorktree(ctx, "/wt")
	if err != nil {
		t.Fatalf("ListLiveByWorktree: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 live bindings (orchA + orchB), got %d", len(got))
	}
}

// TestBindings_ClaimVsAttachMechanism reproduces, at the DAO level, the exact
// disagreement behind BUG-059: with a live binding rooted at (taskA,
// worktreeP, orchO), a task-keyed lookup for a DIFFERENT task id resolves
// nothing ("claim says none"), yet inserting a fresh binding for that other
// task at the SAME worktree+orchestrator is rejected by the
// bindings_live_unique_worktree_orch index ("attach says exists"). The
// worktree-keyed lookup is what reconciles the two views.
func TestBindings_ClaimVsAttachMechanism(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	orch, _ := d.Orchestrators.Create(ctx, "o")
	role, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: KindWorker, ArgusProject: "p",
	})
	live, _ := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "task-A", WorktreePath: "/shared/wt",
	})

	// "claim": a task-keyed lookup for the OTHER (colliding) task id → nothing.
	if _, err := d.Bindings.GetLiveByTaskAndOrchestrator(ctx, "task-B", orch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task-keyed claim: want ErrNotFound, got %v", err)
	}

	// "attach": inserting a fresh binding for task-B at the same worktree+orch
	// is rejected by the (worktree_path, orchestrator_id) uniqueness.
	role2, _ := d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: KindWorker, ArgusProject: "p",
	})
	_, err := d.Bindings.Create(ctx, CreateBindingInput{
		RoleID: role2.ID, ArgusTaskID: "task-B", WorktreePath: "/shared/wt",
	})
	if err == nil {
		t.Fatal("attach INSERT: expected UNIQUE constraint violation, got nil")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "UNIQUE") {
		t.Fatalf("attach INSERT: expected a UNIQUE-constraint error, got %v", err)
	}

	// Reconciliation: the worktree-keyed lookup resolves the same binding the
	// INSERT collided with, so both views now agree on identity.
	got, err := d.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, "/shared/wt", orch.ID)
	if err != nil {
		t.Fatalf("worktree-keyed lookup: %v", err)
	}
	if got.ID != live.ID {
		t.Fatalf("worktree-keyed lookup resolved binding %d, want %d", got.ID, live.ID)
	}
}
