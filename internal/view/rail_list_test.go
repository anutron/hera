package view

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func renderRail(t *testing.T, rl *railList, w, h int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(w, h)
	rl.SetRect(0, 0, w, h)
	rl.Draw(sim)
	sim.Show()
	return readScreen(sim)
}

func TestRailList_HeaderShowsChevronAndCount(t *testing.T) {
	rl := newRailList()
	rl.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	rl.SetOrchestrators([]*orchEntry{
		{
			ID:   1,
			Name: "proj1",
			Roles: []*roleEntry{
				{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true, StartedAt: time.Unix(1_700_000_000-65, 0)},
				{OrchestratorID: 1, RoleID: 11, Name: "w2", StartedAt: time.Unix(1_700_000_000-3600, 0)},
			},
		},
	})

	got := renderRail(t, rl, 22, 6)
	if !strings.Contains(got, "▾ proj1 (2)") {
		t.Fatalf("expected expanded chevron + name + count; got:\n%s", got)
	}
	if !strings.Contains(got, "w1") || !strings.Contains(got, "w2") {
		t.Fatalf("expected both role names rendered; got:\n%s", got)
	}
	// 65 seconds → "1m", 3600 seconds → "1h"
	if !strings.Contains(got, "1m") {
		t.Fatalf("expected elapsed '1m' for w1; got:\n%s", got)
	}
	if !strings.Contains(got, "1h") {
		t.Fatalf("expected elapsed '1h' for w2; got:\n%s", got)
	}
}

func TestRailList_CollapseHidesRoles(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w1"}}},
	})
	rl.SelectByOrchID(1)
	rl.ToggleCollapse()

	got := renderRail(t, rl, 22, 5)
	if !strings.Contains(got, "▸ p (1)") {
		t.Fatalf("expected collapsed chevron; got:\n%s", got)
	}
	if strings.Contains(got, "w1") {
		t.Fatalf("expected w1 hidden when collapsed; got:\n%s", got)
	}
}

func TestRailList_ArchiveSeparatorRendersWhenShowArchived(t *testing.T) {
	rl := newRailList()
	rl.SetShowArchived(true)
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w"}}},
		{ID: 2, Name: "old", Archived: true, Roles: []*roleEntry{{OrchestratorID: 2, RoleID: 20, Name: "g", Archived: true}}},
	})

	got := renderRail(t, rl, 22, 8)
	if !strings.Contains(got, "Archive") {
		t.Fatalf("expected Archive separator; got:\n%s", got)
	}
	if !strings.Contains(got, "old") {
		t.Fatalf("expected archived orchestrator name; got:\n%s", got)
	}
}

func TestRailList_HidesArchivedItemsButShowsExpandoWhenCollapsed(t *testing.T) {
	// Per D14 the Archive expandos are always reachable (collapsed by
	// default). With showArchived=false the expando headers render but the
	// archived items themselves stay hidden until the operator folds them
	// open with `space`.
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w-active"},
			{OrchestratorID: 1, RoleID: 11, Name: "w-old", Archived: true},
		}},
		{ID: 2, Name: "archived-orch", Archived: true, Roles: nil},
	})

	got := renderRail(t, rl, 28, 8)
	if strings.Contains(got, "w-old") {
		t.Fatalf("archived role should be hidden inside a collapsed expando; got:\n%s", got)
	}
	if strings.Contains(got, "archived-orch") {
		t.Fatalf("archived orch should be hidden inside a collapsed top-level Archive; got:\n%s", got)
	}
	// But the expando headers themselves are always present.
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("per-coordinator and top-level Archive (N) expandos must render even when collapsed; got:\n%s", got)
	}
}

