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

func TestRailList_HidesArchivedWhenFlagOff(t *testing.T) {
	rl := newRailList()
	rl.SetOrchestrators([]*orchEntry{
		{ID: 1, Name: "live", Roles: []*roleEntry{
			{OrchestratorID: 1, RoleID: 10, Name: "w-active"},
			{OrchestratorID: 1, RoleID: 11, Name: "w-old", Archived: true},
		}},
		{ID: 2, Name: "archived-orch", Archived: true, Roles: nil},
	})

	got := renderRail(t, rl, 22, 6)
	if strings.Contains(got, "w-old") {
		t.Fatalf("archived role should be hidden when showArchived=false; got:\n%s", got)
	}
	if strings.Contains(got, "archived-orch") {
		t.Fatalf("archived orch should be hidden when showArchived=false; got:\n%s", got)
	}
	if strings.Contains(got, "Archive") {
		t.Fatalf("archive separator should not appear when showArchived=false; got:\n%s", got)
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
