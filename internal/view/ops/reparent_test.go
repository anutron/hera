package ops

import (
	"context"
	"testing"
)

// seedCoordinator installs an orchestrator with a live coordinator role bound
// to coordTask at worktree, returning the orchestrator. Mirrors the shape the
// rail produces for a root coordinator.
func seedCoordinator(db *fakeDB, name, coordTask, worktree string) *Orchestrator {
	orch := db.seedOrchestrator(name, false)
	role := db.seedRole(orch.ID, "coord", KindCoordinator, "Hera", false)
	db.seedBinding(role.ID, coordTask, worktree)
	return orch
}

// ReparentCoordinator nests a coordinator under another by creating a worker
// role + live binding (the multi-binding the rail renders as nested) for the
// child's coordinator argus task, reusing the child's coord worktree.
func TestReparentCoordinator_CreatesNestingBinding(t *testing.T) {
	s, db, _, _, _ := newTestService()
	parent := db.seedOrchestrator("parent", false)
	child := seedCoordinator(db, "child", "task-child-coord", "/wt/child")

	res, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		CoordTaskID:          "task-child-coord",
		ParentOrchestratorID: parent.ID,
		RoleName:             "child",
		ArgusProject:         "Hera",
	})
	if err != nil {
		t.Fatalf("ReparentCoordinator: unexpected error: %v", err)
	}
	if res == nil || res.ParentOrchestratorName != "parent" || res.ChildOrchestratorName != "child" {
		t.Fatalf("result mismatch: %+v", res)
	}

	// A worker role under the parent, bound to the child's coord task at the
	// child's coord worktree — the multi-binding the renderer nests.
	roles, _ := db.ListRolesByOrchestrator(context.Background(), parent.ID)
	var link *Role
	for _, r := range roles {
		if r.Name == "child" {
			link = r
		}
	}
	if link == nil {
		t.Fatal("expected a worker link role named 'child' under the parent")
	}
	if link.Kind != KindWorker {
		t.Fatalf("expected worker kind for the link role, got %q", link.Kind)
	}
	bnd, err := db.GetLiveBindingByRole(context.Background(), link.ID)
	if err != nil {
		t.Fatalf("expected a live binding on the link role: %v", err)
	}
	if bnd.ArgusTaskID != "task-child-coord" {
		t.Fatalf("link binding task mismatch: %q", bnd.ArgusTaskID)
	}
	if bnd.WorktreePath != "/wt/child" {
		t.Fatalf("link binding worktree mismatch (should reuse the child coord worktree): %q", bnd.WorktreePath)
	}

	// SubtreeOrchIDs(parent) now reaches the child via the shared coord task.
	ids, _ := db.SubtreeOrchIDs(context.Background(), parent.ID)
	reaches := false
	for _, id := range ids {
		if id == child.ID {
			reaches = true
		}
	}
	if !reaches {
		t.Fatalf("expected parent subtree to reach child after re-parent, got %v", ids)
	}
}

// A coordinator already nested under one parent is MOVED to a new parent: the
// old link binding ends and the old link role is removed, leaving exactly one
// parent linkage.
func TestReparentCoordinator_MovesFromExistingParent(t *testing.T) {
	s, db, _, _, _ := newTestService()
	oldParent := db.seedOrchestrator("old-parent", false)
	newParent := db.seedOrchestrator("new-parent", false)
	child := seedCoordinator(db, "child", "task-child-coord", "/wt/child")

	// Child already nested under oldParent (worker link bound to its coord task).
	oldLink := db.seedRole(oldParent.ID, "child", KindWorker, "Hera", false)
	oldBnd := db.seedBinding(oldLink.ID, "task-child-coord", "/wt/child")

	_, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		CoordTaskID:          "task-child-coord",
		ParentOrchestratorID: newParent.ID,
		RoleName:             "child",
	})
	if err != nil {
		t.Fatalf("ReparentCoordinator: unexpected error: %v", err)
	}

	// The old link binding was ended and the old link role deleted.
	if _, err := db.GetLiveBindingByRole(context.Background(), oldLink.ID); err == nil {
		t.Fatal("expected the old parent link binding to be ended")
	}
	endedOld := false
	for _, c := range db.endBindingCalls {
		if c.BindingID == oldBnd.ID && c.Reason == EndReasonReparented {
			endedOld = true
		}
	}
	if !endedOld {
		t.Fatalf("expected EndBinding(%d, reparented), got %+v", oldBnd.ID, db.endBindingCalls)
	}
	deletedOld := false
	for _, id := range db.deleteRoleCalls {
		if id == oldLink.ID {
			deletedOld = true
		}
	}
	if !deletedOld {
		t.Fatalf("expected the old link role %d to be deleted, got %v", oldLink.ID, db.deleteRoleCalls)
	}

	// Exactly one live link binding for the coord task remains in a parent
	// orchestrator (the new parent), alongside the child's own coord binding.
	live, _ := db.ListLiveBindingsByTask(context.Background(), "task-child-coord")
	parentLinks := 0
	for _, b := range live {
		if b.OrchestratorID == newParent.ID {
			parentLinks++
		}
		if b.OrchestratorID == oldParent.ID {
			t.Fatalf("old parent link should be gone, found binding %+v", b)
		}
	}
	if parentLinks != 1 {
		t.Fatalf("expected exactly one link binding under the new parent, got %d", parentLinks)
	}
}