func TestRailList_CursorMovesAcrossSelectableRows(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1"},
			{OrchestratorID: 1, RoleID: 11, Name: "w2"},
		}},
	})
	// First selectable row should be the orch header.
	if ref, ok := rl.CurrentRef().(*orchEntry); !ok || ref.ID != 1 {
		t.Fatalf("initial cursor should be on orchestrator header; got %T", rl.CurrentRef())
	}
	rl.CursorDown()
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 10 {
		t.Fatalf("after one down, expect role w1; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
	}
	rl.CursorDown()
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 11 {
		t.Fatalf("after two downs, expect role w2; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
	}
	// Past the end — cursor should stay on the last selectable row.
	rl.CursorDown()
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 11 {
		t.Fatalf("cursor should clamp to last role; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
	}
	rl.CursorUp()
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 10 {
		t.Fatalf("cursor up to w1; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
	}
}

func TestRailList_OnSelectionChangedFiresOnMove(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1"},
			{OrchestratorID: 1, RoleID: 11, Name: "w2"},
		}},
	})
	var observed []any
	rl.SetOnSelectionChanged(func(ref any) { observed = append(observed, ref) })

	// CursorDown lands on w1.
	rl.CursorDown()
	if len(observed) != 1 {
		t.Fatalf("expected 1 selection-change fire after CursorDown; got %d", len(observed))
	}
	if r, ok := observed[0].(*roleEntry); !ok || r.RoleID != 10 {
		t.Fatalf("first fire should be w1; got %T %+v", observed[0], observed[0])
	}

	// CursorDown lands on w2.
	rl.CursorDown()
	if len(observed) != 2 {
		t.Fatalf("expected 2 fires after second CursorDown; got %d", len(observed))
	}
	if r, ok := observed[1].(*roleEntry); !ok || r.RoleID != 11 {
		t.Fatalf("second fire should be w2; got %T %+v", observed[1], observed[1])
	}

	// CursorDown past the end is a no-op — must NOT fire again.
	rl.CursorDown()
	if len(observed) != 2 {
		t.Fatalf("no-op move must not fire; got %d", len(observed))
	}
}

func TestRailList_OnSelectionChangedFiresOnSelectBy(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1"},
			{OrchestratorID: 1, RoleID: 11, Name: "w2"},
		}},
	})
	var fires int
	rl.SetOnSelectionChanged(func(any) { fires++ })

	rl.SelectByRoleID(11)
	if fires != 1 {
		t.Fatalf("SelectByRoleID should fire selection-change once; got %d", fires)
	}
	// Selecting the same row again must NOT fire.
	rl.SelectByRoleID(11)
	if fires != 1 {
		t.Fatalf("re-selecting same row must not fire; got %d", fires)
	}
	rl.SelectByOrchID(1)
	if fires != 2 {
		t.Fatalf("SelectByOrchID to a different row should fire; got %d", fires)
	}
}

func TestRailList_DeadRoleHiddenByDefault(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w-alive"},
			{OrchestratorID: 1, RoleID: 11, Name: "w-dead", Dead: true},
		}},
	})

	got := renderRail(t, rl, 24, 6)
	if !strings.Contains(got, "w-alive") {
		t.Fatalf("alive worker must render; got:\n%s", got)
	}
	if strings.Contains(got, "w-dead") {
		t.Fatalf("dead worker must be hidden by default; got:\n%s", got)
	}

	// Visible role count on the orchestrator header should also exclude
	// the dead row.
	if !strings.Contains(got, "(1)") {
		t.Fatalf("orchestrator header count should be 1 (dead worker excluded); got:\n%s", got)
	}
}

func TestRailList_DeadRoleShownWhenArchivedVisible(t *testing.T) {
	rl := newRailList()
	rl.SetShowArchived(true)
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w-alive"},
			{OrchestratorID: 1, RoleID: 11, Name: "w-dead", Dead: true},
		}},
	})

	got := renderRail(t, rl, 24, 6)
	if !strings.Contains(got, "w-dead") {
		t.Fatalf("dead worker should appear when showArchived=true; got:\n%s", got)
	}
}

