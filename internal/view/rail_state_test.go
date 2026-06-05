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
	defer d.Close()

	ctx := context.Background()

	// Load from an empty DB → all maps nil, no error.
	s, err := loadRailStateFromDB(ctx, d.Config)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if !s.isEmpty() {
		t.Fatalf("expected empty state on fresh DB, got %+v", s)
	}

	// Save a non-trivial state.
	want := railViewState{
		Collapsed:          map[int64]bool{1: true, 2: false},
		FreelanceCollapsed: map[string]bool{"repo-a": true},
		ArchiveExpanded:    map[int64]bool{0: true, 7: true},
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
