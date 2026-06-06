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

// BUG-024: The (F) freelance marker renders for freelance rows both in the
// Freelance section and in the Pinned block.
func TestRailList_FreelanceMarker_RendersOnFreelanceRows(t *testing.T) {
	rl := newRailList()
	rl.SetFreelance([]*freelanceProject{
		{Project: "Hera", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "free-task", ArgusTaskID: "T1", Project: "Hera"},
		}},
	})

	got := renderRail(t, rl, 40, 10)
	// The iconFreelance glyph (U+F0229) must appear on the freelance row.
	// We check for its presence in the rendered output.
	if !strings.ContainsRune(got, iconFreelance) {
		t.Fatalf("freelance marker (iconFreelance U+F0229) must render on freelance rows in Freelance section; got:\n%s", got)
	}

	// Pin the freelancer — marker must also render in the Pinned block.
	rl.ToggleFreelancePin("T1")
	got = renderRail(t, rl, 40, 10)
	if !strings.ContainsRune(got, iconFreelance) {
		t.Fatalf("freelance marker must also render on a pinned freelancer in the Pinned block; got:\n%s", got)
	}
}