func TestRailList_RestoresCursorAcrossRebuild(t *testing.T) {
	rl := newRailList()
	orchs := []*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1"},
			{OrchestratorID: 1, RoleID: 11, Name: "w2"},
		}},
	}
	rl.SetOrchestrators(orchs)
	rl.SelectByRoleID(11)

	// Rebuild with the same data — cursor should stay on w2.
	rl.SetOrchestrators(orchs)
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 11 {
		t.Fatalf("cursor not restored to w2 after rebuild; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
	}
}

func TestRailList_FreelanceSectionRendersAndCollapses(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj1", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true}}},
	})
	rl.SetFreelance([]*freelanceProject{
		{Project: "Beta", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "free-1", ArgusTaskID: "f1", HasState: true, Status: "in_progress", ElapsedOverride: "5m"},
		}},
	})

	got := renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "Freelance") {
		t.Fatalf("expected Freelance separator; got:\n%s", got)
	}
	if !strings.Contains(got, "▾ Beta (1)") {
		t.Fatalf("expected expanded Beta repo header with count; got:\n%s", got)
	}
	if !strings.Contains(got, "free-1") {
		t.Fatalf("expected freelance task row; got:\n%s", got)
	}

	// Collapse the Beta repo group via its header.
	if !rl.SelectByProject("Beta") {
		t.Fatalf("could not select Beta freelance header")
	}
	rl.ToggleCollapse()
	got = renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "▸ Beta (1)") {
		t.Fatalf("expected collapsed Beta header; got:\n%s", got)
	}
	if strings.Contains(got, "free-1") {
		t.Fatalf("expected freelance task hidden when collapsed; got:\n%s", got)
	}
}

func TestRailList_NoFreelanceSectionWhenEmpty(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj1", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true}}},
	})
	rl.SetFreelance(nil)
	got := renderRail(t, rl, 30, 10)
	if strings.Contains(got, "Freelance") {
		t.Fatalf("Freelance separator must be omitted when there are no freelancers; got:\n%s", got)
	}
}

// Scenario: Sub-coordinators sort before leaf workers (folders-first).
// A coordinator with both sub-coordinator children (RoleKind=="coordinator")
// and leaf-worker children must render the sub-coords above the workers.
func TestRailList_FoldersFirstOrdering(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "leaf-a", RoleKind: "worker"},
			{OrchestratorID: 1, RoleID: 11, Name: "sub-coord", RoleKind: "coordinator"},
			{OrchestratorID: 1, RoleID: 12, Name: "leaf-b", RoleKind: "worker"},
		}},
	})
	got := renderRail(t, rl, 30, 8)
	subIdx := strings.Index(got, "sub-coord")
	leafAIdx := strings.Index(got, "leaf-a")
	leafBIdx := strings.Index(got, "leaf-b")
	if subIdx < 0 || leafAIdx < 0 || leafBIdx < 0 {
		t.Fatalf("expected all rows rendered; got:\n%s", got)
	}
	if subIdx > leafAIdx || subIdx > leafBIdx {
		t.Fatalf("sub-coordinator must sort before leaf workers; got:\n%s", got)
	}
}

// Scenario: Rows MUST NOT render kind pills.
func TestRailList_NoKindPills(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "p", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", RoleKind: "worker", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "c1", RoleKind: "coordinator", Live: true},
		}},
	})
	got := renderRail(t, rl, 36, 8)
	for _, pill := range []string{"worker", "coordinator", "[worker]", "[coord]", "WORKER", "COORD"} {
		if strings.Contains(got, pill) {
			t.Fatalf("rail must not render kind pills; found %q in:\n%s", pill, got)
		}
	}
}

