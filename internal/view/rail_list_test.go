package view

import (
	"strings"
	"testing"
	"time"

	"github.com/anutron/argus-sdk/theme"
	"github.com/gdamore/tcell/v2"
)

// runeColOf returns a finder that reports the RUNE column (not byte offset) at
// which needle first appears across the given lines, or -1 if absent. Rune
// columns are what indentation depth maps to on screen; byte offsets are
// distorted by the multi-byte box-border and icon glyphs.
func runeColOf(lines []string) func(needle string) int {
	return func(needle string) int {
		for _, ln := range lines {
			b := strings.Index(ln, needle)
			if b < 0 {
				continue
			}
			return len([]rune(ln[:b]))
		}
		return -1
	}
}

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

// renderRailSim draws the rail onto a SimulationScreen and returns both the
// text dump (for substring assertions) and the raw screen so per-cell styles
// can be decomposed. Mirrors renderRail's setup.
func renderRailSim(t *testing.T, rl *railList, w, h int) (string, tcell.SimulationScreen) {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(w, h)
	rl.SetRect(0, 0, w, h)
	rl.Draw(sim)
	sim.Show()
	return readScreen(sim), sim
}

// rowOf returns the screen row index (y) of the first line containing needle,
// or -1. Used to locate a rendered row before inspecting its cell styles.
func rowOf(dump, needle string) int {
	for i, ln := range strings.Split(dump, "\n") {
		if strings.Contains(ln, needle) {
			return i
		}
	}
	return -1
}

// rowHasBackground reports whether ANY cell on screen-row y carries the given
// (non-default) background color. Used to assert the rail never paints a row
// background to indicate selection.
func rowHasBackground(sim tcell.SimulationScreen, y int, bg tcell.Color) bool {
	cells, w, h := sim.GetContents()
	if y < 0 || y >= h {
		return false
	}
	for x := 0; x < w; x++ {
		_, cellBg, _ := cells[y*w+x].Style.Decompose()
		if cellBg == bg {
			return true
		}
	}
	return false
}

