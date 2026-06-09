package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// makeSimpleTempDir creates a temp directory with a stub .git file so
// removeWorktree passes the path through to WorktreeRemover.Remove.
// removeWorktree skips paths that are missing OR that have no .git file
// (already cleaned up), so both conditions must be satisfied for the fake
// remover to receive the call.
func makeSimpleTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: ../main/.git/worktrees/linked\n"), 0o644); err != nil {
		t.Fatalf("makeSimpleTempDir: write .git: %v", err)
	}
	return dir
}

func TestListCompletedAgents_OnlyCompleted(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	done := db.seedRole(orch.ID, "done-1", KindWorker, "foo", false)
	working := db.seedRole(orch.ID, "working-1", KindWorker, "foo", false)
	pending := db.seedRole(orch.ID, "pending-1", KindWorker, "foo", false)
	db.seedBinding(done.ID, "Tdone", "")
	db.seedBinding(working.ID, "Twork", "")
	db.seedBinding(pending.ID, "Tpend", "")

	a := &fakeArgus{statuses: map[string]string{
		"Tdone": "complete",
		"Twork": "in_progress",
		"Tpend": "pending",
	}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.ListCompletedAgents(context.Background())
	if err != nil {
		t.Fatalf("ListCompletedAgents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 completed agent, got %d: %+v", len(got), got)
	}
	if got[0].ArgusTaskID != "Tdone" || got[0].Name != "done-1" {
		t.Fatalf("completed agent = %+v, want Tdone/done-1", got[0])
	}
}

func TestPruneCompleted_DestroysOnlyGivenAgents(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("foo", false)
	done := db.seedRole(orch.ID, "done-1", KindWorker, "foo", false)
	working := db.seedRole(orch.ID, "working-1", KindWorker, "foo", false)
	db.seedBinding(done.ID, "Tdone", "")
	wbnd := db.seedBinding(working.ID, "Twork", "")

	a := &fakeArgus{statuses: map[string]string{"Tdone": "complete", "Twork": "in_progress"}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	completed, err := s.ListCompletedAgents(context.Background())
	if err != nil {
		t.Fatalf("ListCompletedAgents: %v", err)
	}
	n, err := s.PruneCompleted(context.Background(), completed)
	if err != nil {
		t.Fatalf("PruneCompleted: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1", n)
	}
	// Only the completed task destroyed.
	if len(a.deleteCalls) != 1 || a.deleteCalls[0] != "Tdone" {
		t.Fatalf("argus DeleteTask calls = %v, want [Tdone]", a.deleteCalls)
	}
	// Completed role archived; binding ended.
	doneRole, _ := db.GetRoleByID(context.Background(), done.ID)
	if !doneRole.Archived {
		t.Fatalf("completed role should be archived")
	}
	// Working agent untouched: still live, not archived.
	workRole, _ := db.GetRoleByID(context.Background(), working.ID)
	if workRole.Archived {
		t.Fatalf("working role must NOT be archived")
	}
	if _, err := db.GetLiveBindingByRole(context.Background(), working.ID); err != nil {
		t.Fatalf("working binding %d should still be live", wbnd.ID)
	}
}

func TestPruneCompleted_EmptyList_NoDestruction(t *testing.T) {
	db := newFakeDB()
	a := &fakeArgus{}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	n, err := s.PruneCompleted(context.Background(), nil)
	if err != nil {
		t.Fatalf("PruneCompleted(nil): %v", err)
	}
	if n != 0 || len(a.deleteCalls) != 0 {
		t.Fatalf("empty prune must destroy nothing; n=%d calls=%v", n, a.deleteCalls)
	}
}

// Spec (live-coord-never-complete): ListCompletedAgents MUST NOT include
// coordinator roles even when their argus task reports "complete". A sibling
// worker with a complete task MUST still be listed (guard is coord-specific).
// --- PruneArchivedRole ---

func TestPruneArchivedRole_RemovesRowAndWorktree(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	// Use a real directory so removeWorktree passes the path to WorktreeRemover.
	wtPath := makeSimpleTempDir(t)
	db.seedEndedBinding(role.ID, "T1", wtPath)

	if err := s.PruneArchivedRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PruneArchivedRole: %v", err)
	}
	// Role physically deleted from DB.
	if _, err := db.GetRoleByID(context.Background(), role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role should be gone from DB after prune, got err=%v", err)
	}
	// Worktree removal attempted with the binding's path.
	if len(wt.calls) != 1 || wt.calls[0] != wtPath {
		t.Fatalf("worktree remove calls = %v, want [%s]", wt.calls, wtPath)
	}
}

func TestPruneArchivedRole_NoBinding_SkipsWorktree(t *testing.T) {
	s, db, _, wt, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", true)
	// No binding seeded.

	if err := s.PruneArchivedRole(context.Background(), role.ID); err != nil {
		t.Fatalf("PruneArchivedRole with no binding: %v", err)
	}
	if _, err := db.GetRoleByID(context.Background(), role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role should be gone from DB")
	}
	if len(wt.calls) != 0 {
		t.Fatalf("no worktree should be attempted when binding is absent, got calls=%v", wt.calls)
	}
}

func TestPruneArchivedRole_NotArchived_ReturnsError(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	role := db.seedRole(orch.ID, "w1", KindWorker, "foo", false) // NOT archived

	err := s.PruneArchivedRole(context.Background(), role.ID)
	if err == nil {
		t.Fatalf("expected error pruning a non-archived role")
	}
}

func TestPruneArchivedRole_NotFound_ReturnsErrNotFound(t *testing.T) {
	s, _, _, _, _ := newTestService()

	err := s.PruneArchivedRole(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown role, got: %v", err)
	}
}

// --- CompleteArchivedDescendants with prune ---

func TestCompleteArchivedDescendants_CompletesAndPrunesArchivedWorkers(t *testing.T) {
	s, db, argus, wt, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "proj", true)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "proj", true)
	w2 := db.seedRole(orch.ID, "w2", KindWorker, "proj", false) // still active
	w1Path := makeSimpleTempDir(t)
	db.seedEndedBinding(coord.ID, "Tcoord", "")
	db.seedEndedBinding(w1.ID, "Tw1", w1Path)
	db.seedBinding(w2.ID, "Tw2", "")

	n, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if n != 1 {
		t.Fatalf("completed+pruned = %d, want 1", n)
	}
	// w1 completed in argus.
	if len(argus.setStatusCalls) != 1 || argus.setStatusCalls[0].TaskID != "Tw1" || argus.setStatusCalls[0].Status != "complete" {
		t.Fatalf("SetTaskStatus calls = %+v, want Tw1→complete", argus.setStatusCalls)
	}
	// w1 physically deleted.
	if _, err := db.GetRoleByID(context.Background(), w1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("w1 should be gone from DB after prune")
	}
	// Worktree removed for w1 only (coord and w2 use empty paths → skipped).
	if len(wt.calls) != 1 || wt.calls[0] != w1Path {
		t.Fatalf("worktree calls = %v, want [%s]", wt.calls, w1Path)
	}
	// Coord and w2 untouched.
	if _, err := db.GetRoleByID(context.Background(), coord.ID); err != nil {
		t.Fatalf("coord should still exist")
	}
	if _, err := db.GetRoleByID(context.Background(), w2.ID); err != nil {
		t.Fatalf("w2 (active) should still exist")
	}
}

