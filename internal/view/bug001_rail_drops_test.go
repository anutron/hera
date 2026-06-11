package view

import (
	"context"
	"testing"

	"github.com/anutron/hera/internal/db"
)

// reachableRoleIDs returns the set of role ids that render as a SELECTABLE,
// pane-bindable row anywhere in the rail — active tree, nested sub-coord, OR
// inside an Archive expando (force-open via showArchived). A role is
// "reachable" per spec when the operator can land the cursor on it and rebind;
// dropping it from every row makes it unreachable (BUG-001).
func reachableRoleIDs(rl *railList) map[int64]bool {
	prevShow := rl.showArchived
	rl.SetShowArchived(true) // force every Archive expando open so bucketed-but-reachable rows count
	defer rl.SetShowArchived(prevShow)
	out := map[int64]bool{}
	for _, row := range rl.rows {
		if row.kind == railRowRole && row.role != nil && row.role.RoleID > 0 {
			out[row.role.RoleID] = true
		}
	}
	return out
}

// TestBUG001_DisconnectedWorkerStaysReachable is the minimal claim: a worker
// role whose hera binding has ENDED (session disconnected) but whose argus
// task RECORD still exists (present in the warm state cache, non-archived)
// MUST remain a reachable row in the active tree — at most dimmed, never
// dropped (spec.md "Task status never buckets a rail row").
func TestBUG001_DisconnectedWorkerStaysReachable(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, err := d.Orchestrators.Create(ctx, "sherlock-3b")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "memory-svc-worker", Kind: db.KindWorker, ArgusProject: "sherlock",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	bnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "wtask", WorktreePath: "/w",
	})
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	// Session disconnected: end the binding. The argus task record still exists.
	if err := d.Bindings.End(ctx, bnd.ID, "session_ended"); err != nil {
		t.Fatalf("End binding: %v", err)
	}

	// Warm cache HAS the record (records exist per the bug evidence), non-archived.
	src := &statePaneSource{states: map[string]ArgusTaskState{
		"wtask": {Status: "complete"},
	}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	if !reachableRoleIDs(a.pieces.rail)[w.ID] {
		t.Fatalf("disconnected worker role (record exists) dropped from the rail entirely — unreachable (BUG-001)")
	}
}

// TestBUG001_SubCoordSubtreeReachableWhenParentLinkBucketed reproduces the
// sherlock-3b shape: a root coordinator with a sub-coordinator (multi-binding)
// whose own children persist as argus records, but whose PARENT-LINK worker
// row buckets (its bound coord task is gone/archived). resolveSubCoordinators
// consumes the child orch (removes it from the top level) and nests it under
// the parent-link worker row; but appendOrchChildren only recurses childOrch
// for ACTIVE roles — so a bucketed parent-link row strands the entire child
// subtree: the grandchildren render NOWHERE (not nested, not at top level,
// not in any Archive expando). That is the BUG-001 "unreachable" drop.
func TestBUG001_SubCoordSubtreeReachableWhenParentLinkBucketed(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Root coordinator "sherlock-3b".
	root, err := d.Orchestrators.Create(ctx, "sherlock-3b")
	if err != nil {
		t.Fatalf("root orch: %v", err)
	}
	// Root's worker role that is ALSO the sub-coord's coordinator (multi-binding):
	// its bound argus task == the child orch's coord task ("subcoord-task").
	parentLink, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: root.ID, Name: "1c-add-memory-svc", Kind: db.KindWorker, ArgusProject: "sherlock",
	})
	if err != nil {
		t.Fatalf("parent-link role: %v", err)
	}
	plBnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: parentLink.ID, ArgusTaskID: "subcoord-task", WorktreePath: "/sc",
	})
	if err != nil {
		t.Fatalf("parent-link binding: %v", err)
	}
	if err := d.Bindings.End(ctx, plBnd.ID, "session_ended"); err != nil {
		t.Fatalf("end parent-link binding: %v", err)
	}

	// Child orchestrator "1c" whose coord is bound to the SAME task.
	child, err := d.Orchestrators.Create(ctx, "1c")
	if err != nil {
		t.Fatalf("child orch: %v", err)
	}
	childCoord, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: child.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "sherlock",
	})
	if err != nil {
		t.Fatalf("child coord role: %v", err)
	}
	ccBnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: childCoord.ID, ArgusTaskID: "subcoord-task", WorktreePath: "/sc",
	})
	if err != nil {
		t.Fatalf("child coord binding: %v", err)
	}
	if err := d.Bindings.End(ctx, ccBnd.ID, "session_ended"); err != nil {
		t.Fatalf("end child coord binding: %v", err)
	}
	// Child's leaf worker — a grandchild of the root. Its record still exists.
	grandchild, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: child.ID, Name: "eval-harness-worker", Kind: db.KindWorker, ArgusProject: "sherlock",
	})
	if err != nil {
		t.Fatalf("grandchild role: %v", err)
	}
	gcBnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: grandchild.ID, ArgusTaskID: "grandchild-task", WorktreePath: "/gc",
	})
	if err != nil {
		t.Fatalf("grandchild binding: %v", err)
	}
	if err := d.Bindings.End(ctx, gcBnd.ID, "session_ended"); err != nil {
		t.Fatalf("end grandchild binding: %v", err)
	}

	// Warm cache: the sub-coord's COORD task is GONE (its worktree/transcript was
	// pruned or the task was deleted), but the grandchild's record STILL EXISTS.
	// This is exactly the recoverable-work case the bug calls out: the grandchild
	// must stay reachable so the operator can resume its session.
	src := &statePaneSource{states: map[string]ArgusTaskState{
		"grandchild-task": {Status: "in_progress"},
	}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	reachable := reachableRoleIDs(a.pieces.rail)
	if !reachable[grandchild.ID] {
		t.Fatalf("grandchild role under a sub-coordinator with a bucketed parent-link row is UNREACHABLE — dropped from the entire rail (BUG-001)")
	}
}