// rowHasForeground reports whether ANY cell on screen-row y carries the given
// foreground color — used to assert the selected row's name renders in
// theme.ColorSelected.
func rowHasForeground(sim tcell.SimulationScreen, y int, fg tcell.Color) bool {
	cells, w, h := sim.GetContents()
	if y < 0 || y >= h {
		return false
	}
	for x := 0; x < w; x++ {
		cellFg, _, _ := cells[y*w+x].Style.Decompose()
		if cellFg == fg {
			return true
		}
	}
	return false
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
	if !strings.Contains(got, "▾ "+string(iconCoord)+" proj1 (2)") {
		t.Fatalf("expected expanded chevron + coord marker + name + count; got:\n%s", got)
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
	if !strings.Contains(got, "▸ "+string(iconCoord)+" p (1)") {
		t.Fatalf("expected collapsed chevron + coord marker; got:\n%s", got)
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

// TestRailList_FreelanceCursorRestoresByTaskID proves a freelance selection
// survives a SetFreelance/rebuild on its own row, not the first freelancer.
// Freelance roleEntry values carry RoleID==0, so restoring by RoleID would
// match the first freelancer; the cursor must restore by the stable
// ArgusTaskID instead.
func TestRailList_FreelanceCursorRestoresByTaskID(t *testing.T) {
	rl := newRailList()
	mk := func() []*freelanceProject {
		return []*freelanceProject{
			{Project: "Beta", Tasks: []*roleEntry{
				{RoleKind: "freelance", Name: "free-1", ArgusTaskID: "f1", HasState: true, Status: "in_progress"},
				{RoleKind: "freelance", Name: "free-2", ArgusTaskID: "f2", HasState: true, Status: "in_progress"},
			}},
		}
	}
	rl.SetFreelance(mk())

	// Select the SECOND freelancer (ArgusTaskID f2).
	if !rl.SelectByArgusTaskID("f2") {
		t.Fatalf("could not select freelancer f2")
	}
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.ArgusTaskID != "f2" {
		t.Fatalf("cursor not on f2 before rebuild; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
	}

	// A dynamic refresh rebuilds the freelance rows (fresh roleEntry pointers).
	rl.SetFreelance(mk())

	// Cursor must stay on the freelancer with ArgusTaskID f2 — NOT jump to f1.
	if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.ArgusTaskID != "f2" {
		t.Fatalf("freelance cursor jumped on rebuild; want f2, got %T %+v", rl.CurrentRef(), rl.CurrentRef())
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
// A coordinator header's status icon mirrors argus's vocabulary from the coord
// task's argus state (set via CoordHasState + Coord*), the same way a worker
// row's icon does — needs-input → ?, complete → ✓ — and the 󰹻 coord marker is
// always drawn before the name.
func TestRailList_CoordHeaderStatusIconAndMarker(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{
			ID: 1, Name: "blocked", CoordTaskID: "t-1",
			CoordHasState: true, CoordStatus: "in_progress", CoordNeedsInput: true,
			Roles: []*roleEntry{{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true}},
		},
		{
			ID: 2, Name: "shipped", CoordTaskID: "t-2",
			CoordHasState: true, CoordStatus: "complete",
		},
	})

	got := renderRail(t, rl, 30, 6)
	// needs-input coordinator: ? icon, then chevron, then marker, then name.
	if !strings.Contains(got, string(theme.IconNeedsInput)+" ▾ "+string(iconCoord)+" blocked") {
		t.Fatalf("expected needs-input icon + chevron + marker on coord header; got:\n%s", got)
	}
	// complete coordinator: ✓ icon before its (worker-less) header. Zero
	// non-archived children → the row defaults collapsed (▸).
	if !strings.Contains(got, "✓ ▸ "+string(iconCoord)+" shipped") {
		t.Fatalf("expected ✓ status icon on completed (default-collapsed) coord header; got:\n%s", got)
	}
}

func TestRailList_CoordinatorFoldableSpaceToggle(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w2", Live: true},
		}},
	})
	got := renderRail(t, rl, 28, 8)
	if !strings.Contains(got, "▾ "+string(iconCoord)+" proj (2)") {
		t.Fatalf("expected expanded chevron + coord marker + (2) count; got:\n%s", got)
	}
	// space on the coordinator header collapses.
	rl.SelectByOrchID(1)
	rl.ToggleCollapse()
	got = renderRail(t, rl, 28, 8)
	if !strings.Contains(got, "▸ "+string(iconCoord)+" proj (2)") {
		t.Fatalf("expected collapsed chevron + coord marker after space; got:\n%s", got)
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

// TestRailList_StepToBindable exercises the in-pane-nav stepping primitive
// directly: it must skip non-bindable rows (Freelance separator, freelance
// repo header, Archive expando), clamp at both ends without wrapping, and
// return false on an empty rail without looping.
func TestRailList_StepToBindable(t *testing.T) {
	// Hand-build the row layout so the non-bindable rows sit between two
	// pane-bindable role rows: orch → w1 → Freelance sep → freelance proj
	// header → archive expando → w2.
	orch := &orchEntry{ID: 1, Name: "p", CoordTaskID: "coord-1"}
	w1 := &roleEntry{OrchestratorID: 1, RoleID: 10, Name: "w1", ArgusTaskID: "t-w1"}
	w2 := &roleEntry{OrchestratorID: 1, RoleID: 11, Name: "w2", ArgusTaskID: "t-w2"}
	fproj := &freelanceProject{Project: "Beta"}

	newRail := func() *railList {
		rl := newRailList()
		rl.rows = []railRow{
			{kind: railRowOrch, orch: orch},                                 // 0 bindable
			{kind: railRowRole, role: w1},                                   // 1 bindable
			{kind: railRowFreelanceSep},                                     // 2 non-bindable
			{kind: railRowFreelanceProj, fproj: fproj},                      // 3 non-bindable (selectable, not bindable)
			{kind: railRowArchiveExpando, archiveOwner: 1, archiveCount: 1}, // 4 non-bindable
			{kind: railRowRole, role: w2},                                   // 5 bindable
		}
		return rl
	}

	t.Run("forward from w1 skips three non-bindable rows to w2", func(t *testing.T) {
		rl := newRail()
		rl.cursor = 1 // on w1
		if !rl.StepToBindable(+1) {
			t.Fatalf("StepToBindable(+1) should move from w1 to w2")
		}
		if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 11 {
			t.Fatalf("expected cursor on w2; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
		}
	})

	t.Run("backward from w2 skips three non-bindable rows to w1", func(t *testing.T) {
		rl := newRail()
		rl.cursor = 5 // on w2
		if !rl.StepToBindable(-1) {
			t.Fatalf("StepToBindable(-1) should move from w2 to w1")
		}
		if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 10 {
			t.Fatalf("expected cursor on w1; got %T %+v", rl.CurrentRef(), rl.CurrentRef())
		}
	})

	t.Run("clamps at bottom without wrapping", func(t *testing.T) {
		rl := newRail()
		rl.cursor = 5 // on w2 (last bindable row)
		if rl.StepToBindable(+1) {
			t.Fatalf("StepToBindable(+1) past the last bindable row must return false")
		}
		if ref, ok := rl.CurrentRef().(*roleEntry); !ok || ref.RoleID != 11 {
			t.Fatalf("cursor must stay on w2 (no wrap); got %T %+v", rl.CurrentRef(), rl.CurrentRef())
		}
	})

	t.Run("clamps at top without wrapping", func(t *testing.T) {
		rl := newRail()
		rl.cursor = 0 // on orch header (first bindable row)
		if rl.StepToBindable(-1) {
			t.Fatalf("StepToBindable(-1) past the first bindable row must return false")
		}
		if ref, ok := rl.CurrentRef().(*orchEntry); !ok || ref.ID != 1 {
			t.Fatalf("cursor must stay on the orch header (no wrap); got %T %+v", rl.CurrentRef(), rl.CurrentRef())
		}
	})

	t.Run("empty rail returns false without looping", func(t *testing.T) {
		rl := newRailList() // buildRows not called → rows is empty
		if rl.StepToBindable(+1) {
			t.Fatalf("StepToBindable on an empty rail must return false")
		}
		if rl.StepToBindable(-1) {
			t.Fatalf("StepToBindable(-1) on an empty rail must return false")
		}
	})

	t.Run("rail with no bindable rows returns false", func(t *testing.T) {
		rl := newRailList()
		rl.rows = []railRow{
			{kind: railRowFreelanceSep},
			{kind: railRowFreelanceProj, fproj: fproj},
			{kind: railRowArchiveExpando, archiveOwner: 0, archiveCount: 1},
		}
		rl.cursor = 1
		if rl.StepToBindable(+1) {
			t.Fatalf("StepToBindable(+1) with no bindable rows must return false")
		}
		if rl.StepToBindable(-1) {
			t.Fatalf("StepToBindable(-1) with no bindable rows must return false")
		}
	})
}

// Scenario: A sub-coordinator (a worker roleEntry that is itself another
// orchestrator's coord) renders as a NESTED foldable coordinator row — chevron
// + 󰹻 marker + (N) count — with its own children indented one level deeper, and
// the child orchestrator does NOT also render at top level.
func TestRailList_SubCoordinatorNestsAsFoldableCoordRow(t *testing.T) {
	rl := newRailList()
	// Build the multi-binding by hand: the parent's "sub" worker carries a
	// childOrch pointer to a second orchestrator whose own leaf is nested.
	// childOrch is wired by buildRows from the role's childOrch pointer; the
	// child orchestrator nests under the sub-coord row and is NOT passed as a
	// top-level orchestrator (populateRail's resolveSubCoordinators handles the
	// flat→tree promotion + de-dup; here we hand-wire the resolved tree).
	child := &orchEntry{ID: 2, Name: "sub", CoordTaskID: "t-sub", Roles: []*roleEntry{
		{OrchestratorID: 2, RoleID: 20, Name: "leaf-worker", RoleKind: "worker", Live: true},
	}}
	sub := &roleEntry{OrchestratorID: 1, RoleID: 11, Name: "sub", RoleKind: "coordinator", ArgusTaskID: "t-sub", childOrch: child}
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "parent", CoordTaskID: "t-parent", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "plain-worker", RoleKind: "worker", Live: true},
			sub,
		}},
	})

	got := renderRail(t, rl, 36, 12)
	// The sub-coordinator must render as a coord row (chevron + 󰹻 marker).
	if !strings.Contains(got, "▾ "+string(iconCoord)+" sub") {
		t.Fatalf("sub-coordinator must render as a foldable coord row (chevron + marker); got:\n%s", got)
	}
	// Its child leaf must render, nested.
	if !strings.Contains(got, "leaf-worker") {
		t.Fatalf("sub-coordinator's nested child must render; got:\n%s", got)
	}
	// "sub" must appear exactly once (not as a leaf AND a top-level orch).
	if n := strings.Count(got, "▾ "+string(iconCoord)+" sub"); n != 1 {
		t.Fatalf("sub-coordinator coord row must render exactly once; got %d in:\n%s", n, got)
	}

	// Depth check: the nested leaf must be indented MORE than its sub-coord row,
	// which in turn is indented more than the parent coord. Measure from the
	// CHEVRON column on coordinator rows (icon + chevron = 4 chars from base,
	// consistent across depths) so the comparison is not confused by the
	// variable number of prefix glyphs before the name. Leaf rows are plain
	// workers; their name starts 2 cols after their base (icon col + 2).
	// After BUG-008 fix, all depth-N rows start their icon at the same base
	// column (cx + N*indentStep), so chevron positions increase by indentStep
	// per level and leaf names land strictly deeper than the sub-coord chevron.
	lines := strings.Split(got, "\n")
	indent := runeColOf(lines)
	parentChevron := indent("▾ " + string(iconCoord) + " parent")
	subChevron := indent("▾ " + string(iconCoord) + " sub")
	leafName := indent("leaf-worker")
	if parentChevron >= subChevron || subChevron >= leafName {
		t.Fatalf("expected increasing indentation parent-chevron(%d) < sub-chevron(%d) < leaf-name(%d); got:\n%s",
			parentChevron, subChevron, leafName, got)
	}

	// BUG-008: sibling workers at the same depth as the sub-coordinator MUST
	// NOT indent deeper than the sub-coordinator's own children. Verify that
	// the plain-worker sibling (depth 1) appears SHALLOWER than the leaf (depth 2).
	plainWorkerName := indent("plain-worker")
	if plainWorkerName >= leafName {
		t.Fatalf("BUG-008: sibling plain-worker(%d) must be shallower than leaf-worker(%d); got:\n%s",
			plainWorkerName, leafName, got)
	}
}

