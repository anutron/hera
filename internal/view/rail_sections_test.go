package view

import (
	"strings"
	"testing"
	"time"
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

	// BUG-025: pinned managed roles render as two-line entries in the Pinned
	// section. The breadcrumb row (railRowPinnedBreadcrumb) is the cursor
	// target; the continuation role row (railRowRole, non-selectable) carries
	// the name. Check that the breadcrumb appears INSIDE the Pinned block and
	// the role does NOT also appear outside as a non-continuation row.
	var pinnedBreadcrumb, nestedNonContinuation bool
	inPinned := false
	for _, r := range rl.rows {
		switch r.kind {
		case railRowPinnedSep:
			inPinned = true
		case railRowPinnedEnd:
			inPinned = false
		case railRowPinnedBreadcrumb:
			if r.role != nil && r.role.RoleID == 11 && inPinned {
				pinnedBreadcrumb = true
			}
		case railRowRole:
			if r.role != nil && r.role.RoleID == 11 && !r.isBreadcrumbContinuation {
				nestedNonContinuation = true
			}
		}
	}
	if !pinnedBreadcrumb {
		t.Fatalf("pinned role must render as a breadcrumb entry in the Pinned section; kinds=%v", rowKinds(rl))
	}
	if nestedNonContinuation {
		t.Fatalf("pinned role must NOT also render as a non-continuation row (double-render); kinds=%v", rowKinds(rl))
	}

	// The coordinator's live-child (N) count must EXCLUDE the floated role.
	if n := rl.visibleRoleCount(rl.orchestrators[0]); n != 1 {
		t.Fatalf("coordinator count must exclude the floated pinned role; got %d, want 1", n)
	}
}

// TestRailList_PinnedLeafUnderSubCoordinator_DoesNotVanish guards the
// full-tree collection: a pinned leaf nested under a (non-pinned) sub-
// coordinator is skipped by appendOrchChildren, so it MUST surface in the
// Pinned section rather than vanish. BUG-025: it renders as a two-line
// breadcrumb entry (railRowPinnedBreadcrumb + continuation railRowRole).
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

	// BUG-025: the pinned leaf surfaces as a railRowPinnedBreadcrumb in the
	// Pinned section; the non-continuation railRowRole must not appear.
	var floated, nested bool
	inPinned := false
	for _, r := range rl.rows {
		switch r.kind {
		case railRowPinnedSep:
			inPinned = true
		case railRowPinnedEnd:
			inPinned = false
		case railRowPinnedBreadcrumb:
			if r.role != nil && r.role.RoleID == 30 && inPinned {
				floated = true
			}
		case railRowRole:
			if r.role != nil && r.role.RoleID == 30 && !r.isBreadcrumbContinuation {
				nested = true
			}
		}
	}
	if !floated {
		t.Fatalf("deeply-nested pinned leaf must float to the Pinned section, not vanish; kinds=%v", rowKinds(rl))
	}
	if nested {
		t.Fatalf("deeply-nested pinned leaf must not also render as a non-continuation row (double-render); kinds=%v", rowKinds(rl))
	}
}

