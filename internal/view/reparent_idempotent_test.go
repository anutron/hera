package view

import (
	"context"
	"testing"

	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/view/ops"
)

// TestReparentCoordinator_Idempotent_RealDB drives the FULL real stack
// (ops.Service over dbAdapter over a real sqlite DB) and proves BUG-026 is
// fixed end-to-end: pressing J repeatedly on a dormant coordinator — with the
// resync reconciler ending the link binding between presses — never piles up
// duplicate "C-2"/"C-3" link rows. Exercises the real ListByTaskID query and
// the ON DELETE CASCADE that the fakeDB-based ops test can only approximate.
func TestReparentCoordinator_Idempotent_RealDB(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	child, _ := d.Orchestrators.Create(ctx, "child")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: child.ID, Name: "child-coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	cb, _ := d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, OrchestratorID: child.ID, ArgusTaskID: "t-child", WorktreePath: "/cc"})
	_ = d.Bindings.End(ctx, cb.ID, "ended") // dormant coordinator

	parent, _ := d.Orchestrators.Create(ctx, "parent")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "parent-coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, OrchestratorID: parent.ID, ArgusTaskID: "t-parent", WorktreePath: "/pc"})

	svc := ops.NewService(newDBAdapter(d), nil, nil, nil)
	in := ops.ReparentCoordInput{
		ChildOrchestratorID:  child.ID,
		ParentOrchestratorID: parent.ID,
		RoleName:             "child",
		ArgusProject:         "p",
	}

	countLinks := func() []string {
		all, _ := d.Bindings.ListByTaskID(ctx, "t-child")
		seen := map[int64]bool{}
		var names []string
		for _, b := range all {
			r, err := d.Roles.GetByID(ctx, b.RoleID)
			if err != nil || seen[r.ID] || r.OrchestratorID != parent.ID || r.Kind != db.KindWorker {
				continue
			}
			seen[r.ID] = true
			names = append(names, r.Name)
		}
		return names
	}

	// Press J three times; the reconciler ends each fresh link binding in between.
	for i := 0; i < 3; i++ {
		if _, err := svc.ReparentCoordinator(ctx, in); err != nil {
			t.Fatalf("re-parent #%d: %v", i+1, err)
		}
		// resync reconciler: coord task gone from argus → end the live link binding.
		live, _ := d.Bindings.ListLiveByTaskID(ctx, "t-child")
		for _, b := range live {
			if b.RoleID != childCoord.ID {
				_ = d.Bindings.End(ctx, b.ID, "resync_missing")
			}
		}
	}

	names := countLinks()
	if len(names) != 1 {
		t.Fatalf("BUG-026: want exactly 1 link role after repeated J, got %d: %v", len(names), names)
	}
	if names[0] != "child" {
		t.Fatalf("link role must keep clean name %q, got %q", "child", names[0])
	}
}
