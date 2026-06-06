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

// BUG-019: Archived freelancers render in the Freelance section under a
// per-project Archive expando — NOT in the generic bottom Archive alongside
// archived root coordinators.
func TestRailList_ArchivedFreelancer_RendersInFreelanceSection(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w"}}},
	})
	// An archived freelancer with a project name so it nests properly.
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "arch-free", ArgusTaskID: "T9", ArgusArchived: true, Project: "Hera"},
	})

	// Default view: Freelance section renders with a per-project Archive
	// expando for "Hera" counting the archived freelancer. The bottom Archive
	// (archiveTopLevelOwner) must NOT contain the freelancer.
	got := renderRail(t, rl, 32, 12)
	if !strings.Contains(got, "Freelance") {
		t.Fatalf("Freelance separator must render when archived freelancers exist; got:\n%s", got)
	}
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("per-project Archive expando must render for the archived freelancer; got:\n%s", got)
	}
	if strings.Contains(got, "arch-free") {
		t.Fatalf("archived freelancer must stay hidden inside the collapsed Archive; got:\n%s", got)
	}

	// The bottom Archive (archiveTopLevelOwner) should NOT render since there
	// are no archived root coordinators.
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == archiveTopLevelOwner {
			t.Fatalf("bottom Archive must NOT render for archived freelancers; got row: %+v", r)
		}
	}

	// Open the per-project Archive expando — the freelancer becomes visible.
	owner := freelanceProjArchiveOwner("Hera")
	if !rl.SelectByArchiveOwner(owner) {
		t.Fatalf("per-project Archive expando for 'Hera' must be selectable (owner=%d)", owner)
	}
	rl.ToggleCollapse()
	got = renderRail(t, rl, 32, 12)
	if !strings.Contains(got, "arch-free") {
		t.Fatalf("opening per-project Archive must reveal the archived freelancer; got:\n%s", got)
	}
}

// BUG-021: A pinned sub-coordinator floats to the Pinned section WITH its own
// children (not just as a childless stub), and must NOT also appear nested in
// the active tree (no double-render). Full ancestry (parent chain) is deferred.
func TestRailList_PinnedSubCoord_FloatsToTopWithChildren(t *testing.T) {
	rl := newRailList()
	child := &orchEntry{ID: 2, Name: "sub-coord", CoordTaskID: "T-sc", Roles: []*roleEntry{
		{OrchestratorID: 2, RoleID: 30, Name: "sc-worker", RoleKind: "worker", Live: true},
	}}
	scRole := &roleEntry{
		OrchestratorID: 1, RoleID: 20, Name: "sub-coord", RoleKind: "coordinator",
		ArgusTaskID: "T-sc", childOrch: child, Pinned: true,
	}
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "parent", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "sibling-worker", RoleKind: "worker", Live: true},
			scRole,
		}},
	})

	// Row structure: PinnedSep → sub-coord row → sc-worker → PinnedEnd → parent header → sibling-worker
	if len(rl.rows) == 0 || rl.rows[0].kind != railRowPinnedSep {
		t.Fatalf("expected Pinned separator first; kinds=%v", rowKinds(rl))
	}

	// Find the sub-coord row in the Pinned section (must be at depth 0).
	foundPinned := false
	for i, r := range rl.rows {
		if r.kind == railRowPinnedEnd {
			break // done with Pinned block
		}
		if r.kind == railRowRole && r.role != nil && r.role.RoleID == 20 {
			if r.depth != 0 {
				t.Errorf("pinned sub-coord must render at depth 0 in Pinned section; got depth %d", r.depth)
			}
			_ = i
			foundPinned = true
		}
	}
	if !foundPinned {
		t.Fatalf("pinned sub-coordinator must appear in the Pinned section; kinds=%v", rowKinds(rl))
	}

	// sc-worker (child of the sub-coordinator) must render inside the Pinned block.
	workerInPinned := false
	inPinned := false
	for _, r := range rl.rows {
		if r.kind == railRowPinnedSep {
			inPinned = true
			continue
		}
		if r.kind == railRowPinnedEnd {
			inPinned = false
			continue
		}
		if inPinned && r.kind == railRowRole && r.role != nil && r.role.RoleID == 30 {
			workerInPinned = true
		}
	}
	if !workerInPinned {
		t.Fatalf("sub-coordinator's worker must render inside the Pinned block; kinds=%v", rowKinds(rl))
	}

	// sub-coord must NOT appear in the active tree (no double-render).
	for _, r := range rl.rows {
		if r.kind != railRowPinnedSep && r.kind != railRowPinnedEnd {
			// Only check rows OUTSIDE the Pinned block.
		}
	}
	// Count pinned sub-coord occurrences (kind==railRowRole, RoleID==20).
	var occurrences []int
	for i, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.RoleID == 20 {
			occurrences = append(occurrences, i)
		}
	}
	if len(occurrences) != 1 {
		t.Fatalf("pinned sub-coord must render exactly once; got at rows %v\nkinds=%v", occurrences, rowKinds(rl))
	}

	// Rendered output must show sub-coord in the Pinned block and sc-worker nested under it.
	got := renderRail(t, rl, 36, 14)
	if !strings.Contains(got, "Pinned") {
		t.Fatalf("expected Pinned separator; got:\n%s", got)
	}
	if !strings.Contains(got, "sc-worker") {
		t.Fatalf("sub-coordinator's worker must render in the Pinned block; got:\n%s", got)
	}
}