// Scenario: Archived children indent one level DEEPER than their Archive (N)
// expando header (depth-aware indentation). The Archive expando sits one level
// under its coordinator; its archived children sit one level under the expando.
func TestRailList_ArchivedChildIndentsDeeperThanExpando(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w-active", RoleKind: "worker", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w-archived", RoleKind: "worker", Archived: true},
		}},
	})
	// Open the per-coordinator Archive expando so its child renders.
	if !rl.SelectByArchiveOwner(1) {
		t.Fatalf("could not select per-coordinator Archive expando")
	}
	rl.ToggleCollapse()

	got := renderRail(t, rl, 36, 10)
	lines := strings.Split(got, "\n")
	indent := runeColOf(lines)
	expandoIndent := indent("Archive (1)")
	archivedIndent := indent("w-archived")
	if expandoIndent < 0 || archivedIndent < 0 {
		t.Fatalf("expected both the Archive expando and the archived child to render; got:\n%s", got)
	}
	if archivedIndent <= expandoIndent {
		t.Fatalf("archived child (%d) must indent deeper than its Archive expando header (%d); got:\n%s", archivedIndent, expandoIndent, got)
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

// Scenario: the selected row is indicated by selected-text styling, NOT a grey
// background fill — for BOTH a coordinator header and a worker row. The rail
// must paint no theme.ColorHighlight background on any row.
func TestRailList_SelectionUsesSelectedTextNotBackground(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj1", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w2", Live: true},
		}},
	})

	// Cursor on the coordinator header.
	rl.SelectByOrchID(1)
	dump, sim := renderRailSim(t, rl, 28, 8)
	headerRow := rowOf(dump, "proj1")
	if headerRow < 0 {
		t.Fatalf("coordinator header not rendered; got:\n%s", dump)
	}
	if rowHasBackground(sim, headerRow, theme.ColorHighlight) {
		t.Fatalf("selected coordinator header must NOT paint a ColorHighlight background; got:\n%s", dump)
	}
	if !rowHasForeground(sim, headerRow, theme.ColorSelected) {
		t.Fatalf("selected coordinator header name must render in theme.ColorSelected; got:\n%s", dump)
	}

	// Cursor on a worker row.
	rl.SelectByRoleID(10)
	dump, sim = renderRailSim(t, rl, 28, 8)
	workerRow := rowOf(dump, "w1")
	if workerRow < 0 {
		t.Fatalf("worker row not rendered; got:\n%s", dump)
	}
	if rowHasBackground(sim, workerRow, theme.ColorHighlight) {
		t.Fatalf("selected worker row must NOT paint a ColorHighlight background; got:\n%s", dump)
	}
	if !rowHasForeground(sim, workerRow, theme.ColorSelected) {
		t.Fatalf("selected worker name must render in theme.ColorSelected; got:\n%s", dump)
	}
}

// Scenario: a NON-selected row carries no lingering highlight background. With
// the cursor on the header, the (unselected) worker rows must be default-bg.
func TestRailList_NonSelectedRowHasNoBackground(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj1", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w2", Live: true},
		}},
	})
	rl.SelectByOrchID(1) // cursor on header; workers are NOT selected

	dump, sim := renderRailSim(t, rl, 28, 8)
	for _, name := range []string{"w1", "w2"} {
		y := rowOf(dump, name)
		if y < 0 {
			t.Fatalf("row %q not rendered; got:\n%s", name, dump)
		}
		if rowHasBackground(sim, y, theme.ColorHighlight) {
			t.Fatalf("non-selected row %q must have default background (no ColorHighlight); got:\n%s", name, dump)
		}
	}
}

// markerLines returns the indexes of dump lines containing the selection
// marker glyph. readScreen strips all styling, so these assertions prove the
// selection is identifiable from a colorless text capture alone (the
// live-probe grid renderer, screen readers, reduced-color terminals).
func markerLines(dump string) []int {
	var out []int
	for i, ln := range strings.Split(dump, "\n") {
		if strings.ContainsRune(ln, selectionMarker) {
			out = append(out, i)
		}
	}
	return out
}

// assertMarkerOnRow asserts the marker glyph renders exactly once across the
// dump, on the line containing needle.
func assertMarkerOnRow(t *testing.T, dump, needle string) {
	t.Helper()
	rows := markerLines(dump)
	if len(rows) != 1 {
		t.Fatalf("selection marker must render exactly once; found on lines %v in:\n%s", rows, dump)
	}
	want := rowOf(dump, needle)
	if want < 0 {
		t.Fatalf("row %q not rendered; got:\n%s", needle, dump)
	}
	if rows[0] != want {
		t.Fatalf("selection marker on line %d, want line %d (%q); got:\n%s", rows[0], want, needle, dump)
	}
}

// Scenario: Selected row is identifiable without color via the marker glyph —
// for EVERY selectable row kind (coordinator header, worker row, archive
// expando, freelance repo header, freelance task row).
func TestRailList_SelectionMarkerOnEverySelectableRowKind(t *testing.T) {
	mk := func() *railList {
		rl := newRailList()
		rl.probeMarker = true // marker is probe-gated; this suite asserts the probe-visible glyph
		rl.SetOrchestrators([]*orchEntry{
			{ID: 1, Name: "proj1", CoordTaskID: "c-1", Roles: []*roleEntry{
				{OrchestratorID: 1, RoleID: 10, Name: "w-active", RoleKind: "worker", Live: true},
				{OrchestratorID: 1, RoleID: 11, Name: "w-old", RoleKind: "worker", Archived: true},
			}},
		})
		rl.SetFreelance([]*freelanceProject{
			{Project: "repoX", Tasks: []*roleEntry{
				{RoleKind: "freelance", Name: "free-1", ArgusTaskID: "f1", HasState: true, Status: "in_progress"},
			}},
		})
		return rl
	}

	t.Run("coordinator header", func(t *testing.T) {
		rl := mk()
		if !rl.SelectByOrchID(1) {
			t.Fatal("SelectByOrchID failed")
		}
		assertMarkerOnRow(t, renderRail(t, rl, 32, 12), "proj1")
	})
	t.Run("worker row", func(t *testing.T) {
		rl := mk()
		if !rl.SelectByRoleID(10) {
			t.Fatal("SelectByRoleID failed")
		}
		assertMarkerOnRow(t, renderRail(t, rl, 32, 12), "w-active")
	})
	t.Run("archive expando", func(t *testing.T) {
		rl := mk()
		if !rl.SelectByArchiveOwner(1) {
			t.Fatal("SelectByArchiveOwner failed")
		}
		assertMarkerOnRow(t, renderRail(t, rl, 32, 12), "Archive (1)")
	})
	t.Run("freelance repo header", func(t *testing.T) {
		rl := mk()
		if !rl.SelectByProject("repoX") {
			t.Fatal("SelectByProject failed")
		}
		assertMarkerOnRow(t, renderRail(t, rl, 32, 12), "repoX")
	})
	t.Run("freelance task row", func(t *testing.T) {
		rl := mk()
		if !rl.SelectByArgusTaskID("f1") {
			t.Fatal("SelectByArgusTaskID failed")
		}
		assertMarkerOnRow(t, renderRail(t, rl, 32, 12), "free-1")
	})
}

// Scenario: Marker gutter keeps columns stable across selection moves — the
// marker follows the cursor and vacated rows render a space in the gutter, so
// no row content shifts when the selection moves.
func TestRailList_SelectionMarkerMovesWithCursorWithoutShift(t *testing.T) {
	rl := newRailList()
	rl.probeMarker = true // marker is probe-gated; assert it under the probe
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj1", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", RoleKind: "worker", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "w2", RoleKind: "worker", Live: true},
		}},
	})
	rl.SelectByOrchID(1)
	before := renderRail(t, rl, 28, 8)
	assertMarkerOnRow(t, before, "proj1")
	colBefore := runeColOf(strings.Split(before, "\n"))

	rl.CursorDown() // header → w1
	after := renderRail(t, rl, 28, 8)
	assertMarkerOnRow(t, after, "w1")
	colAfter := runeColOf(strings.Split(after, "\n"))

	for _, needle := range []string{"proj1", "w1", "w2"} {
		b, a := colBefore(needle), colAfter(needle)
		if b < 0 || a < 0 {
			t.Fatalf("%q missing from a render; before:\n%s\nafter:\n%s", needle, before, after)
		}
		if b != a {
			t.Fatalf("%q shifted from col %d to %d on selection move; before:\n%s\nafter:\n%s", needle, b, a, before, after)
		}
	}
}

