package ops

import (
	"context"
	"testing"
)

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
