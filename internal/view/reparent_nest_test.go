package view

import (
	"context"
	"testing"

	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/view/ops"
)

// reparentDormant wires up a dormant child coordinator (coord binding ended)
// re-parented under a live parent, then ends the fresh link binding with
// end_reason resync_missing to mirror the reconciler killing it because the
// child's coord task is gone/archived from argus. Returns the child + parent
// orchestrator ids for assertions. childPresent controls whether the child
// coord task is still in argus's state cache (archived-but-present) or fully
// gone (pruned).
func reparentDormant(t *testing.T, childArchived, childPresent bool) (childID, parentID int64, src *freelancePaneSource, d *db.DB) {
	t.Helper()
	d = openTestDB(t)
	ctx := context.Background()

	child, _ := d.Orchestrators.Create(ctx, "2a-team")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: child.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	cb, _ := d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, OrchestratorID: child.ID, ArgusTaskID: "t-child", WorktreePath: "/cc"})
	_ = d.Bindings.End(ctx, cb.ID, "ended") // dormant coordinator: session ended

	parent, _ := d.Orchestrators.Create(ctx, "sherlock-mvp")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, OrchestratorID: parent.ID, ArgusTaskID: "t-parent", WorktreePath: "/pc"})

	svc := ops.NewService(newDBAdapter(d), nil, nil, nil)
	if _, err := svc.ReparentCoordinator(ctx, ops.ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		ParentOrchestratorID: parent.ID,
		RoleName:             "2a-team",
		ArgusProject:         "p",
	}); err != nil {
		t.Fatalf("reparent: %v", err)
	}

	// Reconciler: child coord task gone from argus → end the fresh link binding.
	live, _ := d.Bindings.ListLiveByTaskID(ctx, "t-child")
	for _, b := range live {
		if b.RoleID != childCoord.ID {
			_ = d.Bindings.End(ctx, b.ID, "resync_missing")
		}
	}

	states := map[string]ArgusTaskState{
		"t-parent": {Status: "in_progress"},
	}
	tasks := []ArgusTaskInfo{
		{ID: "t-parent", Name: "sherlock-mvp", Project: "p", State: ArgusTaskState{Status: "in_progress"}},
	}
	if childPresent {
		st := ArgusTaskState{Status: "complete", Archived: childArchived}
		states["t-child"] = st
		tasks = append(tasks, ArgusTaskInfo{ID: "t-child", Name: "2a-team", Project: "p", State: st})
	}
	src = &freelancePaneSource{states: states, tasks: tasks}
	return child.ID, parent.ID, src, d
}

// TestReparent_DormantCoord_GoneTask_Nests is the BUG-027 reproduction: the
// child coord task is fully gone from argus (pruned) and its link binding was
// ended by the reconciler (resync_missing). The child must still nest under the
// parent — re-parenting is structural and must not depend on the child's
// session liveness.
func TestReparent_DormantCoord_GoneTask_Nests(t *testing.T) {
	childID, _, src, d := reparentDormant(t, false, false)
	t.Cleanup(func() { _ = d.Close() })

	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == childID {
			t.Fatalf("BUG-027: dormant child (gone task) must nest under parent, not render top-level")
		}
	}
}

// TestReparent_DormantCoord_ArchivedTask_Nests covers the archived-but-present
// variant: the child coord task is archived in argus (still in the task list)
// and its link binding was ended by the reconciler. It must still nest.
func TestReparent_DormantCoord_ArchivedTask_Nests(t *testing.T) {
	childID, _, src, d := reparentDormant(t, true, true)
	t.Cleanup(func() { _ = d.Close() })

	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == childID {
			t.Fatalf("BUG-027: dormant child (archived task) must nest under parent, not render top-level")
		}
	}
}

// TestReparent_DormantCoord_TwiceNestsOnce proves a successful dormant
// re-parent is idempotent AND visible: pressing J twice (with the reconciler
// ending the link binding in between) leaves exactly one link role under the
// parent and the child nests — no archived stray rows linger to double-render.
func TestReparent_DormantCoord_TwiceNestsOnce(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	t.Cleanup(func() { _ = d.Close() })

	child, _ := d.Orchestrators.Create(ctx, "2a-team")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: child.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	cb, _ := d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, OrchestratorID: child.ID, ArgusTaskID: "t-child", WorktreePath: "/cc"})
	_ = d.Bindings.End(ctx, cb.ID, "ended")

	parent, _ := d.Orchestrators.Create(ctx, "sherlock-mvp")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, OrchestratorID: parent.ID, ArgusTaskID: "t-parent", WorktreePath: "/pc"})

	svc := ops.NewService(newDBAdapter(d), nil, nil, nil)
	in := ops.ReparentCoordInput{ChildOrchestratorID: child.ID, ParentOrchestratorID: parent.ID, RoleName: "2a-team", ArgusProject: "p"}
	for i := 0; i < 2; i++ {
		if _, err := svc.ReparentCoordinator(ctx, in); err != nil {
			t.Fatalf("reparent #%d: %v", i+1, err)
		}
		live, _ := d.Bindings.ListLiveByTaskID(ctx, "t-child")
		for _, b := range live {
			if b.RoleID != childCoord.ID {
				_ = d.Bindings.End(ctx, b.ID, "resync_missing")
			}
		}
	}

	// Exactly one link role under the parent (no de-collided "2a-team-2").
	var linkRoles int
	roles, _ := d.Roles.ListByOrchestratorInclusive(ctx, parent.ID)
	for _, r := range roles {
		if r.Kind == db.KindWorker {
			linkRoles++
		}
	}
	if linkRoles != 1 {
		t.Fatalf("want exactly 1 link role after two J presses, got %d", linkRoles)
	}

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{"t-parent": {Status: "in_progress"}},
		tasks:  []ArgusTaskInfo{{ID: "t-parent", Name: "sherlock-mvp", Project: "p", State: ArgusTaskState{Status: "in_progress"}}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == child.ID {
			t.Fatalf("child must nest after repeated dormant re-parent, not render top-level")
		}
	}
}

