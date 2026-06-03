package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Prune tolerance: argus prunes (deletes) tasks outright, so a role's
// recorded argus_task_id can point at a task that no longer exists. The
// adapter surfaces that as an error wrapping ErrArgusTaskGone; the archive
// verbs must treat it as a successful no-op for the argus side. See the
// rail-truthfulness delta "Archive operations tolerate argus-pruned tasks".

// gone builds the adapter-shaped error: the raw argus detail wrapping the
// ops sentinel.
func gone() error {
	return fmt.Errorf("argus: POST /api/tasks/T/archive: HTTP 404: task not found: %w", ErrArgusTaskGone)
}

func TestArchiveRole_PrunedTaskSkipsArgusSide(t *testing.T) {
	s, db, argus, _, log := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "PRUNED", "/tmp/wt1")
	argus.archiveErrByTask = map[string]error{"PRUNED": gone()}

	if err := s.ArchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("ArchiveRole on pruned task should succeed, got: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if !got.Archived {
		t.Fatalf("role should be archived hera-side despite the pruned argus task")
	}
	if len(argus.archiveCalls) != 1 || argus.archiveCalls[0] != "PRUNED" {
		t.Fatalf("argus archive calls = %v, want [PRUNED]", argus.archiveCalls)
	}
	if len(log.messages) == 0 || !strings.Contains(strings.Join(log.messages, "\n"), "PRUNED") {
		t.Fatalf("skip should be logged with the task id, got: %v", log.messages)
	}
}

func TestUnarchiveRole_PrunedTaskSkipsArgusSide(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	db.seedEndedBinding(role.ID, "PRUNED", "/tmp/wt1")
	argus.unarchiveErrByTask = map[string]error{"PRUNED": gone()}

	if err := s.UnarchiveRole(context.Background(), role.ID); err != nil {
		t.Fatalf("UnarchiveRole on pruned task should succeed, got: %v", err)
	}
	got, _ := db.GetRoleByID(context.Background(), role.ID)
	if got.Archived {
		t.Fatalf("role should be unarchived hera-side despite the pruned argus task")
	}
}

func TestToggleArchiveTask_PrunedTaskIsNoOp(t *testing.T) {
	// Freelance toggle addresses the argus task directly; a pruned task is
	// a skip in both directions.
	s, _, argus, _, _ := newTestService()
	argus.archiveErrByTask = map[string]error{"PRUNED": gone()}
	argus.unarchiveErrByTask = map[string]error{"PRUNED": gone()}

	if err := s.ToggleArchiveTask(context.Background(), "PRUNED", false); err != nil {
		t.Fatalf("ToggleArchiveTask(archive) on pruned task should succeed, got: %v", err)
	}
	if err := s.ToggleArchiveTask(context.Background(), "PRUNED", true); err != nil {
		t.Fatalf("ToggleArchiveTask(unarchive) on pruned task should succeed, got: %v", err)
	}
}

func TestUnarchiveOrchestrator_PrunedCoordTaskSkipsArgusSide(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo", true)
	db.seedEndedBinding(coord.ID, "PRUNED", "/tmp/coord")
	argus.unarchiveErrByTask = map[string]error{"PRUNED": gone()}

	if err := s.UnarchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("UnarchiveOrchestrator with pruned coord task should succeed, got: %v", err)
	}
	got, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if got.Archived {
		t.Fatalf("orchestrator should be unarchived")
	}
}

func TestArchiveOrchestrator_CascadesThroughPrunedTasks(t *testing.T) {
	// The live-operator repro: an old orchestrator whose roles bind a mix
	// of live and pruned tasks must archive fully — pruned tasks are
	// skips, not aborts.
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("old", false)
	r1 := db.seedRole(orch.ID, "coord", KindCoordinator, "old", false)
	r2 := db.seedRole(orch.ID, "w-pruned", KindWorker, "old", false)
	r3 := db.seedRole(orch.ID, "w-live", KindWorker, "old", false)
	db.seedBinding(r1.ID, "LIVE1", "/tmp/c")
	db.seedBinding(r2.ID, "PRUNED", "/tmp/w1")
	db.seedBinding(r3.ID, "LIVE2", "/tmp/w2")
	argus.archiveErrByTask = map[string]error{"PRUNED": gone()}

	if err := s.ArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("ArchiveOrchestrator with a pruned task in the cascade should succeed, got: %v", err)
	}
	for _, id := range []int64{r1.ID, r2.ID, r3.ID} {
		got, _ := db.GetRoleByID(context.Background(), id)
		if !got.Archived {
			t.Fatalf("role %d should be archived", id)
		}
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if !gotOrch.Archived {
		t.Fatalf("orchestrator should be archived")
	}
	if len(argus.archiveCalls) != 3 {
		t.Fatalf("argus archive calls = %v, want all 3 tasks attempted", argus.archiveCalls)
	}
}