// A coordinator cannot be adopted under itself.
func TestReparentCoordinator_RejectsSelf(t *testing.T) {
	s, db, _, _, _ := newTestService()
	child := seedCoordinator(db, "child", "task-child-coord", "/wt/child")

	_, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		CoordTaskID:          "task-child-coord",
		ParentOrchestratorID: child.ID,
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error adopting a coord under itself, got %v", err)
	}
}

// A coordinator cannot be adopted under one of its own descendants (cycle).
func TestReparentCoordinator_RejectsDescendantCycle(t *testing.T) {
	s, db, _, _, _ := newTestService()
	child := seedCoordinator(db, "child", "task-child-coord", "/wt/child")
	// A sub-coordinator S nested under child: S's coord task is also bound as a
	// worker under child, so SubtreeOrchIDs(child) = {child, sub}.
	sub := seedCoordinator(db, "sub", "task-sub-coord", "/wt/sub")
	subLink := db.seedRole(child.ID, "sub", KindWorker, "Hera", false)
	db.seedBinding(subLink.ID, "task-sub-coord", "/wt/sub")

	_, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		CoordTaskID:          "task-child-coord",
		ParentOrchestratorID: sub.ID,
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error adopting a coord under its own descendant, got %v", err)
	}
	// No link role should have been created under the descendant.
	roles, _ := db.ListRolesByOrchestrator(context.Background(), sub.ID)
	for _, r := range roles {
		if r.Name == "child" {
			t.Fatal("a link role was created under the descendant despite the cycle guard")
		}
	}
}

func TestReparentCoordinator_RejectsEmptyTaskID(t *testing.T) {
	s, db, _, _, _ := newTestService()
	parent := db.seedOrchestrator("parent", false)
	child := db.seedOrchestrator("child", false)
	_, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		ParentOrchestratorID: parent.ID,
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error for empty coord task id, got %v", err)
	}
}

func TestReparentCoordinator_RejectsUnknownParent(t *testing.T) {
	s, db, _, _, _ := newTestService()
	child := seedCoordinator(db, "child", "task-child-coord", "/wt/child")
	_, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		CoordTaskID:          "task-child-coord",
		ParentOrchestratorID: 999,
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error for unknown parent, got %v", err)
	}
}

// A coordinator whose coord task has no live binding (dormant/archived) cannot
// be re-parented — there is no worktree to carry onto the new link binding.
func TestReparentCoordinator_RejectsDormantCoordinator(t *testing.T) {
	s, db, _, _, _ := newTestService()
	parent := db.seedOrchestrator("parent", false)
	child := db.seedOrchestrator("child", false)
	// A coord role exists but its binding has already ended (no live binding).
	coordRole := db.seedRole(child.ID, "coord", KindCoordinator, "Hera", false)
	db.seedEndedBinding(coordRole.ID, "task-child-coord", "/wt/child")

	_, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		CoordTaskID:          "task-child-coord",
		ParentOrchestratorID: parent.ID,
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error for a dormant coordinator, got %v", err)
	}
}

// The link role name is de-collided against an existing active role under the
// parent.
func TestReparentCoordinator_DeCollidesRoleName(t *testing.T) {
	s, db, _, _, _ := newTestService()
	parent := db.seedOrchestrator("parent", false)
	db.seedRole(parent.ID, "child", KindWorker, "Hera", false) // name collision
	child := seedCoordinator(db, "child", "task-child-coord", "/wt/child")

	res, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		CoordTaskID:          "task-child-coord",
		ParentOrchestratorID: parent.ID,
		RoleName:             "child",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RoleName == "child" {
		t.Fatal("expected a de-collided link role name, got the colliding 'child'")
	}
}