// TestReparent_LiveCoord_StillNests guards the unchanged path: a LIVE child
// coordinator (its coord session still running, link binding live) nests under
// its parent. This is the pre-BUG-027 behavior and must not regress.
func TestReparent_LiveCoord_StillNests(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	t.Cleanup(func() { _ = d.Close() })

	child, _ := d.Orchestrators.Create(ctx, "child")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: child.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, OrchestratorID: child.ID, ArgusTaskID: "t-child", WorktreePath: "/cc"})

	parent, _ := d.Orchestrators.Create(ctx, "parent")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, OrchestratorID: parent.ID, ArgusTaskID: "t-parent", WorktreePath: "/pc"})

	svc := ops.NewService(newDBAdapter(d), nil, nil, nil)
	if _, err := svc.ReparentCoordinator(ctx, ops.ReparentCoordInput{
		ChildOrchestratorID: child.ID, ParentOrchestratorID: parent.ID, RoleName: "child", ArgusProject: "p",
	}); err != nil {
		t.Fatalf("reparent: %v", err)
	}
	// Link binding stays LIVE (live coord — reconciler never ends it).

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{"t-child": {Status: "in_progress"}, "t-parent": {Status: "in_progress"}},
		tasks: []ArgusTaskInfo{
			{ID: "t-child", Name: "child", Project: "p", State: ArgusTaskState{Status: "in_progress"}},
			{ID: "t-parent", Name: "parent", Project: "p", State: ArgusTaskState{Status: "in_progress"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == child.ID {
			t.Fatalf("live child must nest under parent, not render top-level")
		}
	}
}

// TestReparent_TornDownLink_DoesNotNest proves the end_reason guard: a stale
// link whose binding was ended by an operator teardown (here a prior re-parent
// elsewhere, end_reason "reparented") must NOT re-nest its child — otherwise a
// leftover row would pull the child back under its OLD parent. The latest-
// binding fallback sets the role's ArgusTaskID, so only the end_reason
// distinguishes this from a structurally-valid dormant link.
func TestReparent_TornDownLink_DoesNotNest(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	t.Cleanup(func() { _ = d.Close() })

	child, _ := d.Orchestrators.Create(ctx, "child")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: child.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	cb, _ := d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, OrchestratorID: child.ID, ArgusTaskID: "t-child", WorktreePath: "/cc"})
	_ = d.Bindings.End(ctx, cb.ID, "ended")

	// An old parent whose link role survived with a "reparented"-ended binding
	// (simulating a teardown that ended the binding but left the role row).
	oldParent, _ := d.Orchestrators.Create(ctx, "old-parent")
	oldParentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: oldParent.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: oldParentCoord.ID, OrchestratorID: oldParent.ID, ArgusTaskID: "t-old", WorktreePath: "/op"})
	link, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: oldParent.ID, Name: "child", Kind: db.KindWorker, ArgusProject: "p"})
	lb, _ := d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: link.ID, OrchestratorID: oldParent.ID, ArgusTaskID: "t-child", WorktreePath: "/cc"})
	_ = d.Bindings.End(ctx, lb.ID, ops.EndReasonReparented)

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{"t-old": {Status: "in_progress"}},
		tasks:  []ArgusTaskInfo{{ID: "t-old", Name: "old-parent", Project: "p", State: ArgusTaskState{Status: "in_progress"}}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// The child must remain top-level (the torn-down link does not nest it).
	var childTopLevel bool
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == child.ID {
			childTopLevel = true
		}
		// The stale link role must not have been promoted to a sub-coordinator.
		if o.ID == oldParent.ID {
			for _, r := range o.Roles {
				if r.childOrch != nil && r.childOrch.ID == child.ID {
					t.Fatalf("torn-down (reparented) link must not re-nest the child under its old parent")
				}
			}
		}
	}
	if !childTopLevel {
		t.Fatalf("child consumed by a stale link — expected it to stay top-level")
	}
}
