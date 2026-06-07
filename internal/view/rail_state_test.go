package view

import (
	"context"
	"testing"

	"github.com/anutron/hera/internal/db"
)

func TestRailStatePersistRoundTrip(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/state.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	ctx := context.Background()

	// Load from an empty DB → all maps nil, no error.
	s, err := loadRailStateFromDB(ctx, d.Config)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if !s.isEmpty() {
		t.Fatalf("expected empty state on fresh DB, got %+v", s)
	}

	// Save a non-trivial state with last-selection fields (BUG-001).
	want := railViewState{
		Collapsed:          map[int64]bool{1: true, 2: false},
		FreelanceCollapsed: map[string]bool{"repo-a": true},
		ArchiveExpanded:    map[int64]bool{0: true, 7: true},
		LastSelection: railLastSelection{
			RoleID:      42,
			ArgusTaskID: "task-abc",
			OrchID:      0,
		},
	}
	if err := saveRailStateToDB(ctx, d.Config, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Reload and compare.
	got, err := loadRailStateFromDB(ctx, d.Config)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Collapsed[1] != true || got.Collapsed[2] != false {
		t.Errorf("Collapsed mismatch: %v", got.Collapsed)
	}
	if got.FreelanceCollapsed["repo-a"] != true {
		t.Errorf("FreelanceCollapsed mismatch: %v", got.FreelanceCollapsed)
	}
	if !got.ArchiveExpanded[0] || !got.ArchiveExpanded[7] {
		t.Errorf("ArchiveExpanded mismatch: %v", got.ArchiveExpanded)
	}
	// BUG-001: last-selection fields must round-trip.
	if got.LastSelection.RoleID != 42 {
		t.Errorf("LastSelection.RoleID: want 42, got %d", got.LastSelection.RoleID)
	}
	if got.LastSelection.ArgusTaskID != "task-abc" {
		t.Errorf("LastSelection.ArgusTaskID: want task-abc, got %q", got.LastSelection.ArgusTaskID)
	}
	if got.LastSelection.OrchID != 0 {
		t.Errorf("LastSelection.OrchID: want 0, got %d", got.LastSelection.OrchID)
	}
}

func TestRailListViewStateRestoreRoundTrip(t *testing.T) {
	rl := newRailList()

	// Set some fold state via the maps directly (simulates ToggleCollapse).
	rl.collapsed[10] = true
	rl.collapsed[20] = false
	rl.freelanceCollapsed["my-repo"] = true
	rl.archiveExpanded[0] = true

	// Snapshot and restore to a fresh rail.
	s := rl.ViewState()
	rl2 := newRailList()
	rl2.RestoreViewState(s)

	if rl2.collapsed[10] != true || rl2.collapsed[20] != false {
		t.Errorf("collapsed not restored: %v", rl2.collapsed)
	}
	if !rl2.freelanceCollapsed["my-repo"] {
		t.Errorf("freelanceCollapsed not restored: %v", rl2.freelanceCollapsed)
	}
	if !rl2.archiveExpanded[0] {
		t.Errorf("archiveExpanded not restored: %v", rl2.archiveExpanded)
	}
}

// TestRailListViewStateCapturesLastSelection verifies that ViewState captures
// the current cursor's role/orch identity (BUG-001).
func TestRailListViewStateCapturesLastSelection(t *testing.T) {
	rl := newRailList()
	rl.orchestrators = []*orchEntry{
		{ID: 7, Name: "alpha", Roles: []*roleEntry{
			{RoleID: 42, ArgusTaskID: "task-x", OrchestratorID: 7, Name: "worker"},
		}},
	}
	rl.buildRows()

	// Cursor on the orch header → LastSelection should capture OrchID.
	rl.cursor = 0 // first row = orch header
	s := rl.ViewState()
	if s.LastSelection.OrchID != 7 {
		t.Errorf("orch header: LastSelection.OrchID want 7, got %d", s.LastSelection.OrchID)
	}
	if s.LastSelection.RoleID != 0 {
		t.Errorf("orch header: LastSelection.RoleID want 0, got %d", s.LastSelection.RoleID)
	}

	// Cursor on the role row → LastSelection should capture RoleID and ArgusTaskID.
	rl.cursor = 1 // second row = role
	s = rl.ViewState()
	if s.LastSelection.RoleID != 42 {
		t.Errorf("role row: LastSelection.RoleID want 42, got %d", s.LastSelection.RoleID)
	}
	if s.LastSelection.ArgusTaskID != "task-x" {
		t.Errorf("role row: LastSelection.ArgusTaskID want task-x, got %q", s.LastSelection.ArgusTaskID)
	}
	if s.LastSelection.OrchID != 0 {
		t.Errorf("role row: LastSelection.OrchID want 0, got %d", s.LastSelection.OrchID)
	}
}

func TestRailListStateChangeCallback(t *testing.T) {
	rl := newRailList()

	fired := 0
	rl.SetOnStateChanged(func() { fired++ })

	// Build a minimal rail with one coordinator so ToggleCollapse has something.
	rl.orchestrators = []*orchEntry{
		{ID: 1, Name: "alpha"},
	}
	rl.buildRows()
	// Move cursor to the coordinator row.
	rl.cursor = 0

	rl.ToggleCollapse()
	if fired != 1 {
		t.Errorf("expected 1 state-change callback after ToggleCollapse, got %d", fired)
	}
}