// BUG-026: Archived freelancers render ONLY in the consolidated bottom Archive
// (NOT in the Freelance section). The bottom Archive shows a "Hera" sub-group
// containing the archived freelancer; the Freelance section is absent when
// there are no live freelancers.
func TestRailList_ArchivedFreelancer_RendersInBottomArchive(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w"}}},
	})
	// An archived freelancer with a project name.
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "arch-free", ArgusTaskID: "T9", ArgusArchived: true, Project: "Hera"},
	})

	// The Freelance section must NOT render (no live freelancers).
	got := renderRail(t, rl, 32, 14)
	if strings.Contains(got, "Freelance") {
		t.Fatalf("Freelance separator must NOT render when only archived freelancers exist; got:\n%s", got)
	}

	// The bottom consolidated Archive expando must render (archived freelancer count = 1).
	bottomArchiveFound := false
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == archiveTopLevelOwner {
			bottomArchiveFound = true
			if r.archiveCount != 1 {
				t.Errorf("bottom Archive count must be 1; got %d", r.archiveCount)
			}
		}
	}
	if !bottomArchiveFound {
		t.Fatalf("bottom Archive expando must render for the archived freelancer; kinds=%v", rowKinds(rl))
	}

	// Archived freelancer must be hidden (Archive is collapsed by default).
	if strings.Contains(got, "arch-free") {
		t.Fatalf("archived freelancer must stay hidden inside the collapsed Archive; got:\n%s", got)
	}

	// Expand the bottom Archive — a "Hera" sub-group becomes visible.
	if !rl.SelectByArchiveOwner(archiveTopLevelOwner) {
		t.Fatalf("bottom Archive expando must be selectable")
	}
	rl.ToggleCollapse() // expand Archive
	heraGroupFound := false
	for _, r := range rl.rows {
		if r.kind == railRowArchiveGroup && r.archiveGroup == "Hera" {
			heraGroupFound = true
		}
	}
	if !heraGroupFound {
		t.Fatalf("'Hera' sub-group must render inside the expanded bottom Archive; kinds=%v", rowKinds(rl))
	}

	// Expand the "Hera" sub-group — the archived freelancer becomes visible.
	if !rl.SelectByArchiveGroup("Hera") {
		t.Fatalf("'Hera' archive group must be selectable")
	}
	rl.ToggleCollapse() // expand Hera group
	got = renderRail(t, rl, 32, 16)
	if !strings.Contains(got, "arch-free") {
		t.Fatalf("expanding the 'Hera' sub-group must reveal the archived freelancer; got:\n%s", got)
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

	// Row structure: PinnedSep → breadcrumb(sub-coord) → continuation(sub-coord)
	//                → sc-worker → PinnedEnd → parent header → sibling-worker
	if len(rl.rows) == 0 || rl.rows[0].kind != railRowPinnedSep {
		t.Fatalf("expected Pinned separator first; kinds=%v", rowKinds(rl))
	}

	// BUG-025: find the sub-coord breadcrumb row in the Pinned section (depth 0).
	foundPinned := false
	inPinned := false
	for _, r := range rl.rows {
		switch r.kind {
		case railRowPinnedSep:
			inPinned = true
		case railRowPinnedEnd:
			inPinned = false
		case railRowPinnedBreadcrumb:
			if r.role != nil && r.role.RoleID == 20 && inPinned {
				if r.depth != 0 {
					t.Errorf("pinned sub-coord breadcrumb must be at depth 0; got depth %d", r.depth)
				}
				foundPinned = true
			}
		}
	}
	if !foundPinned {
		t.Fatalf("pinned sub-coordinator must appear as breadcrumb in the Pinned section; kinds=%v", rowKinds(rl))
	}

	// sc-worker (child of the sub-coordinator) must render inside the Pinned block.
	workerInPinned := false
	inPinned = false
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

	// Count all breadcrumb + non-continuation role occurrences for RoleID 20.
	// Must be exactly 1 breadcrumb row (no double-render).
	var breadcrumbCount int
	for _, r := range rl.rows {
		if r.kind == railRowPinnedBreadcrumb && r.role != nil && r.role.RoleID == 20 {
			breadcrumbCount++
		}
		if r.kind == railRowRole && !r.isBreadcrumbContinuation && r.role != nil && r.role.RoleID == 20 {
			t.Errorf("pinned sub-coord must not appear as a non-continuation role row (double-render); kinds=%v", rowKinds(rl))
		}
	}
	if breadcrumbCount != 1 {
		t.Fatalf("pinned sub-coord must render exactly one breadcrumb row; got %d; kinds=%v", breadcrumbCount, rowKinds(rl))
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
			switch r.role.RoleID {
			case 10:
				pinnedMatch = true
			case 11:
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

// BUG-022 / BUG-026: When a search query is active, archived freelancers in
// the consolidated bottom Archive that do NOT match the query must not appear
// in their sub-group (non-matching project sub-groups disappear entirely).
func TestRailList_FilterAppliesTo_ArchivedFreelancers(t *testing.T) {
	rl := newRailList()
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "bug011", ArgusTaskID: "T1", ArgusArchived: true, Project: "Argus"},
		{RoleKind: "freelance", Name: "feature-xyz", ArgusTaskID: "T2", ArgusArchived: true, Project: "Hera"},
	})

	// Filter "feature": only "feature-xyz" (project Hera) should survive.
	rl.SetFilter("feature")

	// Bottom Archive must exist (filter force-expands it via archiveOpen).
	bottomArchiveFound := false
	heraGroup := false
	argusGroup := false
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == archiveTopLevelOwner {
			bottomArchiveFound = true
		}
		if r.kind == railRowArchiveGroup {
			if r.archiveGroup == "Hera" {
				heraGroup = true
			}
			if r.archiveGroup == "Argus" {
				argusGroup = true
			}
		}
	}
	if !bottomArchiveFound {
		t.Fatalf("bottom Archive must render when archived freelancers exist; kinds=%v", rowKinds(rl))
	}
	if !heraGroup {
		t.Fatalf("matching project 'Hera' must have a sub-group in the bottom Archive; kinds=%v", rowKinds(rl))
	}
	if argusGroup {
		t.Fatalf("non-matching project 'Argus' must NOT have a sub-group in the bottom Archive; kinds=%v", rowKinds(rl))
	}
}

// BUG-026: An archived freelancer whose project also has live freelancers
// must appear ONLY in the bottom Archive (not the Freelance section), and
// must render exactly once even with `l` (showArchived) on.
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

	// The archived freelancer (T2) must appear exactly once — in the bottom Archive.
	count := 0
	for _, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T2" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("archived freelancer must render exactly once (in bottom Archive), not double; got %d\nkinds=%v", count, rowKinds(rl))
	}

	// Bottom Archive expando must exist (showArchived force-expands it).
	bottomArchiveFound := false
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == archiveTopLevelOwner {
			bottomArchiveFound = true
		}
	}
	if !bottomArchiveFound {
		t.Fatalf("bottom Archive must render for archived freelancers; kinds=%v", rowKinds(rl))
	}

	// Freelance section must still render with the live freelancer (T1).
	freelanceFound := false
	for _, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T1" {
			freelanceFound = true
		}
	}
	if !freelanceFound {
		t.Fatalf("live freelancer (T1) must still render in the Freelance section; kinds=%v", rowKinds(rl))
	}
}

