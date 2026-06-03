package ops

import (
	"context"
	"errors"
	"strings"
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

// --- explicit archive/unarchive verbs (direction decided by the VIEW from the
// effective rendered state, not re-derived from the hera flag) ---

func TestArchiveRole_Explicit_SetsHeraAndArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")

	if err := s.ArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ArchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived")
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T1" {
		t.Fatalf("argus archive calls = %v, want [T1]", argus.archiveCalls)
	}
}

func TestArchiveRole_EndedBindingFallsBackToLatest(t *testing.T) {
	// The mirror of the unarchive fallback: a role whose binding was ended
	// by a PREVIOUS archive (end_reason='argus_archived') keeps its argus
	// task id but has no live binding. When such a role is active again
	// (e.g. only the argus side was unarchived — the live mixed state) and
	// the operator archives it, the argus archive MUST still be issued via
	// the latest-binding fallback — skipping silently leaves the argus task
	// active while hera stamps archived_at, recreating the mixed state.
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedEndedBinding(role.ID, "T1", "/tmp/wt1")

	if err := s.ArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ArchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived")
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T1" {
		t.Fatalf("argus archive calls = %v, want [T1] via latest-binding fallback", argus.archiveCalls)
	}
}

func TestArchiveRole_PrefersLiveBindingOverEnded(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedEndedBinding(role.ID, "T-old", "/tmp/wt1")
	db.seedBinding(role.ID, "T-live", "/tmp/wt2")

	if err := s.ArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ArchiveRole: %v", err)
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T-live" {
		t.Fatalf("argus archive calls = %v, want [T-live] (live binding preferred)", argus.archiveCalls)
	}
}

func TestArchiveOrchestrator_CascadeArchivesEndedBindingTask(t *testing.T) {
	// The cascade path calls the role-level ArchiveRole, so it MUST inherit
	// the latest-binding fallback — a cascaded role with only an ended
	// binding still archives its argus task.
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedEndedBinding(w1.ID, "T1", "/tmp/wt1")

	if err := s.ArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("ArchiveOrchestrator: %v", err)
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T1" {
		t.Fatalf("argus archive calls = %v, want [T1] (cascade inherits the fallback)", argus.archiveCalls)
	}
}