// Scenario: Empty coordinator defaults collapsed. A coordinator with zero
// non-archived children — none at all, or only archived/dead ones — renders
// header-only with the ▸ chevron; its children (including the Archive (N)
// expando) stay hidden until the operator expands it. A coordinator with at
// least one active child keeps the expanded (▾) default.
func TestRailList_EmptyCoordinatorDefaultsCollapsed(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "bare", Roles: nil},
		{ID: 2, Name: "spent", Roles: []*roleEntry{
			{OrchestratorID: 2, RoleID: 20, Name: "w-arch", Archived: true},
			{OrchestratorID: 2, RoleID: 21, Name: "w-dead", Dead: true},
		}},
		{ID: 3, Name: "busy", Roles: []*roleEntry{
			{OrchestratorID: 3, RoleID: 30, Name: "w-live", Live: true},
		}},
	})

	got := renderRail(t, rl, 30, 10)
	// No children at all → collapsed header only.
	if !strings.Contains(got, "▸ "+string(iconCoord)+" bare (0)") {
		t.Fatalf("coordinator with no children must default collapsed; got:\n%s", got)
	}
	// Only archived/dead children → still zero non-archived → collapsed, and
	// its Archive expando must NOT render while the coordinator is folded.
	if !strings.Contains(got, "▸ "+string(iconCoord)+" spent (0)") {
		t.Fatalf("coordinator with only archived children must default collapsed; got:\n%s", got)
	}
	if strings.Contains(got, "Archive (2)") {
		t.Fatalf("a default-collapsed coordinator must hide its Archive expando; got:\n%s", got)
	}
	// ≥1 active child → expanded default unchanged.
	if !strings.Contains(got, "▾ "+string(iconCoord)+" busy (1)") {
		t.Fatalf("coordinator with an active child must default expanded; got:\n%s", got)
	}
	if !strings.Contains(got, "w-live") {
		t.Fatalf("active child of an expanded coordinator must render; got:\n%s", got)
	}
}

// Scenario: Manual toggle overrides the default and persists. Expanding a
// default-collapsed empty coordinator reveals its Archive expando and the
// choice survives rebuilds; collapsing a busy coordinator also persists.
func TestRailList_ManualToggleOverridesCollapsedDefault(t *testing.T) {
	mk := func() []*orchEntry {
		return []*orchEntry{
			{ID: 1, Name: "spent", Roles: []*roleEntry{
				{OrchestratorID: 1, RoleID: 10, Name: "w-arch", Archived: true},
			}},
			{ID: 2, Name: "busy", Roles: []*roleEntry{
				{OrchestratorID: 2, RoleID: 20, Name: "w-live", Live: true},
			}},
		}
	}
	rl := newRailList()
	rl.SetOrchestrators(mk())

	// space on the default-collapsed empty coordinator expands it: the
	// Archive (1) expando appears (collapsed itself, per D14).
	if !rl.SelectByOrchID(1) {
		t.Fatal("could not select empty coordinator")
	}
	rl.ToggleCollapse()
	got := renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "▾ "+string(iconCoord)+" spent (0)") {
		t.Fatalf("toggling a default-collapsed coordinator must expand it; got:\n%s", got)
	}
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("expanding an empty coordinator must reveal its Archive expando; got:\n%s", got)
	}

	// The explicit expand persists across a data rebuild (fresh pointers).
	rl.SetOrchestrators(mk())
	got = renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "▾ "+string(iconCoord)+" spent (0)") {
		t.Fatalf("explicit expand must persist across rebuilds; got:\n%s", got)
	}

	// Toggling again re-collapses (explicit collapsed, not back to default).
	if !rl.SelectByOrchID(1) {
		t.Fatal("could not re-select empty coordinator")
	}
	rl.ToggleCollapse()
	got = renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "▸ "+string(iconCoord)+" spent (0)") {
		t.Fatalf("second toggle must re-collapse the coordinator; got:\n%s", got)
	}

	// Manually collapsing the busy coordinator persists across rebuilds too.
	if !rl.SelectByOrchID(2) {
		t.Fatal("could not select busy coordinator")
	}
	rl.ToggleCollapse()
	rl.SetOrchestrators(mk())
	got = renderRail(t, rl, 30, 10)
	if !strings.Contains(got, "▸ "+string(iconCoord)+" busy (1)") {
		t.Fatalf("manual collapse of a busy coordinator must persist; got:\n%s", got)
	}
	if strings.Contains(got, "w-live") {
		t.Fatalf("children of a manually-collapsed coordinator must stay hidden; got:\n%s", got)
	}
}

// Scenario: `l` force-reveal overrides the collapsed default while active —
// an untouched empty coordinator expands (its archived children visible via
// the force-expanded Archive expando), but an explicit manual collapse still
// wins over `l`.
func TestRailList_ShowArchivedOverridesCollapsedDefault(t *testing.T) {
	mk := func() []*orchEntry {
		return []*orchEntry{
			{ID: 1, Name: "spent", Roles: []*roleEntry{
				{OrchestratorID: 1, RoleID: 10, Name: "w-arch", Archived: true},
			}},
			{ID: 2, Name: "pinned", Roles: []*roleEntry{
				{OrchestratorID: 2, RoleID: 20, Name: "w-hidden", Archived: true},
			}},
		}
	}
	rl := newRailList()
	rl.SetOrchestrators(mk())
	// Explicitly collapse "pinned" (it is already default-collapsed; the
	// operator's toggle-expand + toggle-collapse pins the explicit state).
	if !rl.SelectByOrchID(2) {
		t.Fatal("could not select pinned coordinator")
	}
	rl.ToggleCollapse() // explicit expand
	rl.ToggleCollapse() // explicit collapse — pinned
	rl.SetShowArchived(true)

	got := renderRail(t, rl, 32, 12)
	// Untouched empty coordinator: default overridden → expanded, archived
	// child reachable through the force-expanded expando.
	if !strings.Contains(got, "▾ "+string(iconCoord)+" spent (0)") {
		t.Fatalf("`l` must override the collapsed default on an untouched empty coordinator; got:\n%s", got)
	}
	if !strings.Contains(got, "w-arch") {
		t.Fatalf("`l` must reveal the archived child of an untouched empty coordinator; got:\n%s", got)
	}
	// Explicitly collapsed coordinator stays collapsed even under `l`.
	if !strings.Contains(got, "▸ "+string(iconCoord)+" pinned (0)") {
		t.Fatalf("explicit collapse must win over `l`; got:\n%s", got)
	}
	if strings.Contains(got, "w-hidden") {
		t.Fatalf("children of an explicitly collapsed coordinator must stay hidden under `l`; got:\n%s", got)
	}
}

// Scenario: the collapsed-empty default applies to nested sub-coordinators
// too — a sub-coordinator whose child orchestrator has zero non-archived
// children renders with the ▸ chevron and no nested rows.
func TestRailList_EmptySubCoordinatorDefaultsCollapsed(t *testing.T) {
	rl := newRailList()
	child := &orchEntry{ID: 2, Name: "sub", CoordTaskID: "t-sub", Roles: []*roleEntry{
		{OrchestratorID: 2, RoleID: 20, Name: "leaf-arch", RoleKind: "worker", Archived: true},
	}}
	sub := &roleEntry{OrchestratorID: 1, RoleID: 11, Name: "sub", RoleKind: "coordinator", ArgusTaskID: "t-sub", childOrch: child}
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "parent", CoordTaskID: "t-parent", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "plain-worker", RoleKind: "worker", Live: true},
			sub,
		}},
	})

	got := renderRail(t, rl, 36, 12)
	if !strings.Contains(got, "▸ "+string(iconCoord)+" sub (0)") {
		t.Fatalf("empty sub-coordinator must default collapsed; got:\n%s", got)
	}
	if strings.Contains(got, "leaf-arch") || strings.Contains(got, "Archive (1)") {
		t.Fatalf("collapsed empty sub-coordinator must hide its children and Archive expando; got:\n%s", got)
	}

	// Toggling the sub-coordinator row expands it and reveals its expando.
	if !rl.SelectByRoleID(11) {
		t.Fatal("could not select sub-coordinator row")
	}
	rl.ToggleCollapse()
	got = renderRail(t, rl, 36, 12)
	if !strings.Contains(got, "▾ "+string(iconCoord)+" sub (0)") {
		t.Fatalf("toggled empty sub-coordinator must expand; got:\n%s", got)
	}
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("expanded empty sub-coordinator must reveal its Archive expando; got:\n%s", got)
	}
}