// BUG-024: A pinned freelancer floats to the ROOT level of the Pinned block,
// alongside pinned coords — no coordinator ancestry (freelancers are unmanaged).
func TestRailList_PinnedFreelancer_FloatsToRootPinnedSection(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live-coord", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "managed-worker"},
		}},
	})
	rl.SetFreelance([]*freelanceProject{
		{Project: "Hera", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "free-pinned", ArgusTaskID: "T1", Project: "Hera"},
			{RoleKind: "freelance", Name: "free-live", ArgusTaskID: "T2", Project: "Hera"},
		}},
	})
	// Pin T1 via ToggleFreelancePin.
	rl.ToggleFreelancePin("T1")

	// Pinned section must exist.
	if len(rl.rows) == 0 || rl.rows[0].kind != railRowPinnedSep {
		t.Fatalf("expected Pinned separator first; kinds=%v", rowKinds(rl))
	}

	// The pinned freelancer must appear inside the Pinned block at depth 0.
	foundInPinned := false
	inPinned := false
	for _, r := range rl.rows {
		switch r.kind {
		case railRowPinnedSep:
			inPinned = true
		case railRowPinnedEnd:
			inPinned = false
		case railRowRole:
			if r.role != nil && r.role.ArgusTaskID == "T1" {
				if inPinned && r.depth == 0 {
					foundInPinned = true
				} else {
					t.Fatalf("pinned freelancer must render at depth 0 inside the Pinned block; got depth=%d inPinned=%v", r.depth, inPinned)
				}
			}
		}
	}
	if !foundInPinned {
		t.Fatalf("pinned freelancer must appear in the Pinned section at root level; kinds=%v", rowKinds(rl))
	}

	// The pinned freelancer must NOT also appear in the Freelance section.
	inPinned = false
	for _, r := range rl.rows {
		switch r.kind {
		case railRowPinnedSep:
			inPinned = true
		case railRowPinnedEnd:
			inPinned = false
		case railRowRole:
			if !inPinned && r.role != nil && r.role.ArgusTaskID == "T1" {
				t.Fatalf("pinned freelancer must NOT also render in the Freelance section (double-render)")
			}
		}
	}

	// The non-pinned freelancer (T2) must still appear in the Freelance section.
	foundLive := false
	for _, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T2" {
			foundLive = true
		}
	}
	if !foundLive {
		t.Fatalf("non-pinned freelancer must remain in the Freelance section; kinds=%v", rowKinds(rl))
	}

	// Unpinning must return the freelancer to the Freelance section.
	rl.ToggleFreelancePin("T1")
	if rl.rows[0].kind == railRowPinnedSep {
		t.Fatalf("after unpin, Pinned section must disappear when no pinned items remain")
	}
	unPinnedInFree := false
	for _, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T1" {
			unPinnedInFree = true
		}
	}
	if !unPinnedInFree {
		t.Fatalf("unpinned freelancer must return to the Freelance section; kinds=%v", rowKinds(rl))
	}
}

// BUG-025: A pinned managed role renders as a two-line breadcrumb entry.
// Line 1 (railRowPinnedBreadcrumb, selectable): status icon + dimmed ancestry.
// Line 2 (railRowRole continuation, non-selectable): bright name + age.
func TestRailList_PinnedSubItem_GetsBreadcrumbForm(t *testing.T) {
	rl := newRailList()
	rl.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "kbtest", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "nested-child-worker", Pinned: true,
				StartedAt: time.Unix(1_700_000_000-3*3600, 0), // 3 hours ago
			},
		}},
	})

	// Must have a Pinned section.
	if len(rl.rows) == 0 || rl.rows[0].kind != railRowPinnedSep {
		t.Fatalf("expected Pinned separator first; kinds=%v", rowKinds(rl))
	}

	// Find the breadcrumb row inside the Pinned block.
	var bc, cont *railRow
	inPinned := false
	for i := range rl.rows {
		r := &rl.rows[i]
		switch r.kind {
		case railRowPinnedSep:
			inPinned = true
		case railRowPinnedEnd:
			inPinned = false
		case railRowPinnedBreadcrumb:
			if inPinned && r.role != nil && r.role.RoleID == 10 {
				bc = r
			}
		case railRowRole:
			if inPinned && r.role != nil && r.role.RoleID == 10 {
				cont = r
			}
		}
	}
	if bc == nil {
		t.Fatalf("expected railRowPinnedBreadcrumb for the pinned sub-item; kinds=%v", rowKinds(rl))
	}
	if cont == nil {
		t.Fatalf("expected railRowRole continuation for the pinned sub-item; kinds=%v", rowKinds(rl))
	}

	// Breadcrumb row: depth 0, selectable, carries the ancestry trail.
	if bc.depth != 0 {
		t.Errorf("breadcrumb row must be at depth 0; got %d", bc.depth)
	}
	if bc.breadcrumb != "kbtest › " {
		t.Errorf("breadcrumb ancestry: got %q, want %q", bc.breadcrumb, "kbtest › ")
	}
	for i := range rl.rows {
		if &rl.rows[i] == bc {
			if !rl.selectable(i) {
				t.Errorf("breadcrumb row at index %d must be selectable", i)
			}
		}
	}

	// Continuation row: depth 1, non-selectable, isBreadcrumbContinuation.
	if cont.depth != 1 {
		t.Errorf("continuation row must be at depth 1; got %d", cont.depth)
	}
	if !cont.isBreadcrumbContinuation {
		t.Errorf("continuation row must have isBreadcrumbContinuation=true")
	}
	for i := range rl.rows {
		if &rl.rows[i] == cont {
			if rl.selectable(i) {
				t.Errorf("continuation row at index %d must NOT be selectable", i)
			}
		}
	}

	// Rendered output: breadcrumb line shows dimmed "kbtest ›", name line shows
	// "nested-child-worker" and age "3h".
	got := renderRail(t, rl, 40, 8)
	if !strings.Contains(got, "kbtest") {
		t.Fatalf("breadcrumb line must show the parent orch name; got:\n%s", got)
	}
	if !strings.Contains(got, "nested-child-worker") {
		t.Fatalf("name line must show the item name; got:\n%s", got)
	}
	if !strings.Contains(got, "3h") {
		t.Fatalf("name line must show the age; got:\n%s", got)
	}

	// SelectByRoleID must find the breadcrumb row (cursor target).
	if !rl.SelectByRoleID(10) {
		t.Fatalf("SelectByRoleID must succeed for a pinned sub-item")
	}
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 10 {
		t.Fatalf("currentRef after SelectByRoleID must be the role; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
	}
	// Cursor must be on the breadcrumb row, not the continuation.
	if rl.rows[rl.cursor].kind != railRowPinnedBreadcrumb {
		t.Errorf("cursor must be on railRowPinnedBreadcrumb after SelectByRoleID; got kind=%v", rl.rows[rl.cursor].kind)
	}
}

