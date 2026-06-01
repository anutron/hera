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
	// complete coordinator: ✓ icon before its (coord-less) header.
	if !strings.Contains(got, "✓ ▾ "+string(iconCoord)+" shipped") {
		t.Fatalf("expected ✓ status icon on completed coord header; got:\n%s", got)
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
	// which in turn is indented more than the parent coord. Find the column of
	// the needle within each line (the rail border occupies column 0).
	lines := strings.Split(got, "\n")
	indent := runeColOf(lines)
	parentIndent := indent(string(iconCoord) + " parent")
	subIndent := indent(string(iconCoord) + " sub")
	leafIndent := indent("leaf-worker")
	if !(parentIndent < subIndent && subIndent < leafIndent) {
		t.Fatalf("expected increasing indentation parent(%d) < sub(%d) < leaf(%d); got:\n%s", parentIndent, subIndent, leafIndent, got)
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