// Spec (rail-truthfulness): Rail status icons reflect the bound task's actual
// argus state — archive state and binding liveness modulate only STYLE
// (dimmed), never the GLYPH. An archived/dead row with known state renders its
// true status glyph dimmed.
func TestStatusIcon_ArchivedRowsKeepTrueStatusGlyph(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		needsInput bool
		idle       bool
		want       rune
	}{
		{"complete", "complete", false, false, '✓'},
		{"in_review", "in_review", false, false, theme.IconReview},
		{"working", "in_progress", false, false, spinnerFrames[0]},
		{"idle", "in_progress", false, true, theme.IconMoonOutline},
		{"pending", "pending", false, false, '○'},
		{"needs-input", "in_progress", true, false, theme.IconNeedsInput},
	}
	for _, tc := range cases {
		glyph, style := statusIcon(true, true, tc.needsInput, tc.status, tc.idle, false, 0)
		if glyph != tc.want {
			t.Errorf("%s: archived row with known state: glyph = %q, want %q (true status, dimmed)", tc.name, glyph, tc.want)
		}
		if style != theme.StyleDimmed {
			t.Errorf("%s: archived row with known state must render dimmed; got %v", tc.name, style)
		}
	}
}

// Spec (rail-truthfulness): unknown-state archived/dead rows keep the dimmed
// circle fallback — '○' is the fallback ONLY when argus state is unknown.
func TestStatusIcon_UnknownStateArchivedFallsBackToCircle(t *testing.T) {
	glyph, style := statusIcon(true, false, false, "", false, false, 0)
	if glyph != '○' {
		t.Fatalf("archived row with UNKNOWN state: glyph = %q, want '○' fallback", glyph)
	}
	if style != theme.StyleDimmed {
		t.Fatalf("archived unknown-state fallback must be dimmed; got %v", style)
	}
}

// Spec (rail-truthfulness): the R1/R2 QA shape — a complete agent archived
// then unarchived must render '✓' at every step of the round trip; the glyph
// never mutates while the argus status is unchanged.
func TestRoleIcon_ArchiveRoundTripNeverMutatesGlyph(t *testing.T) {
	rl := newRailList()
	r := &roleEntry{Name: "archive-this-agent", HasState: true, Status: "complete"}

	active, _ := rl.roleIcon(r)
	if active != '✓' {
		t.Fatalf("active complete row: glyph = %q, want '✓'", active)
	}

	// Archive (both sides, the `a` verb): glyph must hold, style dims.
	r.Archived, r.ArgusArchived = true, true
	archived, archivedStyle := rl.roleIcon(r)
	if archived != active {
		t.Fatalf("archiving mutated the status glyph: %q -> %q (argus status unchanged)", active, archived)
	}
	if archivedStyle != theme.StyleDimmed {
		t.Fatalf("archived row must render dimmed; got %v", archivedStyle)
	}

	// Unarchive: back to the active rendering.
	r.Archived, r.ArgusArchived = false, false
	back, _ := rl.roleIcon(r)
	if back != active {
		t.Fatalf("unarchiving mutated the status glyph: %q -> %q", active, back)
	}
}

// Spec (rail-truthfulness): even if a row is classified Dead (record gone)
// while it still carries a known complete status, the glyph never lies — it
// renders '✓' dimmed, not '○'.
func TestRoleIcon_DeadCompleteShowsCheckDimmed(t *testing.T) {
	rl := newRailList()
	r := &roleEntry{Name: "done-agent", Dead: true, Live: true, HasState: true, Status: "complete"}
	glyph, style := rl.roleIcon(r)
	if glyph != '✓' {
		t.Fatalf("dead-classified complete row: glyph = %q, want '✓'", glyph)
	}
	if style != theme.StyleDimmed {
		t.Fatalf("dead-classified row must render dimmed; got %v", style)
	}
}

// Spec (rail-truthfulness): coordinator headers obey the same rule — an
// archived orchestrator whose coord task state is known renders the true
// status glyph dimmed, not '○'.
func TestOrchIcon_ArchivedHeaderKeepsTrueStatusGlyph(t *testing.T) {
	rl := newRailList()
	o := &orchEntry{
		ID: 1, Name: "shipped", Archived: true, CoordTaskID: "t-1",
		CoordHasState: true, CoordStatus: "complete",
	}
	glyph, style := rl.orchIcon(o)
	if glyph != '✓' {
		t.Fatalf("archived coord header with known complete state: glyph = %q, want '✓'", glyph)
	}
	if style != theme.StyleDimmed {
		t.Fatalf("archived coord header must render dimmed; got %v", style)
	}
}

// Spec (icon-alignment): the glyph + color for every KNOWN argus state mirrors
// argus's task-panel table exactly (argus theme.go:29-34 + tasklist.go:1095-1132).
func TestStateGlyph_MirrorsArgusTable(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		needsInput bool
		idle       bool
		wantGlyph  rune
		wantStyle  tcell.Style
	}{
		{"pending", "pending", false, false, '○', theme.StylePending},
		{"complete", "complete", false, false, '✓', theme.StyleComplete},
		{"in_review", "in_review", false, false, theme.IconReview, theme.StyleInReview},
		{"needs-input", "in_progress", true, false, theme.IconNeedsInput, theme.StyleNeedsInput},
		{"needs-input outranks idle", "in_progress", true, true, theme.IconNeedsInput, theme.StyleNeedsInput},
		{"idle moon is blue", "in_progress", false, true, theme.IconMoonOutline, theme.StyleInReview},
		{"running spinner is orange", "in_progress", false, false, spinnerFrames[0], theme.StyleInProgress},
	}
	for _, tc := range cases {
		glyph, style, ok := stateGlyph(tc.needsInput, tc.status, tc.idle, 0)
		if !ok {
			t.Errorf("%s: stateGlyph not ok for known state", tc.name)
			continue
		}
		if glyph != tc.wantGlyph {
			t.Errorf("%s: glyph = %q (U+%04X), want %q (U+%04X)", tc.name, glyph, glyph, tc.wantGlyph, tc.wantGlyph)
		}
		if style != tc.wantStyle {
			t.Errorf("%s: style = %v, want %v", tc.name, style, tc.wantStyle)
		}
	}
}

// Spec (icon-alignment): in_review MUST be visually distinct from complete by
// GLYPH — the checkmark renders for complete and ONLY complete. Argus never
// renders a blue check (the operator QA R3 "blue check" confusion).
func TestStateGlyph_NoCheckmarkForNonCompleteStates(t *testing.T) {
	type combo struct {
		status string
		idle   bool
		ni     bool
	}
	nonComplete := []combo{
		{"pending", false, false},
		{"in_review", false, false},
		{"in_progress", false, false},
		{"in_progress", true, false},
		{"in_progress", false, true},
		{"in_progress", true, true},
	}
	for _, c := range nonComplete {
		for frame := 0; frame < len(spinnerFrames); frame++ {
			glyph, _, ok := stateGlyph(c.ni, c.status, c.idle, frame)
			if ok && glyph == '✓' {
				t.Errorf("non-complete state %q (idle=%v needsInput=%v frame=%d) rendered the checkmark", c.status, c.idle, c.ni, frame)
			}
		}
	}
}

// Spec (icon-alignment): needs-input applies within in_progress only,
// mirroring argus's switch nesting — a needs_input flag arriving with any
// other status (a shape argus's API never serves) defers to the status glyph.
func TestStateGlyph_NeedsInputScopedToInProgress(t *testing.T) {
	glyph, style, ok := stateGlyph(true, "complete", false, 0)
	if !ok || glyph != '✓' || style != theme.StyleComplete {
		t.Fatalf("complete+needsInput: glyph=%q style=%v ok=%v, want '✓' StyleComplete (status wins)", glyph, style, ok)
	}
	glyph, _, ok = stateGlyph(true, "in_review", false, 0)
	if !ok || glyph != theme.IconReview {
		t.Fatalf("in_review+needsInput: glyph=%q ok=%v, want IconReview/clipboard-check (status wins)", glyph, ok)
	}
}