// BUG-025: A pinned root coordinator stays single-line (no breadcrumb).
func TestRailList_PinnedRootOrch_StaysSingleLine(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "top-coord", Pinned: true, Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "worker"},
		}},
	})

	// No railRowPinnedBreadcrumb rows anywhere.
	for _, r := range rl.rows {
		if r.kind == railRowPinnedBreadcrumb {
			t.Fatalf("pinned root coordinator must NOT emit a breadcrumb row; kinds=%v", rowKinds(rl))
		}
	}
	// The root coord renders as a regular orchRow.
	found := false
	for _, r := range rl.rows {
		if r.kind == railRowOrch && r.orch != nil && r.orch.ID == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("pinned root coordinator must render as railRowOrch; kinds=%v", rowKinds(rl))
	}
}

// BUG-025: A pinned freelancer renders as a single line — no breadcrumb —
// because freelancers pin at root level (no coordinator ancestry, BUG-024).
func TestRailList_PinnedFreelancer_StaysSingleLine(t *testing.T) {
	rl := newRailList()
	rl.SetFreelance([]*freelanceProject{
		{Project: "Hera", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "free-task", ArgusTaskID: "T1", Project: "Hera"},
		}},
	})
	rl.ToggleFreelancePin("T1")

	for _, r := range rl.rows {
		if r.kind == railRowPinnedBreadcrumb {
			t.Fatalf("pinned freelancer must NOT emit a breadcrumb row; kinds=%v", rowKinds(rl))
		}
	}
	// The freelancer renders as a plain railRowRole with depth 0 in the Pinned block.
	found := false
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
		if inPinned && r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T1" {
			if r.isBreadcrumbContinuation {
				t.Errorf("pinned freelancer must not be a breadcrumb continuation; kinds=%v", rowKinds(rl))
			}
			if r.depth != 0 {
				t.Errorf("pinned freelancer must render at depth 0; got %d", r.depth)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("pinned freelancer must appear in the Pinned block as a plain role row; kinds=%v", rowKinds(rl))
	}
}

