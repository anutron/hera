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

// linksUnder counts the worker LINK roles under orchestrator orchID bound (live
// OR ended) to coordTask — the multi-binding rows the rail would render as the
// re-parented coordinator. Duplicate links here are exactly the BUG-026 symptom.
func linksUnder(db *fakeDB, orchID int64, coordTask string) []*Role {
	var out []*Role
	seen := map[int64]bool{}
	all, _ := db.ListBindingsByTask(context.Background(), coordTask)
	for _, b := range all {
		r, ok := db.roles[b.RoleID]
		if !ok || seen[r.ID] || r.OrchestratorID != orchID || r.Kind != KindWorker {
			continue
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	return out
}

// Re-parenting a DORMANT coordinator is IDEMPOTENT: pressing J repeatedly must
// not pile up de-collided duplicate link roles (BUG-026). The resync reconciler
// ends a parent-link binding when the coord task is gone from argus, leaving the
// link role behind; the next re-parent must delete that stale role (resolved via
// ListBindingsByTask, not the live-only lookup) rather than de-collide a new one.
func TestReparentCoordinator_DormantCoord_NoDuplicateLinks(t *testing.T) {
	s, db, _, _, _ := newTestService()
	parent := db.seedOrchestrator("parent", false)
	child := seedCoordinator(db, "child", "task-child-coord", "/wt/child")

	in := ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		ParentOrchestratorID: parent.ID,
		RoleName:             "child",
		ArgusProject:         "Hera",
	}

	// First J: creates the link role + live binding under the parent.
	if _, err := s.ReparentCoordinator(context.Background(), in); err != nil {
		t.Fatalf("first re-parent: %v", err)
	}
	links := linksUnder(db, parent.ID, "task-child-coord")
	if len(links) != 1 {
		t.Fatalf("after first J want 1 link role, got %d: %v", len(links), links)
	}

	// The resync reconciler ends the link's live binding (coord task gone from
	// argus → end_reason resync_missing). The link ROLE row survives.
	live, _ := db.ListLiveBindingsByTask(context.Background(), "task-child-coord")
	for _, b := range live {
		if b.RoleID == links[0].ID {
			_ = db.EndBinding(context.Background(), b.ID, "resync_missing")
		}
	}

	// Second J on the same dormant coordinator under the same parent.
	if _, err := s.ReparentCoordinator(context.Background(), in); err != nil {
		t.Fatalf("second re-parent: %v", err)
	}

	// BUG-026: without idempotent teardown this is 2 (the stale "child" plus a
	// de-collided "child-2"). With the fix it stays exactly 1.
	links = linksUnder(db, parent.ID, "task-child-coord")
	if len(links) != 1 {
		names := make([]string, len(links))
		for i, r := range links {
			names[i] = r.Name
		}
		t.Fatalf("after second J want 1 link role (no duplicates), got %d: %v", len(links), names)
	}
	if links[0].Name != "child" {
		t.Fatalf("link role must keep the clean name %q, not a de-collided dup; got %q", "child", links[0].Name)
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

// A coordinator whose coord session is NOT live (its coord binding has ended)
// is STILL re-parentable (BUG-025): re-parenting links to the coordinator's
// argus TASK, which outlives its session. The new link binding recovers the
// task id + worktree from the coord role's most-recent ENDED binding. The
// operator does not pass CoordTaskID (the rail carries none for a dormant
// coordinator) — the op resolves it.
func TestReparentCoordinator_DormantCoordinatorResolvesFromEndedBinding(t *testing.T) {
	s, db, _, _, _ := newTestService()
	parent := db.seedOrchestrator("parent", false)
	child := db.seedOrchestrator("child", false)
	// A coord role exists but its binding has already ended (no live binding).
	coordRole := db.seedRole(child.ID, "coord", KindCoordinator, "Hera", false)
	db.seedEndedBinding(coordRole.ID, "task-child-coord", "/wt/child")

	res, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		ParentOrchestratorID: parent.ID,
		RoleName:             "child",
		// CoordTaskID intentionally empty — the rail carries no live coord task.
	})
	if err != nil {
		t.Fatalf("ReparentCoordinator on a dormant coordinator: unexpected error: %v", err)
	}
	if res == nil || res.ChildOrchestratorName != "child" || res.ParentOrchestratorName != "parent" {
		t.Fatalf("result mismatch: %+v", res)
	}

	// The new link binding under the parent recovers the coord task + worktree
	// from the ended binding.
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
	bnd, err := db.GetLiveBindingByRole(context.Background(), link.ID)
	if err != nil {
		t.Fatalf("expected a live binding on the link role: %v", err)
	}
	if bnd.ArgusTaskID != "task-child-coord" {
		t.Fatalf("link binding task mismatch (should recover from ended binding): %q", bnd.ArgusTaskID)
	}
	if bnd.WorktreePath != "/wt/child" {
		t.Fatalf("link binding worktree mismatch (should recover from ended binding): %q", bnd.WorktreePath)
	}
}

// A coordinator whose coord role has NEVER been bound cannot be re-parented —
// there is no argus task or worktree to carry onto the new link binding.
func TestReparentCoordinator_RejectsNeverBoundCoordinator(t *testing.T) {
	s, db, _, _, _ := newTestService()
	parent := db.seedOrchestrator("parent", false)
	child := db.seedOrchestrator("child", false)
	// A coord role exists but was never bound to an argus task.
	db.seedRole(child.ID, "coord", KindCoordinator, "Hera", false)

	_, err := s.ReparentCoordinator(context.Background(), ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		ParentOrchestratorID: parent.ID,
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error for a never-bound coordinator, got %v", err)
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