func TestCompleteArchivedDescendants_PrunedArgusTask_StillPrunesHera(t *testing.T) {
	// If the argus task is already pruned (404), the hera row + worktree
	// should still be cleaned up.
	s, db, argus, wt, _ := newTestService()
	orch := db.seedOrchestrator("proj", false)
	w1 := db.seedRole(orch.ID, "w1", KindWorker, "proj", true)
	prunedPath := makeSimpleTempDir(t)
	db.seedEndedBinding(w1.ID, "PRUNED", prunedPath)
	// SetTaskStatus returns ErrArgusTaskGone for the pruned task.
	argus.setStatusErr = fmt.Errorf("argus task gone: %w", ErrArgusTaskGone)

	n, err := s.CompleteArchivedDescendants(context.Background(), orch.ID)
	if err != nil {
		t.Fatalf("CompleteArchivedDescendants: %v", err)
	}
	if n != 1 {
		t.Fatalf("n = %d, want 1 (hera row pruned even though argus task was gone)", n)
	}
	if _, err := db.GetRoleByID(context.Background(), w1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("w1 should be gone from DB even when argus task was pruned")
	}
	if len(wt.calls) != 1 {
		t.Fatalf("worktree should still be removed, calls=%v", wt.calls)
	}
}

func TestListCompletedAgents_SkipsCoordinatorRoles(t *testing.T) {
	db := newFakeDB()
	orch := db.seedOrchestrator("live-proj", false)
	coord := db.seedRole(orch.ID, "live-proj-coord", KindCoordinator, "live-proj", false)
	worker := db.seedRole(orch.ID, "done-worker", KindWorker, "live-proj", false)
	db.seedBinding(coord.ID, "Tcoord", "")
	db.seedBinding(worker.ID, "Tworker", "")

	a := &fakeArgus{statuses: map[string]string{
		"Tcoord":  "complete",
		"Tworker": "complete",
	}}
	s := NewService(db, a, &fakeWorktreeRemover{}, &fakeLogger{})

	got, err := s.ListCompletedAgents(context.Background())
	if err != nil {
		t.Fatalf("ListCompletedAgents: %v", err)
	}
	for _, ag := range got {
		if ag.ArgusTaskID == "Tcoord" {
			t.Fatalf("coordinator role must NOT appear in prune list; got %+v", ag)
		}
	}
	found := false
	for _, ag := range got {
		if ag.ArgusTaskID == "Tworker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("worker role with complete task must appear in prune list; got %v", got)
	}
}