// BUG-025: j/k navigation treats a two-line breadcrumb entry as one unit —
// pressing j once from the entry before the two-line block moves PAST both
// lines to the next selectable entry after them.
func TestRailList_PinnedBreadcrumb_NavigationTreatsAsTwoLineUnit(t *testing.T) {
	rl := newRailList()
	// Two orchestrators: alpha (with a pinned role) and beta (active).
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "alpha", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "pin-me", Pinned: true},
		}},
		{ID: 2, Name: "beta", Roles: []*roleEntry{
			{OrchestratorID: 2, RoleID: 20, Name: "beta-worker"},
		}},
	})

	// Row layout (expected):
	//   0: railRowPinnedSep
	//   1: railRowPinnedBreadcrumb (cursor target for "pin-me")
	//   2: railRowRole continuation (non-selectable)
	//   3: railRowPinnedEnd
	//   4: railRowOrch (alpha)
	//   5: railRowOrch (beta)
	//   6: railRowRole (beta-worker)
	//
	// From the PinnedEnd or the alpha orch, pressing j should land on the
	// breadcrumb (index 1). From the breadcrumb, pressing j should skip the
	// continuation (non-selectable) and land on the next selectable (PinnedEnd
	// is non-selectable, so it lands on alpha header at index 4).

	// Verify the breadcrumb row is at index 1 and selectable.
	if len(rl.rows) < 4 {
		t.Fatalf("expected at least 4 rows; got %d; kinds=%v", len(rl.rows), rowKinds(rl))
	}
	if rl.rows[1].kind != railRowPinnedBreadcrumb {
		t.Fatalf("expected breadcrumb at index 1; got kind=%v; kinds=%v", rl.rows[1].kind, rowKinds(rl))
	}
	if !rl.selectable(1) {
		t.Fatalf("breadcrumb row at index 1 must be selectable")
	}
	if rl.selectable(2) {
		t.Fatalf("continuation row at index 2 must NOT be selectable")
	}

	// Navigate: select the pinned entry via SelectByRoleID.
	if !rl.SelectByRoleID(10) {
		t.Fatalf("SelectByRoleID(10) must succeed")
	}
	if rl.cursor != 1 {
		t.Fatalf("cursor must be at breadcrumb row (index 1); got %d", rl.cursor)
	}

	// One j from the breadcrumb must skip the continuation and land on the
	// next selectable (PinnedEnd is non-selectable, so: alpha orch header).
	rl.CursorDown()
	curRow := rl.rows[rl.cursor]
	if curRow.kind != railRowOrch || curRow.orch == nil || curRow.orch.ID != 1 {
		t.Fatalf("one j from breadcrumb must land on alpha orch; got kind=%v ref=%+v; cursor=%d kinds=%v",
			curRow.kind, rl.CurrentRef(), rl.cursor, rowKinds(rl))
	}

	// One k from alpha orch must land back on the breadcrumb (index 1).
	rl.CursorUp()
	if rl.cursor != 1 {
		t.Fatalf("k from alpha must land back on breadcrumb (index 1); got %d; kinds=%v", rl.cursor, rowKinds(rl))
	}
}

// BUG-025: A deep ancestry chain that overflows the breadcrumb line width is
// left-truncated with "…" so the nearest parent stays visible.
func TestRailList_PinnedBreadcrumb_DeepChainLeftTruncates(t *testing.T) {
	// Build a 3-level deep chain: grandparent → parent → child, with the leaf
	// inside child being pinned.
	leaf := &orchEntry{ID: 3, Name: "child", Roles: []*roleEntry{
		{OrchestratorID: 3, RoleID: 30, Name: "pinned-leaf", Pinned: true},
	}}
	mid := &orchEntry{ID: 2, Name: "parent", Roles: []*roleEntry{
		{OrchestratorID: 2, RoleID: 20, Name: "child", RoleKind: "coordinator",
			ArgusTaskID: "T-child", childOrch: leaf},
	}}
	root := &orchEntry{ID: 1, Name: "grandparent", Roles: []*roleEntry{
		{OrchestratorID: 1, RoleID: 10, Name: "parent", RoleKind: "coordinator",
			ArgusTaskID: "T-mid", childOrch: mid},
	}}
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{root})

	// Find the breadcrumb row and verify the ancestry trail.
	var bc *railRow
	for i := range rl.rows {
		if rl.rows[i].kind == railRowPinnedBreadcrumb && rl.rows[i].role != nil && rl.rows[i].role.RoleID == 30 {
			bc = &rl.rows[i]
		}
	}
	if bc == nil {
		t.Fatalf("pinned deep leaf must have a breadcrumb row; kinds=%v", rowKinds(rl))
	}
	// Full ancestry: "grandparent › parent › child › "
	wantFull := "grandparent › parent › child › "
	if bc.breadcrumb != wantFull {
		t.Errorf("breadcrumb ancestry: got %q, want %q", bc.breadcrumb, wantFull)
	}

	// Render at a narrow width where the ancestry overflows; the output must
	// contain "…" (left-truncated) and include the nearest parent "child".
	got := renderRail(t, rl, 24, 8) // very narrow
	if !strings.Contains(got, "…") {
		t.Fatalf("truncated ancestry must start with '…'; got:\n%s", got)
	}
	// The name must still fully render (it's on a separate line with its own space).
	if !strings.Contains(got, "pinned-leaf") {
		t.Fatalf("pinned leaf name must render on the name line; got:\n%s", got)
	}

	// Also verify truncRunesLeft directly.
	full := "grandparent › parent › child › "
	got2 := truncRunesLeft(full, 12)
	if !strings.HasPrefix(got2, "…") {
		t.Errorf("truncRunesLeft: result must start with '…'; got %q", got2)
	}
	if len([]rune(got2)) > 12 {
		t.Errorf("truncRunesLeft: result must be at most 12 runes; got %d in %q", len([]rune(got2)), got2)
	}

	// No-op: string shorter than max returns unchanged.
	if got3 := truncRunesLeft("abc", 10); got3 != "abc" {
		t.Errorf("truncRunesLeft no-op: got %q, want %q", got3, "abc")
	}
}

