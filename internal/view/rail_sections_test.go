package view

import (
	"strings"
	"testing"
)

// Tests for the rail-sections change: a Pinned section at the rail top (Story
// 1) and archived freelancers rendering in the bottom Archive section (Story 2).

// rowKinds returns the kind of every built row, for structural assertions.
func rowKinds(rl *railList) []railRowKind {
	out := make([]railRowKind, 0, len(rl.rows))
	for _, r := range rl.rows {
		out = append(out, r.kind)
	}
	return out
}

func TestRailList_PinnedOrchestrator_FloatsToTopPinnedSection(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w"}}},
		{ID: 2, Name: "pinme", Pinned: true, Roles: []*roleEntry{{OrchestratorID: 2, RoleID: 20, Name: "pw"}}},
	})

	// The first row MUST be the Pinned separator, and the pinned orchestrator
	// MUST render directly under it (above the active tree).
	if len(rl.rows) == 0 || rl.rows[0].kind != railRowPinnedSep {
		t.Fatalf("first row must be the Pinned separator; kinds=%v", rowKinds(rl))
	}
	if rl.rows[1].kind != railRowOrch || rl.rows[1].orch == nil || rl.rows[1].orch.ID != 2 {
		t.Fatalf("pinned orchestrator must render under the Pinned separator; got %+v", rl.rows[1])
	}

	// The pinned orchestrator must NOT also appear in the active tree (no
	// double-render): exactly one orch row carries ID 2.
	count := 0
	for _, r := range rl.rows {
		if r.kind == railRowOrch && r.orch != nil && r.orch.ID == 2 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("pinned orchestrator must render exactly once; got %d", count)
	}

	got := renderRail(t, rl, 24, 10)
	if !strings.Contains(got, "Pinned") {
		t.Fatalf("expected Pinned separator label; got:\n%s", got)
	}
}

func TestRailList_PinnedRole_FloatsOutOfCoordinator(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "foo", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w-normal"},
			{OrchestratorID: 1, RoleID: 11, Name: "w-pinned", Pinned: true},
		}},
	})

	// A Pinned section must exist with the pinned role floated to the top.
	if rl.rows[0].kind != railRowPinnedSep {
		t.Fatalf("expected Pinned separator first; kinds=%v", rowKinds(rl))
	}
	var pinnedRow, nestedPinned bool
	for i, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.RoleID == 11 {
			if i == 1 { // directly under the Pinned separator
				pinnedRow = true
			} else {
				nestedPinned = true
			}
		}
	}
	if !pinnedRow {
		t.Fatalf("pinned role must render as a standalone row in the Pinned section; kinds=%v", rowKinds(rl))
	}
	if nestedPinned {
		t.Fatalf("pinned role must NOT also render nested under its coordinator (double-render)")
	}

	// The coordinator's live-child (N) count must EXCLUDE the floated role.
	if n := rl.visibleRoleCount(rl.orchestrators[0]); n != 1 {
		t.Fatalf("coordinator count must exclude the floated pinned role; got %d, want 1", n)
	}
}

// TestRailList_PinnedLeafUnderSubCoordinator_DoesNotVanish guards the
// full-tree collection: a pinned leaf nested under a (non-pinned) sub-
// coordinator is skipped by appendOrchChildren, so it MUST surface in the
// Pinned section rather than vanish.
func TestRailList_PinnedLeafUnderSubCoordinator_DoesNotVanish(t *testing.T) {
	rl := newRailList()
	child := &orchEntry{ID: 2, Name: "child", CoordTaskID: "T-sc", Roles: []*roleEntry{
		{OrchestratorID: 2, RoleID: 30, Name: "deep-pinned", Pinned: true},
		{OrchestratorID: 2, RoleID: 31, Name: "deep-normal"},
	}}
	parent := &orchEntry{ID: 1, Name: "parent", Roles: []*roleEntry{
		{OrchestratorID: 1, RoleID: 20, Name: "sc", RoleKind: "coordinator", ArgusTaskID: "T-sc", childOrch: child},
	}}
	rl.SetOrchestrators([]*orchEntry{parent})

	var floated, nested bool
	for i, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.RoleID == 30 {
			if i >= 1 && rl.rows[0].kind == railRowPinnedSep && i <= len(rl.rows) && r.depth == 0 {
				floated = true
			} else {
				nested = true
			}
		}
	}
	if !floated {
		t.Fatalf("deeply-nested pinned leaf must float to the Pinned section, not vanish; kinds=%v", rowKinds(rl))
	}
	if nested {
		t.Fatalf("deeply-nested pinned leaf must not also render nested (double-render)")
	}
}

func TestRailList_ArchivedFreelancer_RendersInBottomArchive(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w"}}},
	})
	// An archived freelancer (the operator pressed `a` on it) — would otherwise
	// vanish. It must surface in the bottom Archive section WITHOUT `l`.
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "arch-free", ArgusTaskID: "T9", ArgusArchived: true},
	})

	// Default view (showArchived=false): the bottom Archive expando renders
	// (collapsed) counting the freelancer, but the freelancer row stays hidden.
	got := renderRail(t, rl, 28, 10)
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("bottom Archive expando must render counting the archived freelancer; got:\n%s", got)
	}
	if strings.Contains(got, "arch-free") {
		t.Fatalf("archived freelancer must stay hidden inside the collapsed Archive; got:\n%s", got)
	}

	// Fold the bottom Archive open: the freelancer becomes a visible, selectable row.
	if !rl.SelectByArchiveOwner(archiveTopLevelOwner) {
		t.Fatalf("bottom Archive expando must be selectable")
	}
	rl.ToggleCollapse()
	got = renderRail(t, rl, 28, 10)
	if !strings.Contains(got, "arch-free") {
		t.Fatalf("folding the bottom Archive open must reveal the archived freelancer; got:\n%s", got)
	}
}

// TestRailList_ArchivedFreelancer_NoDoubleRenderWithListAll proves the
// reconciliation: an archived freelancer renders ONLY in the bottom Archive,
// never inline in a Freelance repo group — even with `l` (showArchived) on.
func TestRailList_ArchivedFreelancer_NoDoubleRenderWithListAll(t *testing.T) {
	rl := newRailList()
	rl.SetShowArchived(true) // `l`
	rl.SetFreelance([]*freelanceProject{
		{Project: "Beta", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "beta-live", ArgusTaskID: "T1"},
		}},
	})
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "beta-arch", ArgusTaskID: "T2", ArgusArchived: true},
	})

	// Count how many rows carry the archived freelancer's task.
	count := 0
	for _, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T2" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("archived freelancer must render exactly once (in the bottom Archive), not double; got %d\nkinds=%v", count, rowKinds(rl))
	}
}