// Spec (icon-alignment): the running spinner cycles argus's Nerd Font
// progress frames U+EE06..U+EE0B, advancing one frame per tick and wrapping.
func TestStateGlyph_SpinnerCyclesArgusProgressFrames(t *testing.T) {
	want := []rune{0xEE06, 0xEE07, 0xEE08, 0xEE09, 0xEE0A, 0xEE0B}
	for i := 0; i < 2*len(want); i++ {
		glyph, _, ok := stateGlyph(false, "in_progress", false, i)
		if !ok {
			t.Fatalf("frame %d: stateGlyph not ok", i)
		}
		if glyph != want[i%len(want)] {
			t.Errorf("frame %d: glyph = U+%04X, want U+%04X", i, glyph, want[i%len(want)])
		}
	}
}

// Spec (icon-alignment): running rows animate by wall clock at argus's 150 ms
// cadence — the frame derives from the rail's (test-overridable) now source.
func TestRailList_AnimFrameAdvancesByWallClock(t *testing.T) {
	rl := newRailList()
	// Frame-aligned base (1,500,000 ms is an exact multiple of the 150 ms
	// cadence) so the +149ms probe stays inside the same frame.
	base := time.UnixMilli(1_500_000)
	rl.now = func() time.Time { return base }
	f0 := rl.animFrame()
	rl.now = func() time.Time { return base.Add(149 * time.Millisecond) }
	if got := rl.animFrame(); got != f0 {
		t.Fatalf("149ms later: frame = %d, want unchanged %d", got, f0)
	}
	rl.now = func() time.Time { return base.Add(150 * time.Millisecond) }
	if got := rl.animFrame(); got != f0+1 {
		t.Fatalf("150ms later: frame = %d, want %d", got, f0+1)
	}
}

// Spec (icon-alignment): a running role row renders the wall-clock spinner
// frame as its status icon, and the frame advances across a tick boundary.
func TestDrawRoleRow_RunningRowRendersAdvancingSpinner(t *testing.T) {
	rl := newRailList()
	base := time.UnixMilli(2_000_000)
	rl.now = func() time.Time { return base }
	rl.SetOrchestrators([]*orchEntry{{
		ID: 1, Name: "team", CoordTaskID: "coord-1",
		Roles: []*roleEntry{{
			OrchestratorID: 1, RoleID: 10, Name: "runner",
			Live: true, ArgusTaskID: "t-run",
			HasState: true, Status: "in_progress",
		}},
	}})

	out := renderRail(t, rl, 36, 6)
	frame0 := spinnerFrames[rl.animFrame()%len(spinnerFrames)]
	if !strings.Contains(out, string(frame0)+" runner") {
		t.Fatalf("running row must render spinner frame %q before its name; got:\n%s", frame0, out)
	}

	rl.now = func() time.Time { return base.Add(150 * time.Millisecond) }
	out = renderRail(t, rl, 36, 6)
	frame1 := spinnerFrames[rl.animFrame()%len(spinnerFrames)]
	if frame1 == frame0 {
		t.Fatal("expected the wall-clock frame to advance across a 150ms boundary")
	}
	if !strings.Contains(out, string(frame1)+" runner") {
		t.Fatalf("after one tick the row must render the next frame %q; got:\n%s", frame1, out)
	}
}

// Spec (icon-alignment): the spinner driver schedules repaints only while a
// running row exists — HasRunningRows reports whether any visible row (role,
// sub-coord, freelance, or coord header) is in_progress, not idle, and not
// blocked on input.
func TestRailList_HasRunningRows(t *testing.T) {
	rl := newRailList()
	if rl.HasRunningRows() {
		t.Fatal("empty rail must not report running rows")
	}

	running := &roleEntry{OrchestratorID: 1, RoleID: 10, Name: "runner", ArgusTaskID: "t", HasState: true, Status: "in_progress"}
	rl.SetOrchestrators([]*orchEntry{{ID: 1, Name: "team", Roles: []*roleEntry{running}}})
	if !rl.HasRunningRows() {
		t.Fatal("rail with a running role must report running rows")
	}

	// Idle, needs-input, and terminal states are NOT running.
	for _, quiet := range []*roleEntry{
		{OrchestratorID: 1, RoleID: 11, Name: "idle", ArgusTaskID: "t1", HasState: true, Status: "in_progress", ArgusIdle: true},
		{OrchestratorID: 1, RoleID: 12, Name: "blocked", ArgusTaskID: "t2", HasState: true, Status: "in_progress", NeedsInput: true},
		{OrchestratorID: 1, RoleID: 13, Name: "done", ArgusTaskID: "t3", HasState: true, Status: "complete"},
		{OrchestratorID: 1, RoleID: 14, Name: "review", ArgusTaskID: "t4", HasState: true, Status: "in_review"},
		{OrchestratorID: 1, RoleID: 15, Name: "unknown", ArgusTaskID: "t5", Live: true},
	} {
		rl.SetOrchestrators([]*orchEntry{{ID: 1, Name: "team", Roles: []*roleEntry{quiet}}})
		if rl.HasRunningRows() {
			t.Errorf("row %q must not count as running", quiet.Name)
		}
	}

	// A running coord task on the header counts even with no child rows.
	rl.SetOrchestrators([]*orchEntry{{
		ID: 2, Name: "solo", CoordTaskID: "c", CoordHasState: true, CoordStatus: "in_progress",
	}})
	if !rl.HasRunningRows() {
		t.Fatal("rail with a running coord header must report running rows")
	}

	// A running freelance row counts too.
	rl.SetOrchestrators(nil)
	rl.SetFreelance([]*freelanceProject{{Project: "Hera", Tasks: []*roleEntry{
		{Name: "free", ArgusTaskID: "f", RoleKind: "freelance", HasState: true, Status: "in_progress"},
	}}})
	if !rl.HasRunningRows() {
		t.Fatal("rail with a running freelancer must report running rows")
	}
}

// Spec (complete-not-archived): the spinner driver goes quiet when the LAST
// running row leaves the rail — the running → complete transition on the same
// rail data clears hasRunning on the rebuild, so the 150ms tick stops
// scheduling repaints (no tick-forever leak once everything settles).
func TestRailList_HasRunningRowsClearsWhenLastRunnerCompletes(t *testing.T) {
	rl := newRailList()
	runner := &roleEntry{OrchestratorID: 1, RoleID: 10, Name: "runner", ArgusTaskID: "t", HasState: true, Status: "in_progress"}
	rl.SetOrchestrators([]*orchEntry{{ID: 1, Name: "team", Roles: []*roleEntry{runner}}})
	if !rl.HasRunningRows() {
		t.Fatal("running row must arm the spinner driver")
	}

	// The same row steps to complete; the next rebuild must disarm the driver.
	// The completed row stays in the ACTIVE tree (status never buckets) — it
	// simply no longer renders the animated spinner.
	runner.Status = "complete"
	rl.SetOrchestrators([]*orchEntry{{ID: 1, Name: "team", Roles: []*roleEntry{runner}}})
	if rl.HasRunningRows() {
		t.Fatal("after the last running row completes, the spinner driver must go quiet")
	}
}

// Spec (mixed-coord-repair): a mixed-coord header — orchestrator displayed
// active while its coord-pane binding's argus task is ARCHIVED — renders the
// ⊘ repair cue in error red at the status-icon cell, replacing the coord
// task's status glyph, so the operator SEES "this coord is broken/archived"
// at a glance.
func TestOrchIcon_MixedCoordArchivedRendersRepairCue(t *testing.T) {
	rl := newRailList()
	o := &orchEntry{
		ID: 1, Name: "live-proj", Archived: false, CoordTaskID: "t-1",
		CoordHasState: true, CoordStatus: "in_review", CoordArgusArchived: true,
	}
	glyph, style := rl.orchIcon(o)
	if glyph != iconCoordBroken {
		t.Fatalf("mixed-coord header: glyph = %q, want %q (⊘ repair cue)", glyph, iconCoordBroken)
	}
	if style != theme.StyleError {
		t.Fatalf("mixed-coord header cue must render in error style; got %v", style)
	}
}