// BUG-025: A two-line entry renders both lines consecutively on screen: the
// breadcrumb line appears immediately ABOVE the name line, and the name is
// indented one step more than the breadcrumb icon.
func TestRailList_PinnedBreadcrumb_TwoLinesAreConsecutive(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "kbtest", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "nested-worker", Pinned: true},
		}},
	})
	got := renderRail(t, rl, 40, 10)
	lines := strings.Split(got, "\n")
	bcLine := -1
	nameLine := -1
	// Identify the breadcrumb line via the ancestry "›" at the end (distinct
	// from the orch header which uses the "▸" chevron, not "›").
	bcNeedle := "kbtest ›" // "kbtest ›"
	for i, ln := range lines {
		if strings.Contains(ln, bcNeedle) && bcLine < 0 {
			bcLine = i
		}
		if strings.Contains(ln, "nested-worker") {
			nameLine = i
		}
	}
	if bcLine < 0 {
		t.Fatalf("breadcrumb line (containing %q) not found; got:\n%s", bcNeedle, got)
	}
	if nameLine < 0 {
		t.Fatalf("name line (containing 'nested-worker') not found; got:\n%s", got)
	}
	if nameLine != bcLine+1 {
		t.Fatalf("name line (%d) must immediately follow breadcrumb line (%d); got:\n%s", nameLine, bcLine, got)
	}
	// The name is aligned with the breadcrumb TEXT (after the status icon), so
	// both "kbtest" and "nested-worker" start at the same column — the name is
	// visually "indented under" the icon, not further right than the ancestor text.
	// Assert that the icon column (col of the breadcrumb line's first non-border
	// cell) is strictly LEFT of the name column.
	colOf := runeColOf(lines)
	// The "○" icon starts 2 cells into the content (after border + 2-cell gutter);
	// "kbtest" starts 2 more (after icon + space). The name at depth=1 starts
	// at the same column as "kbtest". Verify name col >= bcNeedle col.
	bcTextCol := colOf(bcNeedle)
	nameCol := colOf("nested-worker")
	if nameCol < bcTextCol {
		t.Fatalf("name col (%d) must be >= breadcrumb-text col (%d); got:\n%s", nameCol, bcTextCol, got)
	}
}

// BUG-025: Deep nested breadcrumb — a role under a sub-coordinator under a root
// coordinator shows both ancestor names in the ancestry trail.
func TestRailList_PinnedBreadcrumb_DeepAncestry(t *testing.T) {
	sub := &orchEntry{ID: 2, Name: "nested-sub", Roles: []*roleEntry{
		{OrchestratorID: 2, RoleID: 20, Name: "nested-child-worker", Pinned: true},
	}}
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "kbtest", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "nested-sub", RoleKind: "coordinator",
				ArgusTaskID: "T-sub", childOrch: sub},
		}},
	})

	var bc *railRow
	for i := range rl.rows {
		if rl.rows[i].kind == railRowPinnedBreadcrumb && rl.rows[i].role != nil && rl.rows[i].role.RoleID == 20 {
			bc = &rl.rows[i]
		}
	}
	if bc == nil {
		t.Fatalf("deep nested pinned role must have a breadcrumb row; kinds=%v", rowKinds(rl))
	}
	// Ancestry must include both kbtest and nested-sub.
	want := "kbtest › nested-sub › "
	if bc.breadcrumb != want {
		t.Errorf("deep ancestry: got %q, want %q", bc.breadcrumb, want)
	}

	// Rendered output must show both ancestor names on the breadcrumb line.
	got := renderRail(t, rl, 50, 10)
	if !strings.Contains(got, "kbtest") || !strings.Contains(got, "nested-sub") {
		t.Fatalf("breadcrumb line must show kbtest and nested-sub; got:\n%s", got)
	}
	if !strings.Contains(got, "nested-child-worker") {
		t.Fatalf("name line must show nested-child-worker; got:\n%s", got)
	}
}

// BUG-024 / BUG-026: The (F) freelance marker renders for freelance rows both
// in the Freelance section and in the Pinned block. The glyph is
// nf-md-alpha_f_box_outline (U+F0BFA), confirmed present in HackNerdFont-Regular.
func TestRailList_FreelanceMarker_RendersOnFreelanceRows(t *testing.T) {
	rl := newRailList()
	rl.SetFreelance([]*freelanceProject{
		{Project: "Hera", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "free-task", ArgusTaskID: "T1", Project: "Hera"},
		}},
	})

	got := renderRail(t, rl, 40, 10)
	// The iconFreelance glyph (U+F0BFA, nf-md-alpha_f_box_outline) must render
	// on freelance rows in the Freelance section.
	if !strings.ContainsRune(got, iconFreelance) {
		t.Fatalf("freelance marker (iconFreelance U+F0BFA) must render on freelance rows in Freelance section; got:\n%s", got)
	}

	// Pin the freelancer — marker must also render in the Pinned block.
	rl.ToggleFreelancePin("T1")
	got = renderRail(t, rl, 40, 10)
	if !strings.ContainsRune(got, iconFreelance) {
		t.Fatalf("freelance marker must also render on a pinned freelancer in the Pinned block; got:\n%s", got)
	}
}