func TestArchiveRole_SharedTaskGuard_SkipsArgusWhenLiveBoundElsewhere(t *testing.T) {
	// The cascade-collateral hazard: a role's ENDED binding can record a
	// task that is ALSO the LIVE-bound task of a different role (reused
	// sessions / multi-binding history). Archiving the stale role must not
	// reach through its ended binding and archive a task an ACTIVE role
	// depends on — the hera-side archive proceeds, the argus side is
	// skipped, and the skip is logged with both role names.
	s, db, argus, _, log := newTestService()
	oldOrch := db.seedOrchestrator("old", false)
	stale := db.seedRole(oldOrch.ID, "w1", KindWorker, "old", false)
	db.seedEndedBinding(stale.ID, "T1", "/tmp/wt-old")
	newOrch := db.seedOrchestrator("new", false)
	owner := db.seedRole(newOrch.ID, "coord", KindCoordinator, "new", false)
	db.seedBinding(owner.ID, "T1", "/tmp/wt-new")

	if err := s.ArchiveRole(context.Background(), stale.ID); err != nil {
		t.Fatalf("ArchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), stale.ID)
	if !got.Archived {
		t.Fatalf("role should be archived hera-side")
	}
	if len(argus.archiveCalls) != 0 {
		t.Fatalf("argus archive calls = %v, want none (task live-bound elsewhere)", argus.archiveCalls)
	}
	var found bool
	for _, m := range log.messages {
		if strings.Contains(m, "w1") && strings.Contains(m, "coord") && strings.Contains(m, "T1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("skip should be logged with both role names; got %v", log.messages)
	}
}

func TestArchiveRole_SharedTaskGuard_OtherLiveBindingOnDifferentTaskArchives(t *testing.T) {
	// The guard keys on the TASK id, not on the mere existence of other
	// live bindings — a sibling live-bound to a DIFFERENT task must not
	// suppress the fallback archive.
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("old", false)
	stale := db.seedRole(orch.ID, "w1", KindWorker, "old", false)
	db.seedEndedBinding(stale.ID, "T1", "/tmp/wt1")
	other := db.seedRole(orch.ID, "w2", KindWorker, "old", false)
	db.seedBinding(other.ID, "T2", "/tmp/wt2")

	if err := s.ArchiveRole(context.Background(), stale.ID); err != nil {
		t.Fatalf("ArchiveRole: %v", err)
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T1" {
		t.Fatalf("argus archive calls = %v, want [T1] (no live binding on T1 itself)", argus.archiveCalls)
	}
}

func TestArchiveRole_OwnLiveBindingArchivesEvenWhenShared(t *testing.T) {
	// A task resolved via the role's OWN live binding archives as today —
	// the live binding IS the ownership claim — even when another role also
	// holds a live binding to the same task (multi-binding).
	s, db, argus, _, _ := newTestService()
	orchA := db.seedOrchestrator("a", false)
	role := db.seedRole(orchA.ID, "w1", KindWorker, "a", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")
	orchB := db.seedOrchestrator("b", false)
	sibling := db.seedRole(orchB.ID, "w9", KindWorker, "b", false)
	db.seedBinding(sibling.ID, "T1", "/tmp/wt9")

	if err := s.ArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ArchiveRole: %v", err)
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T1" {
		t.Fatalf("argus archive calls = %v, want [T1] (own live binding is ownership)", argus.archiveCalls)
	}
}

func TestArchiveOrchestrator_CascadeSkipsTaskLiveBoundElsewhere(t *testing.T) {
	// The operator's live symptom: cascading an OLD orchestrator archived
	// the task an ACTIVE orchestrator's coord was live-bound to. The cascade
	// inherits ArchiveRole's shared-task guard — the old orchestrator and
	// its roles archive hera-side, the shared task's argus archive is
	// skipped, and the cascade still succeeds.
	s, db, argus, _, _ := newTestService()
	oldOrch := db.seedOrchestrator("old", false)
	w1 := db.seedRole(oldOrch.ID, "w1", KindWorker, "old", false)
	db.seedEndedBinding(w1.ID, "T1", "/tmp/wt-old")
	newOrch := db.seedOrchestrator("new", false)
	owner := db.seedRole(newOrch.ID, "coord", KindCoordinator, "new", false)
	db.seedBinding(owner.ID, "T1", "/tmp/wt-new")

	if err := s.ArchiveOrchestrator(context.Background(), oldOrch.ID); err != nil {
		t.Fatalf("ArchiveOrchestrator: %v", err)
	}
	if len(argus.archiveCalls) != 0 {
		t.Fatalf("argus archive calls = %v, want none (T1 live-bound under the active orchestrator)", argus.archiveCalls)
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), oldOrch.ID)
	if !gotOrch.Archived {
		t.Fatalf("old orchestrator should be archived (skip is not a failure)")
	}
	gotRole, _ := db.GetRoleByID(context.Background(), w1.ID)
	if !gotRole.Archived {
		t.Fatalf("cascaded role should be archived hera-side")
	}
}

func TestUnarchiveRole_Explicit_ClearsHeraAndArgus(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")

	if err := s.UnarchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("UnarchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role should be unarchived")
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T1" {
		t.Fatalf("argus unarchive calls = %v, want [T1]", argus.unarchiveCalls)
	}
}

// The live-found mixed-flag state: hera-active (archived_at NULL) but the
// bound argus task is archived. The row DISPLAYS as archived, so the view
// dispatches UnarchiveRole. The hera side is already clear (the DB unarchive
// is a harmless no-op) and the argus side MUST be unarchived — and critically
// NO fresh archived_at may be stamped (the old flag-derived toggle re-archived
// exactly this state).
func TestUnarchiveRole_MixedFlags_HeraActiveArgusArchived(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false) // hera-active
	db.seedBinding(role.ID, "T1", "/tmp/wt1")                    // argus side archived per rail state

	if err := s.UnarchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("UnarchiveRole: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role must remain unarchived (no fresh archived_at)")
	}
	if len(db.archiveRoleCalls) != 0 {
		t.Fatalf("UnarchiveRole must never stamp archived_at; got archive DAO calls %v", db.archiveRoleCalls)
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "T1" {
		t.Fatalf("argus unarchive calls = %v, want [T1] (clear the argus side)", argus.unarchiveCalls)
	}
	if len(argus.archiveCalls) != 0 {
		t.Fatalf("unexpected argus archive calls: %v", argus.archiveCalls)
	}
}

func TestArchiveOrchestrator_Explicit_Cascades(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(w1.ID, "T1", "/tmp/wt1")

	if err := s.ArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("ArchiveOrchestrator: %v", err)
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if !gotOrch.Archived {
		t.Fatalf("orchestrator should be archived")
	}
	gotRole, _ := db.GetRoleByID(context.Background(), w1.ID)
	if !gotRole.Archived {
		t.Fatalf("role should be cascade-archived")
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "T1" {
		t.Fatalf("argus archive calls = %v, want [T1]", argus.archiveCalls)
	}
}

func TestUnarchiveOrchestrator_Explicit_UnarchivesCoordTask(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", true)
	db.seedBinding(coord.ID, "TC", "/tmp/wtc")

	if err := s.UnarchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("UnarchiveOrchestrator: %v", err)
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if gotOrch.Archived {
		t.Fatalf("orchestrator should be unarchived")
	}
	if len(argus.unarchiveCalls) != 1 || argus.unarchiveCalls[0] != "TC" {
		t.Fatalf("argus unarchive calls = %v, want [TC]", argus.unarchiveCalls)
	}
	// Workers (here: none besides coord) stay as-is; coord ROLE row remains
	// archived per the no-cascade unarchive contract.
	gotCoord, _ := db.GetRoleByID(context.Background(), coord.ID)
	if !gotCoord.Archived {
		t.Fatalf("coord role row must stay archived (unarchive does not cascade)")
	}
}
