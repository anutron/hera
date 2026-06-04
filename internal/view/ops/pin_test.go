package ops

import (
	"context"
	"testing"
)

// TestPinRole_PinsClearsArchivedAndUnarchivesArgus proves Story 1's pin/archive
// mutual exclusivity at the ops layer: pinning a role that displays as archived
// (its bound argus task is archived) sets pinned_at, clears the hera archived
// flag, AND unarchives the argus task so the row never lingers in an Archive
// expando.
func TestPinRole_PinsClearsArchivedAndUnarchivesArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true) // archived
	db.seedBinding(role.ID, "T1", "/tmp/wt1")

	if err := s.PinRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PinRole: %v", err)
	}
	if len(db.pinRoleCalls) != 1 || db.pinRoleCalls[0] != role.ID {
		t.Fatalf("pin role DAO calls = %v", db.pinRoleCalls)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Pinned {
		t.Fatalf("role should be pinned")
	}
	if got.Archived {
		t.Fatalf("pin must clear archived (mutual exclusivity)")
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T1" {
		t.Fatalf("pin must unarchive the bound argus task; calls = %v", argus.unarchiveCalls)
	}
}

func TestPinRole_NoBindingSkipsArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)

	if err := s.PinRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PinRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Pinned {
		t.Fatalf("role should be pinned")
	}
	if len(argus.archiveCalls)+len(argus.unarchiveCalls) != 0 {
		t.Fatalf("no binding → no argus call; archive=%v unarchive=%v", argus.archiveCalls, argus.unarchiveCalls)
	}
}

func TestUnpinRole_ClearsPinNoArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")
	_ = s.PinRole(context.Background(), role.ID)

	if err := s.UnpinRole(context.Background(), role.ID); err != nil {
		t.Fatalf("UnpinRole: %v", err)
	}
	if len(db.unpinRoleCalls) != 1 {
		t.Fatalf("unpin role DAO calls = %v", db.unpinRoleCalls)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Pinned {
		t.Fatalf("role should be unpinned")
	}
	// Unpin must not archive/unarchive — it only clears the pin.
	if len(argus.archiveCalls) != 0 {
		t.Fatalf("unpin must not call argus archive: %v", argus.archiveCalls)
	}
}

func TestPinOrchestrator_PinsClearsArchivedAndUnarchivesCoordTask(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true) // archived orchestrator
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", true)
	db.seedBinding(coord.ID, "T-coord", "/tmp/coord")

	if err := s.PinOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("PinOrchestrator: %v", err)
	}
	if len(db.pinOrchCalls) != 1 || db.pinOrchCalls[0] != orch.ID {
		t.Fatalf("pin orch DAO calls = %v", db.pinOrchCalls)
	}
	got, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if !got.Pinned || got.Archived {
		t.Fatalf("orchestrator should be pinned and not archived; got pinned=%v archived=%v", got.Pinned, got.Archived)
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T-coord" {
		t.Fatalf("pin orchestrator must unarchive the coord task; calls = %v", argus.unarchiveCalls)
	}
}

func TestUnpinOrchestrator_ClearsPin(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	_ = s.PinOrchestrator(context.Background(), orch.ID)

	if err := s.UnpinOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("UnpinOrchestrator: %v", err)
	}
	got, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if got.Pinned {
		t.Fatalf("orchestrator should be unpinned")
	}
}