// Spec (mixed-coord-repair): the cue applies ONLY to the MIXED state. An
// orchestrator that is itself archived renders the normal dimmed-archived
// treatment (true status glyph, dimmed) even when its coord task is also
// argus-archived — both sides agree, nothing is broken.
func TestOrchIcon_ArchivedOrchestratorSkipsRepairCue(t *testing.T) {
	rl := newRailList()
	o := &orchEntry{
		ID: 1, Name: "shipped", Archived: true, CoordTaskID: "t-1",
		CoordHasState: true, CoordStatus: "complete", CoordArgusArchived: true,
	}
	glyph, style := rl.orchIcon(o)
	if glyph != '✓' {
		t.Fatalf("archived-both-sides header: glyph = %q, want '✓' (no cue)", glyph)
	}
	if style != theme.StyleDimmed {
		t.Fatalf("archived-both-sides header must render dimmed; got %v", style)
	}
}

// Spec (mixed-coord-repair): a header whose coord task is NOT argus-archived
// renders its normal status glyph — the cue never fires on a healthy header.
func TestOrchIcon_HealthyHeaderRendersNormalGlyph(t *testing.T) {
	rl := newRailList()
	o := &orchEntry{
		ID: 1, Name: "healthy", CoordTaskID: "t-1",
		CoordHasState: true, CoordStatus: "in_review",
	}
	glyph, style := rl.orchIcon(o)
	if glyph != theme.IconReview {
		t.Fatalf("healthy in_review header: glyph = %q, want IconReview/clipboard-check", glyph)
	}
	if style != theme.StyleInReview {
		t.Fatalf("healthy in_review header style = %v, want StyleInReview", style)
	}
}

// --- PR review indicator (change add-pr-review-indicator) ---

// Spec: actionable PR states (awaiting-review, changes-requested, approved)
// must render the correct glyph and style; non-actionable states return ok=false.
func TestPRGlyph_ActionableStatesReturnGlyphAndOk(t *testing.T) {
	cases := []struct {
		state     string
		wantGlyph rune
		wantStyle tcell.Style
	}{
		{"awaiting-review", theme.IconPRAwaiting, theme.StylePRAwaiting},
		{"changes-requested", theme.IconPRChanges, theme.StylePRChanges},
		{"approved", theme.IconPRApproved, theme.StylePRApproved},
	}
	for _, tc := range cases {
		got, style, ok := prGlyph(tc.state)
		if !ok {
			t.Errorf("prGlyph(%q): ok=false, want true", tc.state)
			continue
		}
		if got != tc.wantGlyph {
			t.Errorf("prGlyph(%q): glyph=%q, want %q", tc.state, got, tc.wantGlyph)
		}
		if style != tc.wantStyle {
			t.Errorf("prGlyph(%q): style mismatch", tc.state)
		}
	}
}

// Spec: non-actionable PR states produce no indicator cell.
func TestPRGlyph_NonActionableStatesReturnNotOk(t *testing.T) {
	for _, state := range []string{"none", "draft", "merged-closed", "unknown", ""} {
		_, _, ok := prGlyph(state)
		if ok {
			t.Errorf("prGlyph(%q): ok=true, want false (non-actionable)", state)
		}
	}
}

// Spec: when a role row has an actionable PR state the glyph renders in the
// rail and the name shifts right; for non-actionable states the name reclaims
// the space and appears at the same column as the status icon end.
func TestRailList_PRIndicatorCellRendersForActionableState(t *testing.T) {
	mk := func(prState string) *railList {
		rl := newRailList()
		rl.SetOrchestrators([]*orchEntry{
			{ID: 1, Name: "proj", CoordTaskID: "ct", Roles: []*roleEntry{
				{OrchestratorID: 1, RoleID: 10, Name: "myworker", HasState: true, Status: "in_progress", PRState: prState},
			}},
		})
		return rl
	}

	// Actionable: the PR glyph must appear in the rendered output.
	for _, state := range []string{"awaiting-review", "changes-requested", "approved"} {
		rl := mk(state)
		got := renderRail(t, rl, 40, 6)
		prRune, _, _ := prGlyph(state)
		if !strings.ContainsRune(got, prRune) {
			t.Errorf("actionable PR state %q: PR glyph %q not found in render:\n%s", state, prRune, got)
		}
		if !strings.Contains(got, "myworker") {
			t.Errorf("actionable PR state %q: role name missing from render:\n%s", state, got)
		}
	}

	// Non-actionable: no PR glyph should appear; name must still render.
	for _, state := range []string{"none", "draft", "merged-closed", "unknown", ""} {
		rl := mk(state)
		got := renderRail(t, rl, 40, 6)
		for _, s := range []string{"awaiting-review", "changes-requested", "approved"} {
			pr, _, _ := prGlyph(s)
			if strings.ContainsRune(got, pr) {
				t.Errorf("non-actionable PR state %q: found PR glyph %q unexpectedly:\n%s", state, pr, got)
			}
		}
		if !strings.Contains(got, "myworker") {
			t.Errorf("non-actionable PR state %q: role name missing from render:\n%s", state, got)
		}
	}
}

// Spec: the PR indicator cell only consumes width when actionable — the name
// column starts FURTHER right when there is an actionable PR than when there
// is not. Verifies the "reclaim space" behavior mirrors argus.
func TestRailList_PRCellReclainsSpaceWhenNonActionable(t *testing.T) {
	mkRole := func(prState string) *railList {
		rl := newRailList()
		rl.SetOrchestrators([]*orchEntry{
			{ID: 1, Name: "p", CoordTaskID: "ct", Roles: []*roleEntry{
				{OrchestratorID: 1, RoleID: 10, Name: "xworker", HasState: true, Status: "in_progress", PRState: prState},
			}},
		})
		return rl
	}
	withPR := renderRail(t, mkRole("approved"), 50, 6)
	noPR := renderRail(t, mkRole(""), 50, 6)

	lines := strings.Split(withPR, "\n")
	nlines := strings.Split(noPR, "\n")
	colWithPR := runeColOf(lines)("xworker")
	colNoPR := runeColOf(nlines)("xworker")
	if colWithPR <= colNoPR {
		t.Fatalf("with PR indicator, name should start further right (%d) than without (%d)", colWithPR, colNoPR)
	}
}

// --- rail search/filter (change rail-search) ---

// railNames returns the set of rendered row names present in a rail dump,
// tested via substring. Helper for filter assertions.
func railHas(dump, needle string) bool { return strings.Contains(dump, needle) }

// Scenario: `/` narrows the rail to rows whose name matches (case-insensitive
// substring), hiding non-matching coordinators and agents.
func TestRailList_FilterNarrowsToNameMatches(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "alpha", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "scout", RoleKind: "worker", Live: true},
		}},
		{ID: 2, Name: "beta", CoordTaskID: "c-2", Roles: []*roleEntry{
			{OrchestratorID: 2, RoleID: 20, Name: "ranger", RoleKind: "worker", Live: true},
		}},
	})
	rl.SetFilter("ALPHA") // case-insensitive
	dump := renderRail(t, rl, 32, 14)
	if !railHas(dump, "alpha") {
		t.Fatalf("matching coordinator 'alpha' must render; got:\n%s", dump)
	}
	if railHas(dump, "beta") || railHas(dump, "ranger") {
		t.Fatalf("non-matching rows must be hidden; got:\n%s", dump)
	}
}

