package ops

import (
	"context"
	"errors"
	"testing"
)

// Adopt creates a worker role + live binding under the chosen orchestrator
// for the freelancer's argus task, and best-effort stamps meta:role=worker.
func TestAdoptTaskIntoOrchestrator_CreatesWorkerRoleAndBinding(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("alpha", false)

	res, err := s.AdoptTaskIntoOrchestrator(context.Background(), AdoptInput{
		ArgusTaskID:    "task-free-1",
		OrchestratorID: orch.ID,
		RoleName:       "scout",
		ArgusProject:   "Hera",
		WorktreePath:   "/wt/scout",
	})
	if err != nil {
		t.Fatalf("AdoptTaskIntoOrchestrator: unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("AdoptTaskIntoOrchestrator: nil result")
	}
	if res.OrchestratorName != "alpha" || res.RoleName != "scout" {
		t.Fatalf("result mismatch: orch=%q role=%q", res.OrchestratorName, res.RoleName)
	}

	// A worker role under the orchestrator.
	roles, _ := db.ListRolesByOrchestrator(context.Background(), orch.ID)
	var role *Role
	for _, r := range roles {
		if r.Name == "scout" {
			role = r
		}
	}
	if role == nil {
		t.Fatal("expected a role named 'scout' under the orchestrator")
	}
	if role.Kind != KindWorker {
		t.Fatalf("expected worker kind, got %q", role.Kind)
	}
	if role.ArgusProject != "Hera" {
		t.Fatalf("expected argus_project Hera, got %q", role.ArgusProject)
	}

	// A live binding from the freelancer's task to the new role.
	bnd, err := db.GetLiveBindingByRole(context.Background(), role.ID)
	if err != nil {
		t.Fatalf("expected a live binding for the new role: %v", err)
	}
	if bnd.ArgusTaskID != "task-free-1" {
		t.Fatalf("binding task mismatch: %q", bnd.ArgusTaskID)
	}
	if bnd.WorktreePath != "/wt/scout" {
		t.Fatalf("binding worktree mismatch: %q", bnd.WorktreePath)
	}

	// Best-effort meta stamp role=worker.
	if got := argus.metaFor("task-free-1", "role"); got != "worker" {
		t.Fatalf("expected meta role=worker, got %q", got)
	}
}

// A meta-stamp failure must not undo or fail the binding.
func TestAdoptTaskIntoOrchestrator_MetaFailureDoesNotFailBinding(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("alpha", false)
	argus.putMetaErr = errors.New("argus down")

	res, err := s.AdoptTaskIntoOrchestrator(context.Background(), AdoptInput{
		ArgusTaskID:    "task-free-1",
		OrchestratorID: orch.ID,
		RoleName:       "scout",
	})
	if err != nil {
		t.Fatalf("meta failure must not fail adopt: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if _, err := db.GetLiveBindingByRole(context.Background(), res.RoleID); err != nil {
		t.Fatalf("binding must still exist after meta failure: %v", err)
	}
}

// The default role name is de-collided against an existing active role.
func TestAdoptTaskIntoOrchestrator_DeCollidesRoleName(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("alpha", false)
	db.seedRole(orch.ID, "scout", KindWorker, "Hera", false)

	res, err := s.AdoptTaskIntoOrchestrator(context.Background(), AdoptInput{
		ArgusTaskID:    "task-free-1",
		OrchestratorID: orch.ID,
		RoleName:       "scout",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RoleName == "scout" {
		t.Fatal("expected a de-collided role name, got the colliding 'scout'")
	}
	// The new role is distinct from the seeded one and live-bound.
	if _, err := db.GetLiveBindingByRole(context.Background(), res.RoleID); err != nil {
		t.Fatalf("expected live binding on the new role: %v", err)
	}
}

func TestAdoptTaskIntoOrchestrator_RejectsEmptyTaskID(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("alpha", false)
	_, err := s.AdoptTaskIntoOrchestrator(context.Background(), AdoptInput{
		OrchestratorID: orch.ID,
		RoleName:       "scout",
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error for empty task id, got %v", err)
	}
}

func TestAdoptTaskIntoOrchestrator_RejectsUnknownOrchestrator(t *testing.T) {
	s, _, _, _, _ := newTestService()
	_, err := s.AdoptTaskIntoOrchestrator(context.Background(), AdoptInput{
		ArgusTaskID:    "task-free-1",
		OrchestratorID: 999,
		RoleName:       "scout",
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error for unknown orchestrator, got %v", err)
	}
}

// A task already live-bound somewhere is not a freelancer; reject.
func TestAdoptTaskIntoOrchestrator_RejectsAlreadyBoundTask(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("alpha", false)
	other := db.seedRole(orch.ID, "existing", KindWorker, "Hera", false)
	db.seedBinding(other.ID, "task-free-1", "/wt/existing")

	_, err := s.AdoptTaskIntoOrchestrator(context.Background(), AdoptInput{
		ArgusTaskID:    "task-free-1",
		OrchestratorID: orch.ID,
		RoleName:       "scout",
	})
	if asValidation(err) == nil {
		t.Fatalf("expected validation error for already-bound task, got %v", err)
	}
}

func TestListActiveOrchestrators_ReturnsActiveSet(t *testing.T) {
	s, db, _, _, _ := newTestService()
	db.seedOrchestrator("alpha", false)
	db.seedOrchestrator("beta", true) // archived — excluded
	db.seedOrchestrator("gamma", false)

	orchs, err := s.ListActiveOrchestrators(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := map[string]bool{}
	for _, o := range orchs {
		names[o.Name] = true
	}
	if !names["alpha"] || !names["gamma"] {
		t.Fatalf("expected alpha+gamma, got %v", names)
	}
	if names["beta"] {
		t.Fatalf("archived orchestrator beta should be excluded, got %v", names)
	}
}