// BUG-021: The Pinned block must have a closing separator rule (railRowPinnedEnd)
// immediately below the last Pinned row, delineating it from the active tree.
func TestRailList_PinnedBlockHasClosingDelineator(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "active", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w", Pinned: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w2", Live: true},
		}},
	})

	// Locate PinnedSep and PinnedEnd.
	pinnedSepIdx := -1
	pinnedEndIdx := -1
	for i, r := range rl.rows {
		if r.kind == railRowPinnedSep {
			pinnedSepIdx = i
		}
		if r.kind == railRowPinnedEnd {
			pinnedEndIdx = i
		}
	}
	if pinnedSepIdx < 0 {
		t.Fatalf("expected Pinned section to exist; kinds=%v", rowKinds(rl))
	}
	if pinnedEndIdx < 0 {
		t.Fatalf("expected PinnedEnd closing rule; kinds=%v", rowKinds(rl))
	}
	if pinnedEndIdx <= pinnedSepIdx {
		t.Fatalf("PinnedEnd (row %d) must come after PinnedSep (row %d)", pinnedEndIdx, pinnedSepIdx)
	}

	// Active tree rows must come AFTER the PinnedEnd row.
	for i, r := range rl.rows {
		if i <= pinnedEndIdx {
			continue
		}
		if r.kind == railRowOrch && r.orch != nil && r.orch.ID == 1 {
			return // found active coord after PinnedEnd
		}
	}
	t.Fatalf("active coordinator must render after PinnedEnd; kinds=%v", rowKinds(rl))
}

// BUG-022: When a search query is active, pinned rows that do NOT match must
// be filtered out of the Pinned section.
func TestRailList_FilterAppliesTo_PinnedRoles(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "hera-orch", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "match-worker", Pinned: true},
			{OrchestratorID: 1, RoleID: 11, Name: "other-worker", Pinned: true},
		}},
	})

	// Filter "match": only "match-worker" should survive in the Pinned section.
	rl.SetFilter("match")

	pinnedMatch := false
	pinnedOther := false
	inPinned := false
	for _, r := range rl.rows {
		if r.kind == railRowPinnedSep {
			inPinned = true
			continue
		}
		if r.kind == railRowPinnedEnd {
			inPinned = false
			continue
		}
		if inPinned && r.kind == railRowRole && r.role != nil {
			if r.role.RoleID == 10 {
				pinnedMatch = true
			} else if r.role.RoleID == 11 {
				pinnedOther = true
			}
		}
	}
	if !pinnedMatch {
		t.Fatalf("matching pinned role must survive the filter; kinds=%v", rowKinds(rl))
	}
	if pinnedOther {
		t.Fatalf("non-matching pinned role must be filtered out of the Pinned section; kinds=%v", rowKinds(rl))
	}
}

// BUG-022: When a search query is active, archived freelancers in the Freelance
// section that do NOT match the query must not render (per-project Archive expandos
// for non-matching projects also disappear).
func TestRailList_FilterAppliesTo_ArchivedFreelancers(t *testing.T) {
	rl := newRailList()
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "bug011", ArgusTaskID: "T1", ArgusArchived: true, Project: "Argus"},
		{RoleKind: "freelance", Name: "feature-xyz", ArgusTaskID: "T2", ArgusArchived: true, Project: "Hera"},
	})

	// Filter "feature": only "feature-xyz" (project Hera) should survive.
	rl.SetFilter("feature")

	heraOwner := freelanceProjArchiveOwner("Hera")
	argusOwner := freelanceProjArchiveOwner("Argus")

	heraExpando := false
	argusExpando := false
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando {
			if r.archiveOwner == heraOwner {
				heraExpando = true
			}
			if r.archiveOwner == argusOwner {
				argusExpando = true
			}
		}
	}
	if !heraExpando {
		t.Fatalf("matching project 'Hera' must keep its Archive expando; kinds=%v", rowKinds(rl))
	}
	if argusExpando {
		t.Fatalf("non-matching project 'Argus' must lose its Archive expando; kinds=%v", rowKinds(rl))
	}
}

// BUG-019: An archived freelancer whose project also has live freelancers
// should be in the SAME project group, not the bottom Archive, and must render
// exactly once even with `l` (showArchived) on.
func TestRailList_ArchivedFreelancer_NoDoubleRenderWithListAll(t *testing.T) {
	rl := newRailList()
	rl.SetShowArchived(true) // `l`
	rl.SetFreelance([]*freelanceProject{
		{Project: "Beta", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "beta-live", ArgusTaskID: "T1"},
		}},
	})
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "beta-arch", ArgusTaskID: "T2", ArgusArchived: true, Project: "Beta"},
	})

	// Count how many rows carry the archived freelancer's task.
	count := 0
	for _, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T2" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("archived freelancer must render exactly once (in Freelance section), not double; got %d\nkinds=%v", count, rowKinds(rl))
	}

	// Must render in the Freelance section, not in the bottom Archive.
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == archiveTopLevelOwner {
			t.Fatalf("archived freelancer must NOT appear in the bottom Archive (archiveTopLevelOwner=0); found such expando")
		}
	}
}
