package ops

import (
	"context"
	"strings"
	"testing"
)

func TestResurrectOrchestrator_HappyPath(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", true)

	got, err := s.ResurrectOrchestrator(context.Background(), coord.ID)
	if err != nil {
		t.Fatalf("ResurrectOrchestrator: %v", err)
	}
	if got == nil || got.ID == "" {
		t.Fatalf("expected created task, got %+v", got)
	}
	// orchestrator + coord unarchived
	gotOrch, _ := db.GetOrchestratorByID(context.Background(), orch.ID)
	if gotOrch.Archived {
		t.Fatalf("orchestrator should be unarchived")
	}
	gotRole, _ := db.GetRoleByID(context.Background(), coord.ID)
	if gotRole.Archived {
		t.Fatalf("coord role should be unarchived")
	}
	// argus task spawned in role's argus_project with hera_join prompt
	if len(argus.createCalls) != 1 {
		t.Fatalf("expected 1 CreateTask call, got %d", len(argus.createCalls))
	}
	req := argus.createCalls[0]
	if req.Project != "foo-frontend" {
		t.Fatalf("Project = %q, want %q", req.Project, "foo-frontend")
	}
	if !strings.Contains(req.Prompt, "hera_join(cwd=$PWD)") {
		t.Fatalf("prompt missing hera_join call: %q", req.Prompt)
	}
}

func TestResurrectOrchestrator_RejectsNonCoord(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	worker := db.seedRole(orch.ID, "w1", KindWorker, "foo-worker", true)

	_, err := s.ResurrectOrchestrator(context.Background(), worker.ID)
	if v := asValidation(err); v == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestResurrectOrchestrator_RejectsActiveRole(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "foo-frontend", false)

	_, err := s.ResurrectOrchestrator(context.Background(), coord.ID)
	if v := asValidation(err); v == nil {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestResurrectOrchestrator_RejectsEmptyArgusProject(t *testing.T) {
	s, db, _, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", true)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "", true)

	_, err := s.ResurrectOrchestrator(context.Background(), coord.ID)
	if err == nil {
		t.Fatalf("expected error")
	}
	// Should NOT be a ValidationError — this is an internal-consistency
	// bug, not user-correctable input.
	if asValidation(err) != nil {
		t.Fatalf("empty argus_project is a system error, not validation: %v", err)
	}
}
