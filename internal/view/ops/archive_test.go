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

func TestToggleArchiveRole_UnarchiveUnarchivesArgusToo(t *testing.T) {
	// Symmetric toggle: the rail buckets a row as archived when EITHER
	// side is archived, so a hera-only unarchive of an argus-archived
	// task would produce zero visible change. `a` must clear both.
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true) // already archived
	db.seedBinding(role.ID, "T1", "/tmp/wt1")

	if err := s.ToggleArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ToggleArchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role should be unarchived")
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T1" {
		t.Fatalf("argus unarchive calls = %v, want [T1] (symmetric toggle)", argus.unarchiveCalls)
	}
	if len(argus.archiveCalls) != 0 {
		t.Fatalf("unexpected argus archive calls: %v", argus.archiveCalls)
	}
}

func TestToggleArchiveRole_UnarchiveNoBindingSkipsArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	// no binding seeded — argus skip, hera unarchive still proceeds.

	if err := s.ToggleArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ToggleArchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role should be unarchived even with no live binding")
	}
	if len(argus.archiveCalls)+len(argus.unarchiveCalls) != 0 {
		t.Fatalf("expected no argus calls, got archive=%v unarchive=%v", argus.archiveCalls, argus.unarchiveCalls)
	}
}

func TestToggleArchiveRole_UnarchiveEndedBindingFallsBackToLatest(t *testing.T) {
	// The archived-role shape: archiving the task ENDED the binding
	// (end_reason='argus_archived') while keeping its argus task id, so
	// EVERY archived row has no live binding. The unarchive must resolve
	// the latest ended binding and still POST argus unarchive — skipping
	// silently defeats the symmetric toggle for exactly the rows that
	// need it (the row never visibly leaves the Archive expando).
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedEndedBinding(role.ID, "T1", "/tmp/wt1")

	if err := s.ToggleArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ToggleArchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role should be unarchived")
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T1" {
		t.Fatalf("argus unarchive calls = %v, want [T1] via latest-binding fallback", argus.unarchiveCalls)
	}
}

func TestToggleArchiveRole_UnarchivePrefersLiveBindingOverEnded(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedEndedBinding(role.ID, "T-old", "/tmp/wt1")
	db.seedBinding(role.ID, "T-live", "/tmp/wt2")

	if err := s.ToggleArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ToggleArchiveRole: %v", err)
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T-live" {
		t.Fatalf("argus unarchive calls = %v, want [T-live] (live binding preferred)", argus.unarchiveCalls)
	}
}

func TestToggleArchiveRole_UnarchiveArgusErrorPropagates(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")
	argus.unarchiveErr = errors.New("network down")

	err := s.ToggleArchiveRole(context.Background(), role.ID)
	if err == nil {
		t.Fatalf("expected error")
	}
	// Role row is still unarchived per DB DAO call ordering — the
	// argus call comes after. Mirrors the archive direction's choice.
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role should remain unarchived even on argus failure")
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
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", true)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedBinding(coord.ID, "T-coord", "/tmp/coord")
	db.seedBinding(w1.ID, "T-w1", "/tmp/w1")

	if err := s.ToggleArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("ToggleArchiveOrchestrator: %v", err)
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if gotOrch.Archived {
		t.Fatalf("orchestrator should be unarchived")
	}
	// Symmetric toggle: the coord task unarchives on the argus side —
	// and ONLY the coord task; worker tasks stay archived.
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T-coord" {
		t.Fatalf("argus unarchive calls = %v, want [T-coord]", argus.unarchiveCalls)
	}
	// Roles must stay archived hera-side.
	for _, id := range []int64{coord.ID, w1.ID} {
		got, _ := db.GetRoleByID(context.Background(), id)
		if !got.Archived {
			t.Fatalf("role %d MUST stay archived (no cascade on unarchive)", id)
		}
	}
}

func TestToggleArchiveOrchestrator_UnarchiveNoCoordBindingSkipsArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	db.seedRole(orch.ID, "coord", KindCoordinator, "foo", true)
	// coord has no live binding — argus skip, orchestrator unarchive proceeds.

	if err := s.ToggleArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("ToggleArchiveOrchestrator: %v", err)
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if gotOrch.Archived {
		t.Fatalf("orchestrator should be unarchived")
	}
	if len(argus.archiveCalls)+len(argus.unarchiveCalls) != 0 {
		t.Fatalf("expected no argus calls, got archive=%v unarchive=%v", argus.archiveCalls, argus.unarchiveCalls)
	}
}

// --- ToggleArchiveTask (task-direct, freelancers) ---

func TestToggleArchiveTask_Active_ArchivesByTaskID(t *testing.T) {
	// A freelancer is an unmanaged argus task: no role, no binding, no hera
	// DB write — the verb must address the argus task directly.
	s, db, a, _, _ := newTestService()

	if err := s.ToggleArchiveTask(context.Background(), "T9", false); err != nil {
		t.Fatalf("ToggleArchiveTask: %v", err)
	}
	if len(a.archiveCalls) != 1 || a.archiveCalls[0] != "T9" {
		t.Fatalf("ArchiveTask calls = %+v, want [T9]", a.archiveCalls)
	}
	if len(a.unarchiveCalls) != 0 {
		t.Fatalf("unexpected UnarchiveTask calls: %+v", a.unarchiveCalls)
	}
	if len(db.archiveRoleCalls)+len(db.archiveOrchCalls) != 0 {
		t.Fatalf("ToggleArchiveTask must not write hera DB rows")
	}
}

func TestToggleArchiveTask_Archived_UnarchivesByTaskID(t *testing.T) {
	s, _, a, _, _ := newTestService()

	if err := s.ToggleArchiveTask(context.Background(), "T9", true); err != nil {
		t.Fatalf("ToggleArchiveTask: %v", err)
	}
	if len(a.unarchiveCalls) != 1 || a.unarchiveCalls[0] != "T9" {
		t.Fatalf("UnarchiveTask calls = %+v, want [T9]", a.unarchiveCalls)
	}
	if len(a.archiveCalls) != 0 {
		t.Fatalf("unexpected ArchiveTask calls: %+v", a.archiveCalls)
	}
}

func TestToggleArchiveTask_EmptyTaskID_Errors(t *testing.T) {
	s, _, a, _, _ := newTestService()

	if err := s.ToggleArchiveTask(context.Background(), "", false); err == nil {
		t.Fatalf("ToggleArchiveTask with empty task id must error")
	}
	if len(a.archiveCalls)+len(a.unarchiveCalls) != 0 {
		t.Fatalf("empty task id must not call argus")
	}
}

func TestToggleArchiveTask_ArgusErrorPropagates(t *testing.T) {
	s, _, a, _, _ := newTestService()
	a.archiveErr = errors.New("argus 500")

	if err := s.ToggleArchiveTask(context.Background(), "T9", false); err == nil {
		t.Fatalf("ToggleArchiveTask must propagate the argus error")
	}
}
