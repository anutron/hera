package view

import (
	"strings"
	"testing"

	"github.com/anutron/argus-sdk/theme"
)

// TestBUG003_RecursedSubtreeInsideArchiveRendersDimmed pins the dimming rule for
// the BUG-001 transitive-recursion fix: when a bucketed parent-link row strands a
// sub-coordinator's child subtree inside a coordinator's `Archive (N)` expando,
// the WHOLE recursed subtree — the sub-coord row AND all its descendants — must
// render in the DIMMED (grey) style consistent with its Archive placement,
// regardless of each row's own status. The BUG-001 fix restored REACHABILITY but
// the recursion re-entered the ACTIVE-style render path, so a live, non-archived
// grandchild rendered bright inside an Archive expando — contradicting the
// operator's "they should all be grey since they are in the archive" and the
// spec rule that Archive PLACEMENT modulates STYLE (dimmed) (spec.md:1482).
func TestBUG003_RecursedSubtreeInsideArchiveRendersDimmed(t *testing.T) {
	rl := newRailList()

	// Child orchestrator with a LIVE, non-archived grandchild worker — its own
	// status would normally render bright. Hand-wire the resolved tree (as
	// TestRailList_SubCoordinatorNestsAsFoldableCoordRow does): the child orch is
	// consumed by resolveSubCoordinators and renders ONLY nested beneath the
	// parent-link row.
	child := &orchEntry{ID: 2, Name: "child", CoordTaskID: "t-sub", Roles: []*roleEntry{
		{OrchestratorID: 2, RoleID: 20, Name: "grandchild", RoleKind: "worker", Live: true},
	}}
	// Parent-link sub-coordinator row that BUCKETS: its bound argus task record is
	// gone (Dead), so it sorts into the root coordinator's Archive expando.
	parentLink := &roleEntry{
		OrchestratorID: 1, RoleID: 11, Name: "subcoord", RoleKind: "coordinator",
		ArgusTaskID: "t-sub", Dead: true, childOrch: child,
	}
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "root", CoordTaskID: "t-root", Roles: []*roleEntry{parentLink}},
	})
	// Force every Archive expando open so the bucketed subtree renders.
	rl.SetShowArchived(true)

	dump, sim := renderRailSim(t, rl, 40, 14)
	gcRow := rowOf(dump, "grandchild")
	if gcRow < 0 {
		t.Fatalf("grandchild row inside the Archive expando did not render at all; got:\n%s", dump)
	}

	// The recursed grandchild MUST render dimmed (grey) by Archive placement, even
	// though its own status is live/active.
	if !rowHasForeground(sim, gcRow, theme.ColorDimmed) {
		t.Fatalf("BUG-003: grandchild placed inside an Archive expando must render DIMMED (grey); got bright row:\n%s", dump)
	}
	// And it must NOT render in the bright/normal name color — placement dimming
	// replaces the active style entirely.
	if rowHasForeground(sim, gcRow, theme.ColorNormal) {
		t.Fatalf("BUG-003: grandchild inside an Archive expando still renders bright (ColorNormal); got:\n%s", dump)
	}
}

// TestBUG003_NestedSubCoordRowInsideArchiveRendersDimmed extends the claim one
// level: a sub-coordinator ROW nested inside the Archive subtree (itself
// non-archived) must also render dimmed by placement, via drawSubCoordRow's
// placement-aware style — not only leaf workers.
func TestBUG003_NestedSubCoordRowInsideArchiveRendersDimmed(t *testing.T) {
	rl := newRailList()

	// Grandchild orchestrator (deepest), with a leaf.
	grand := &orchEntry{ID: 3, Name: "grand", CoordTaskID: "t-grand", Roles: []*roleEntry{
		{OrchestratorID: 3, RoleID: 30, Name: "leaf", RoleKind: "worker", Live: true},
	}}
	// A LIVE, non-archived nested sub-coordinator row under the child orch.
	nestedSub := &roleEntry{
		OrchestratorID: 2, RoleID: 21, Name: "nested-sub", RoleKind: "coordinator",
		ArgusTaskID: "t-grand", Live: true, childOrch: grand,
	}
	child := &orchEntry{ID: 2, Name: "child", CoordTaskID: "t-sub", Roles: []*roleEntry{nestedSub}}
	parentLink := &roleEntry{
		OrchestratorID: 1, RoleID: 11, Name: "subcoord", RoleKind: "coordinator",
		ArgusTaskID: "t-sub", Dead: true, childOrch: child,
	}
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "root", CoordTaskID: "t-root", Roles: []*roleEntry{parentLink}},
	})
	rl.SetShowArchived(true)

	dump, sim := renderRailSim(t, rl, 44, 16)
	for _, name := range []string{"nested-sub", "leaf"} {
		y := rowOf(dump, name)
		if y < 0 {
			t.Fatalf("%q row inside the Archive subtree did not render; got:\n%s", name, dump)
		}
		if !rowHasForeground(sim, y, theme.ColorDimmed) {
			t.Fatalf("BUG-003: %q placed inside an Archive expando must render DIMMED; got bright:\n%s", name, dump)
		}
	}
	// Sanity: ColorProject (the bright coord-name cyan) must NOT appear on the
	// nested-sub row — placement dimming replaces the active coord style.
	if rowHasForeground(sim, rowOf(dump, "nested-sub"), theme.ColorProject) {
		t.Fatalf("BUG-003: nested sub-coord inside Archive still renders bright (ColorProject); got:\n%s", strings.TrimRight(dump, "\n"))
	}
}