// Scenario: a matching agent keeps its parent coordinator header visible and
// expanded (ancestry-preserving) even when the coordinator name doesn't match.
func TestRailList_FilterPreservesAncestry(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "alpha", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "scout", RoleKind: "worker", Live: true},
		}},
		{ID: 2, Name: "beta", CoordTaskID: "c-2", Roles: []*roleEntry{
			{OrchestratorID: 2, RoleID: 20, Name: "ranger", RoleKind: "worker", Live: true},
		}},
	})
	rl.SetFilter("ranger") // matches an agent under "beta", not the coord name
	dump := renderRail(t, rl, 32, 14)
	if !railHas(dump, "ranger") {
		t.Fatalf("matching agent 'ranger' must render; got:\n%s", dump)
	}
	if !railHas(dump, "beta") {
		t.Fatalf("matching agent's parent coordinator 'beta' must stay visible (ancestry); got:\n%s", dump)
	}
	if railHas(dump, "alpha") || railHas(dump, "scout") {
		t.Fatalf("unrelated coordinator/agent must be hidden; got:\n%s", dump)
	}
}

// Scenario: a filter auto-expands a coordinator that the operator had
// explicitly collapsed, so matches are never hidden behind a fold.
func TestRailList_FilterAutoExpandsCollapsed(t *testing.T) {
	rl := newRailList()
	rl.collapsed[1] = true // explicitly collapsed before the first build
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "alpha", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "scout", RoleKind: "worker", Live: true},
		}},
	})
	if railHas(renderRail(t, rl, 32, 12), "scout") {
		t.Fatalf("precondition: collapsed coordinator should hide 'scout'")
	}
	rl.SetFilter("scout")
	if !railHas(renderRail(t, rl, 32, 12), "scout") {
		t.Fatalf("filter must auto-expand the collapsed coordinator to reveal the match")
	}
}

// Scenario: whitespace-separated terms each must match (name terms).
func TestRailList_FilterWhitespaceTerms(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "alpha", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "scout-fixer", RoleKind: "worker", Live: true},
			{OrchestratorID: 1, RoleID: 11, Name: "scout-builder", RoleKind: "worker", Live: true},
		}},
	})
	rl.SetFilter("scout fixer") // both terms must match the same row
	dump := renderRail(t, rl, 34, 12)
	if !railHas(dump, "scout-fixer") {
		t.Fatalf("'scout fixer' must match 'scout-fixer'; got:\n%s", dump)
	}
	if railHas(dump, "scout-builder") {
		t.Fatalf("'scout-builder' lacks term 'fixer' and must be hidden; got:\n%s", dump)
	}
}

// Scenario: Esc / ClearFilter restores the full, unfiltered rail.
func TestRailList_ClearFilterRestoresFullRail(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "alpha", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "scout", RoleKind: "worker", Live: true},
		}},
		{ID: 2, Name: "beta", CoordTaskID: "c-2", Roles: []*roleEntry{
			{OrchestratorID: 2, RoleID: 20, Name: "ranger", RoleKind: "worker", Live: true},
		}},
	})
	rl.BeginFilter()
	rl.SetFilter("alpha")
	if railHas(renderRail(t, rl, 32, 14), "beta") {
		t.Fatalf("precondition: filtered rail should hide 'beta'")
	}
	rl.ClearFilter()
	if rl.Filtering() {
		t.Fatalf("ClearFilter must exit input mode")
	}
	dump := renderRail(t, rl, 32, 14)
	if !railHas(dump, "alpha") || !railHas(dump, "beta") {
		t.Fatalf("ClearFilter must restore the full rail; got:\n%s", dump)
	}
}

// Scenario: Enter / AcceptFilter keeps the query applied but leaves input mode.
func TestRailList_AcceptFilterKeepsQueryLeavesInputMode(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "alpha", CoordTaskID: "c-1"},
		{ID: 2, Name: "beta", CoordTaskID: "c-2"},
	})
	rl.BeginFilter()
	rl.SetFilter("alpha")
	rl.AcceptFilter()
	if rl.Filtering() {
		t.Fatalf("AcceptFilter must leave input mode")
	}
	if rl.Filter() != "alpha" {
		t.Fatalf("AcceptFilter must keep the query; got %q", rl.Filter())
	}
	dump := renderRail(t, rl, 32, 10)
	if railHas(dump, "beta") {
		t.Fatalf("accepted filter must stay applied (beta hidden); got:\n%s", dump)
	}
}

// Scenario: while in input mode a rail mutation rune ('a') is appended to the
// query rather than triggering a mutation; the input handler consumes it.
func TestRailList_FilterInputAppendsRunes(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{{ID: 1, Name: "alpha", CoordTaskID: "c-1"}})
	rl.BeginFilter()
	rl.HandleFilterKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	rl.HandleFilterKey(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone))
	if rl.Filter() != "al" {
		t.Fatalf("filter input must append runes; got %q", rl.Filter())
	}
	// Backspace removes the last rune.
	rl.HandleFilterKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	if rl.Filter() != "a" {
		t.Fatalf("backspace must delete the last rune; got %q", rl.Filter())
	}
}

// Scenario: the active query renders as an unobtrusive `/`-prefixed input line
// while typing.
func TestRailList_FilterInputLineRendered(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{{ID: 1, Name: "alpha", CoordTaskID: "c-1"}})
	rl.BeginFilter()
	rl.SetFilter("alp")
	dump := renderRail(t, rl, 32, 12)
	if !strings.Contains(dump, "/alp") && !strings.Contains(dump, "/ alp") {
		t.Fatalf("filter input line must show the query; got:\n%s", dump)
	}
}

// --- selection marker probe-gating (change rail-search) ---

// Scenario: with the probe gate OFF (normal operation) no `›` marker renders,
// yet the selected row is still distinguishable by its selected text style.
func TestRailList_MarkerHiddenWithoutProbe(t *testing.T) {
	rl := newRailList()
	rl.probeMarker = false
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj1", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", RoleKind: "worker", Live: true},
		}},
	})
	rl.SelectByOrchID(1)
	dump, sim := renderRailSim(t, rl, 30, 8)
	if len(markerLines(dump)) != 0 {
		t.Fatalf("no `›` marker may render with the probe gate off; got:\n%s", dump)
	}
	y := rowOf(dump, "proj1")
	if y < 0 || !rowHasForeground(sim, y, theme.ColorSelected) {
		t.Fatalf("selected row must still render in theme.ColorSelected; got:\n%s", dump)
	}
}

// Scenario: with the probe gate ON the `›` marker renders once, on the
// selected row, exactly as before.
func TestRailList_MarkerShownWithProbe(t *testing.T) {
	rl := newRailList()
	rl.probeMarker = true
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "proj1", CoordTaskID: "c-1", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w1", RoleKind: "worker", Live: true},
		}},
	})
	rl.SelectByOrchID(1)
	assertMarkerOnRow(t, renderRail(t, rl, 30, 8), "proj1")
}

// Scenario: the gutter is reserved in both gate states, so toggling the marker
// never shifts row content horizontally.
func TestRailList_MarkerGateDoesNotShiftContent(t *testing.T) {
	mk := func(probe bool) string {
		rl := newRailList()
		rl.probeMarker = probe
		rl.SetOrchestrators([]*orchEntry{
			{ID: 1, Name: "proj1", CoordTaskID: "c-1", Roles: []*roleEntry{
				{OrchestratorID: 1, RoleID: 10, Name: "w1", RoleKind: "worker", Live: true},
			}},
		})
		rl.SelectByOrchID(1)
		return renderRail(t, rl, 30, 8)
	}
	off := runeColOf(strings.Split(mk(false), "\n"))
	on := runeColOf(strings.Split(mk(true), "\n"))
	for _, needle := range []string{"proj1", "w1"} {
		a, b := off(needle), on(needle)
		if a < 0 || b < 0 {
			t.Fatalf("%q missing from a render", needle)
		}
		if a != b {
			t.Fatalf("%q shifted from col %d (gate off) to %d (gate on)", needle, a, b)
		}
	}
}