// Scenario: Coordinator row is foldable with a count, and `space` (ToggleCollapse)
// toggles whether its children are shown.
func TestRailList_CoordinatorFoldableSpaceToggle(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w2", Live: true},
		}},
	})
	got := renderRail(t, rl, 28, 8)
	if !strings.Contains(got, "▾ proj (2)") {
		t.Fatalf("expected expanded chevron + (2) count; got:\n%s", got)
	}
	// space on the coordinator header collapses.
	rl.SelectByOrchID(1)
	rl.ToggleCollapse()
	got = renderRail(t, rl, 28, 8)
	if !strings.Contains(got, "▸ proj (2)") {
		t.Fatalf("expected collapsed chevron after space; got:\n%s", got)
	}
	if strings.Contains(got, "w1") || strings.Contains(got, "w2") {
		t.Fatalf("children must be hidden when collapsed; got:\n%s", got)
	}
}

// Scenario: Archived agents live in their coordinator's Archive expando,
// collapsed by default; folding the expando open reveals them.
func TestRailList_PerCoordinatorArchiveExpando(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w-active", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w-arch", Archived: true},
		}},
	})
	got := renderRail(t, rl, 30, 8)
	if !strings.Contains(got, "w-active") {
		t.Fatalf("active worker must render among the coordinator's rows; got:\n%s", got)
	}
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("coordinator with an archived child must render an Archive (1) expando; got:\n%s", got)
	}
	if strings.Contains(got, "w-arch") {
		t.Fatalf("archived agent must NOT appear among active rows / collapsed expando; got:\n%s", got)
	}

	// Fold the per-coordinator Archive expando open via its header row.
	if !rl.SelectByArchiveOwner(1) {
		t.Fatalf("could not select the per-coordinator Archive expando header")
	}
	rl.ToggleCollapse()
	got = renderRail(t, rl, 30, 8)
	if !strings.Contains(got, "w-arch") {
		t.Fatalf("archived agent must appear once its coordinator's Archive expando is open; got:\n%s", got)
	}
}

// Scenario: Archived root coordinators live under the top-level Archive
// section at the bottom of the rail (collapsed by default).
func TestRailList_TopLevelArchiveForArchivedRootCoordinators(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live-proj", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true}}},
		{ID: 2, Name: "dead-proj", Archived: true, Roles: nil},
	})
	got := renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("expected a top-level Archive (1) expando for the archived root coordinator; got:\n%s", got)
	}
	if strings.Contains(got, "dead-proj") {
		t.Fatalf("archived root coordinator must stay hidden inside the collapsed top-level Archive; got:\n%s", got)
	}
	// The live project's row must still render outside the Archive.
	if !strings.Contains(got, "live-proj") {
		t.Fatalf("live project must render outside the Archive; got:\n%s", got)
	}

	// Open the top-level Archive (owner 0) and the archived root appears.
	if !rl.SelectByArchiveOwner(0) {
		t.Fatalf("could not select the top-level Archive expando header")
	}
	rl.ToggleCollapse()
	got = renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "dead-proj") {
		t.Fatalf("archived root coordinator must appear once the top-level Archive is open; got:\n%s", got)
	}
}

// The top-level Archive sorts to the very bottom of the rail, below the
// Freelance section.
func TestRailList_TopLevelArchiveBelowFreelance(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live-proj", Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true}}},
		{ID: 2, Name: "dead-proj", Archived: true, Roles: nil},
	})
	rl.SetFreelance([]*freelanceProject{
		{Project: "repoX", Tasks: []*roleEntry{
			{RoleKind: "freelance", Name: "free-1", ArgusTaskID: "f1", HasState: true, Status: "in_progress"},
		}},
	})
	got := renderRail(t, rl, 32, 12)
	freeIdx := strings.Index(got, "Freelance")
	archIdx := strings.LastIndex(got, "Archive")
	if freeIdx < 0 || archIdx < 0 {
		t.Fatalf("expected both Freelance and top-level Archive; got:\n%s", got)
	}
	if archIdx < freeIdx {
		t.Fatalf("top-level Archive must render below the Freelance section; got:\n%s", got)
	}
}