// BUG-026: Consolidated bottom Archive — one expando, "Hera sessions" sub-group
// for archived root coords, per-project sub-groups for archived freelancers,
// no per-project Archive expandos in the Freelance section.
func TestRailList_BUG026_ConsolidatedBottomArchive(t *testing.T) {
	rl := newRailList()
	// Two active coords, one archived coord.
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "active-1", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w1"}}},
		{ID: 2, Name: "active-2", Roles: []*roleEntry{{OrchestratorID: 2, RoleID: 20, Name: "w2"}}},
		{ID: 3, Name: "archived-coord", Archived: true, Roles: []*roleEntry{
			{OrchestratorID: 3, RoleID: 30, Name: "arch-worker"},
		}},
	})
	// Live freelancers in two projects.
	rl.SetFreelance([]*freelanceProject{
		{Project: "Hera", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "hera-free", ArgusTaskID: "T-hf", Project: "Hera"},
		}},
		{Project: "ARGUS", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "argus-free", ArgusTaskID: "T-af", Project: "ARGUS"},
		}},
	})
	// Archived freelancers in two projects.
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "hera-arch", ArgusTaskID: "T-ha", ArgusArchived: true, Project: "Hera"},
		{RoleKind: "freelance", Name: "argus-arch", ArgusTaskID: "T-aa", ArgusArchived: true, Project: "ARGUS"},
	})

	// Exactly ONE bottom Archive expando must exist.
	bottomArchiveCount := 0
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == archiveTopLevelOwner {
			bottomArchiveCount++
		}
	}
	if bottomArchiveCount != 1 {
		t.Fatalf("exactly ONE bottom Archive expando must exist; got %d; kinds=%v", bottomArchiveCount, rowKinds(rl))
	}

	// No per-project Archive expandos in the Freelance section (negative int64
	// keys — the BUG-019 pattern). Since we removed freelanceProjArchiveOwner,
	// just assert no archiveExpando with a negative owner exists.
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner < 0 {
			t.Fatalf("per-project Archive expandos must NOT appear in the Freelance section; got owner=%d kinds=%v", r.archiveOwner, rowKinds(rl))
		}
	}

	// Expand the bottom Archive.
	if !rl.SelectByArchiveOwner(archiveTopLevelOwner) {
		t.Fatalf("bottom Archive expando must be selectable")
	}
	rl.ToggleCollapse()

	// "Hera sessions" sub-group must appear (for the archived coord).
	heraSessionsFound := false
	heraProjectFound := false
	argusProjectFound := false
	for _, r := range rl.rows {
		if r.kind != railRowArchiveGroup {
			continue
		}
		switch r.archiveGroup {
		case "Hera sessions":
			heraSessionsFound = true
			if r.archiveCount != 1 {
				t.Errorf("Hera sessions sub-group count must be 1; got %d", r.archiveCount)
			}
		case "Hera":
			heraProjectFound = true
		case "ARGUS":
			argusProjectFound = true
		}
	}
	if !heraSessionsFound {
		t.Fatalf("'Hera sessions' sub-group must render inside the expanded Archive; kinds=%v", rowKinds(rl))
	}
	if !heraProjectFound {
		t.Fatalf("'Hera' freelancer sub-group must render inside the expanded Archive; kinds=%v", rowKinds(rl))
	}
	if !argusProjectFound {
		t.Fatalf("'ARGUS' freelancer sub-group must render inside the expanded Archive; kinds=%v", rowKinds(rl))
	}
}

// BUG-026: Per-coord agent archives inside a coordinator remain intact — only
// archived top-level coordinator entries move to the bottom Archive's "Hera sessions".
func TestRailList_BUG026_PerCoordAgentArchivesIntact(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "my-coord", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "live-agent", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "dead-agent", Archived: true},
		}},
	})

	// There should be a per-coordinator Archive expando for my-coord (ID=1).
	perCoordArchiveFound := false
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == 1 {
			perCoordArchiveFound = true
			if r.archiveCount != 1 {
				t.Errorf("per-coord Archive count must be 1; got %d", r.archiveCount)
			}
		}
	}
	if !perCoordArchiveFound {
		t.Fatalf("per-coordinator Archive expando must still render for archived agents inside a coord; kinds=%v", rowKinds(rl))
	}

	// No bottom Archive expando (no archived ROOT coordinators, no archived freelancers).
	for _, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == archiveTopLevelOwner {
			t.Fatalf("bottom Archive must NOT render when only per-coord agents are archived; kinds=%v", rowKinds(rl))
		}
	}
}

// BUG-026: Archived freelancers carry the (F) marker inside the bottom Archive's
// per-project sub-groups, same as in the live Freelance section.
func TestRailList_BUG026_ArchivedFreelancerCarriesMarkerInArchive(t *testing.T) {
	rl := newRailList()
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "arch-free", ArgusTaskID: "T1", ArgusArchived: true, Project: "Sketch"},
	})
	// Expand the bottom Archive and its "Sketch" sub-group so the row is visible.
	rl.archiveExpanded[archiveTopLevelOwner] = true
	rl.archiveGroupExpanded["Sketch"] = true
	rl.buildRows()

	// Find the archived freelancer role row.
	found := false
	for _, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.ArgusTaskID == "T1" {
			if r.role.RoleKind != "freelance" {
				t.Errorf("archived freelancer must have RoleKind=freelance; got %q", r.role.RoleKind)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("archived freelancer row must be visible when Archive and sub-group are expanded; kinds=%v", rowKinds(rl))
	}

	// The rendered output must include the iconFreelance glyph.
	got := renderRail(t, rl, 48, 12)
	if !strings.ContainsRune(got, iconFreelance) {
		t.Fatalf("(F) marker (iconFreelance U+F0BFA) must render on archived freelancer rows in the bottom Archive; got:\n%s", got)
	}
}