func TestArchiveOrchestrator_NonPrunedFailureContinuesAndAggregates(t *testing.T) {
	// A non-404 failure (argus flaky for one call) must not abort the
	// cascade: the remaining roles still archive, the orchestrator stays
	// active for a clean retry, and the summary error names the failed role.
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("old", false)
	r1 := db.seedRole(orch.ID, "w-ok", KindWorker, "old", false)
	r2 := db.seedRole(orch.ID, "w-broken", KindWorker, "old", false)
	r3 := db.seedRole(orch.ID, "w-ok2", KindWorker, "old", false)
	db.seedBinding(r1.ID, "T1", "/tmp/w1")
	db.seedBinding(r2.ID, "T2", "/tmp/w2")
	db.seedBinding(r3.ID, "T3", "/tmp/w3")
	boom := errors.New("argus: connection refused")
	argus.archiveErrByTask = map[string]error{"T2": boom}

	err := s.ArchiveOrchestrator(context.Background(), orch.ID)
	if err == nil {
		t.Fatalf("expected a summary error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("summary error should wrap the role failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), "w-broken") {
		t.Fatalf("summary error should name the failed role, got: %v", err)
	}
	// All three tasks attempted — no abort-on-first-error.
	if len(argus.archiveCalls) != 3 {
		t.Fatalf("argus archive calls = %v, want all 3 tasks attempted", argus.archiveCalls)
	}
	// Siblings archived; failed role's hera flip happened before the argus
	// call (ArchiveRole flips hera first), but the orchestrator must stay
	// active so retry reaches the remainder.
	for _, id := range []int64{r1.ID, r3.ID} {
		got, _ := db.GetRoleByID(context.Background(), id)
		if !got.Archived {
			t.Fatalf("sibling role %d should be archived despite the failure", id)
		}
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if gotOrch.Archived {
		t.Fatalf("orchestrator must stay active when a role failed (retryable)")
	}
}

func TestArchiveOrchestrator_RetryAfterPartialFailureCompletes(t *testing.T) {
	// After a partial failure, a second `a` must archive only the
	// remainder and finish the orchestrator.
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("old", false)
	r1 := db.seedRole(orch.ID, "w-ok", KindWorker, "old", false)
	r2 := db.seedRole(orch.ID, "w-flaky", KindWorker, "old", false)
	db.seedBinding(r1.ID, "T1", "/tmp/w1")
	db.seedBinding(r2.ID, "T2", "/tmp/w2")
	argus.archiveErrByTask = map[string]error{"T2": errors.New("argus: connection refused")}

	if err := s.ArchiveOrchestrator(context.Background(), orch.ID); err == nil {
		t.Fatalf("expected a summary error on the first attempt")
	}

	// argus recovers.
	argus.mu.Lock()
	argus.archiveErrByTask = nil
	argus.mu.Unlock()

	if err := s.ArchiveOrchestrator(context.Background(), orch.ID); err != nil {
		t.Fatalf("retry should succeed, got: %v", err)
	}
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if !gotOrch.Archived {
		t.Fatalf("orchestrator should be archived after the retry")
	}
}

func TestArchiveRole_Non404ArgusErrorStillPropagates(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false)
	db.seedBinding(role.ID, "T1", "/tmp/wt1")
	argus.archiveErr = errors.New("argus: HTTP 500: boom")

	if err := s.ArchiveRole(context.Background(), role.ID); err == nil {
		t.Fatalf("non-404 argus errors must still surface")
	}
	_ = db
}

func TestStepTaskStatus_PrunedTaskFriendlyError(t *testing.T) {
	// Stepping a pruned task stays an error (you cannot step a
	// nonexistent task) but the message must say so plainly, not dump the
	// raw HTTP 404.
	s, _, argus, _, _ := newTestService()
	argus.getStatusErr = gone()

	_, err := s.StepTaskStatus(context.Background(), "PRUNED", true)
	if err == nil {
		t.Fatalf("expected an error stepping a pruned task")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("error should state the task no longer exists in argus, got: %v", err)
	}
	if strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error should not surface the raw HTTP 404, got: %v", err)
	}
}
