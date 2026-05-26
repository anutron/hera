package ops

import (
	"context"
	"errors"
	"testing"
)

func TestToggleArchiveRole_ArchivesAndCallsArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")

	if err := s.ToggleArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ToggleArchiveRole: %v", err)
	}

	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived")
	}
	if len(db.archiveRoleCalls) != 1 || db.archiveRoleCalls[0] != role.ID {
		t.Fatalf("archive role DAO calls = %v", db.archiveRoleCalls)
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T1" {
		t.Fatalf("argus archive calls = %v", argus.archiveCalls)
	}
}

func TestToggleArchiveRole_NoLiveBindingSkipsArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	// no binding seeded.

	if err := s.ToggleArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ToggleArchiveRole: %v", err)
	}
	if len(argus.archiveCalls) != 0 {
		t.Fatalf("expected no argus archive call, got %v", argus.archiveCalls)
	}
}

func TestToggleArchiveRole_UnarchiveDoesNotTouchArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true) // already archived

	if err := s.ToggleArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ToggleArchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role should be unarchived")
	}
	if len(argus.archiveCalls) != 0 || len(argus.unarchiveCalls) != 0 {
		t.Fatalf("unarchive must not call argus (auto-unarchive on argus side is operator concern)")
	}
}

func TestToggleArchiveRole_ArgusErrorPropagates(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")
	argus.archiveErr = errors.New("network down")

	err := s.ToggleArchiveRole(context.Background(), role.ID)
	if err == nil {
		t.Fatalf("expected error")
	}
	// Role row is still archived per DB DAO call ordering — the
	// argus call comes after. That matches design.md ordering choice.
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should remain archived even on argus failure")
	}
}

func TestToggleArchiveOrchestrator_CascadesArchive(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	w2 := db.seedRole(orch.ID, "w2", KindWorker, "foo", false)
	db.seedBinding(coord.ID, "T-coord", "/tmp/coord")
	db.seedBinding(w1.ID, "T-w1", "/tmp/w1")
	// w2 has no binding.

	if err := s.ToggleArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("ToggleArchiveOrchestrator: %v", err)
	}

	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if !gotOrch.Archived {
		t.Fatalf("orchestrator should be archived")
	}
	for _, id := range []int64{coord.ID, w1.ID, w2.ID} {
		got, _ := db.GetRoleByID(context.Background(), id)
		if !got.Archived {
			t.Fatalf("role %d should be archived", id)
		}
	}
	// Argus archive called for the two roles with live bindings.
	if len(argus.archiveCalls) != 2 {
		t.Fatalf("expected 2 argus archive calls (live bindings only), got %v", argus.archiveCalls)
	}
}

func TestToggleArchiveOrchestrator_UnarchiveDoesNotCascade(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", true)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)

	if err := s.ToggleArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("ToggleArchiveOrchestrator: %v", err)
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if gotOrch.Archived {
		t.Fatalf("orchestrator should be unarchived")
	}
	// Roles must stay archived.
	for _, id := range []int64{coord.ID, w1.ID} {
		got, _ := db.GetRoleByID(context.Background(), id)
		if !got.Archived {
			t.Fatalf("role %d MUST stay archived (no cascade on unarchive)", id)
		}
	}
}