// BUG-026: No "Rail" label in the border title. The title is empty by default;
// only the filter query (/search) appears when active.
func TestRailList_BUG026_NoRailLabel(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "my-orch", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w"}}},
	})
	got := renderRail(t, rl, 40, 8)
	// The border renders, but "Rail" must not appear in it.
	if strings.Contains(got, "Rail") {
		t.Fatalf("'Rail' label must NOT appear in the rail border; got:\n%s", got)
	}

	// With a filter active, only the filter query appears (no "Rail" prefix).
	rl.SetFilter("scout")
	got = renderRail(t, rl, 40, 8)
	if strings.Contains(got, "Rail") {
		t.Fatalf("'Rail' label must NOT appear even with a filter active; got:\n%s", got)
	}
}

// BUG-026: Section rules (railRowSectionRule) appear between active coords and
// the Freelance section, and between Freelance and the Archive section.
func TestRailList_BUG026_SectionRulesBetweenZones(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "coord", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w"}}},
	})
	rl.SetFreelance([]*freelanceProject{
		{Project: "Hera", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "free", ArgusTaskID: "T1", Project: "Hera"},
		}},
	})
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "arch", ArgusTaskID: "T2", ArgusArchived: true, Project: "Hera"},
	})

	// Expect: coord section → SectionRule → FreelanceSep → freelance →
	//         SectionRule → ArchiveExpando
	var kinds []railRowKind
	for _, r := range rl.rows {
		kinds = append(kinds, r.kind)
	}

	// Find the FreelanceSep index.
	freeSepIdx := -1
	archiveExpandoIdx := -1
	for i, k := range kinds {
		if k == railRowFreelanceSep {
			freeSepIdx = i
		}
		if k == railRowArchiveExpando {
			archiveExpandoIdx = i
		}
	}
	if freeSepIdx < 0 {
		t.Fatalf("FreelanceSep must exist; kinds=%v", kinds)
	}
	if archiveExpandoIdx < 0 {
		t.Fatalf("ArchiveExpando must exist; kinds=%v", kinds)
	}

	// The row immediately before FreelanceSep must be a SectionRule.
	if freeSepIdx == 0 || kinds[freeSepIdx-1] != railRowSectionRule {
		t.Fatalf("SectionRule must appear immediately before FreelanceSep (row %d); kinds=%v", freeSepIdx, kinds)
	}

	// The row immediately before ArchiveExpando must be a SectionRule.
	if archiveExpandoIdx == 0 || kinds[archiveExpandoIdx-1] != railRowSectionRule {
		t.Fatalf("SectionRule must appear immediately before ArchiveExpando (row %d); kinds=%v", archiveExpandoIdx, kinds)
	}
}

// BUG-026: iconFreelance constant uses U+F0BFA (nf-md-alpha_f_box_outline),
// NOT U+F0229 (which is md-file_presentation_box in the Hack Nerd Font).
func TestRailList_BUG026_FreelanceGlyphCodepoint(t *testing.T) {
	const wantCodepoint = rune(0x0F0BFA)
	if iconFreelance != wantCodepoint {
		t.Fatalf("iconFreelance must be U+F0BFA (nf-md-alpha_f_box_outline); got U+%X", iconFreelance)
	}
}

// BUG-026: Archive sub-group fold state persists via ViewState/RestoreViewState.
func TestRailList_BUG026_ArchiveGroupFoldStatePersists(t *testing.T) {
	rl := newRailList()
	rl.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "arch-free", ArgusTaskID: "T1", ArgusArchived: true, Project: "Hera"},
	})

	// Expand the bottom Archive then expand the "Hera" sub-group.
	rl.archiveExpanded[archiveTopLevelOwner] = true
	rl.archiveGroupExpanded["Hera"] = true
	rl.buildRows()

	// Capture and restore state.
	state := rl.ViewState()
	if !state.ArchiveGroupExpanded["Hera"] {
		t.Fatalf("ViewState must capture ArchiveGroupExpanded['Hera']=true")
	}

	rl2 := newRailList()
	rl2.RestoreViewState(state)
	rl2.SetArchivedFreelance([]*roleEntry{
		{RoleKind: "freelance", Name: "arch-free", ArgusTaskID: "T1", ArgusArchived: true, Project: "Hera"},
	})
	// Manually expand the Archive expando on the new rail so rows are visible.
	rl2.archiveExpanded[archiveTopLevelOwner] = true
	rl2.buildRows()

	// The Hera sub-group must be expanded in the restored rail.
	if !rl2.archiveGroupExpanded["Hera"] {
		t.Fatalf("RestoreViewState must restore ArchiveGroupExpanded['Hera']=true")
	}
}
