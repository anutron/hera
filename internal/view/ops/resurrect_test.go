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

// --- ResurrectRole (BUG-028) ---

// A worker role whose worktree is gone (it still carries a stale LIVE binding to
// the dead instance) resurrects: a fresh argus task is created in the role's
// stored project, the stale binding is ended, a NEW binding ties the fresh task
// to the SAME role id, and the prompt is auto-submitted. Role identity (id, name,
// prompt) is preserved — no new role is created.
func TestResurrectRole_Worker_HappyPath(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "fresh-task", Name: "build-thing"}
	argus.getTaskResp = &TaskDetails{ID: "fresh-task", WorktreePath: "/wt/fresh"}
	orch := db.seedOrchestrator("sherlock-mvp", false)
	db.seedRole(orch.ID, "coord", KindCoordinator, "sherlock-mvp", false)
	worker := db.seedRole(orch.ID, "build-thing", KindWorker, "sherlock-mvp", false)
	worker.Prompt = "build the thing" // override the seed default
	staleBinding := db.seedBinding(worker.ID, "dead-task", "/wt/gone")

	res, err := s.ResurrectRole(context.Background(), worker.ID)
	if err != nil {
		t.Fatalf("ResurrectRole: %v", err)
	}
	if res == nil || res.RoleID != worker.ID || res.ArgusTaskID != "fresh-task" {
		t.Fatalf("result = %+v; want RoleID=%d ArgusTaskID=fresh-task", res, worker.ID)
	}

	// Fresh argus task created in the role's project, named after the role.
	if len(argus.createCalls) != 1 {
		t.Fatalf("want 1 CreateTask; got %d", len(argus.createCalls))
	}
	req := argus.createCalls[0]
	if req.Project != "sherlock-mvp" {
		t.Fatalf("Project = %q; want sherlock-mvp", req.Project)
	}
	if req.Name != "build-thing" {
		t.Fatalf("Name = %q; want build-thing (role name)", req.Name)
	}
	if req.Meta["role"] != "worker" {
		t.Fatalf("Meta[role] = %q; want worker", req.Meta["role"])
	}
	// Prompt carries the role's stored prompt AND the worker orientation naming
	// the coordinator.
	if !strings.Contains(req.Prompt, "build the thing") {
		t.Fatalf("prompt missing stored role prompt: %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, `coordinator "coord"`) {
		t.Fatalf("prompt missing coordinator orientation: %q", req.Prompt)
	}

	// Stale binding ended.
	if len(db.endBindingCalls) != 1 || db.endBindingCalls[0].BindingID != staleBinding.ID {
		t.Fatalf("want stale binding %d ended; got %v", staleBinding.ID, db.endBindingCalls)
	}

	// The role's live binding now points at the fresh task.
	live, err := db.GetLiveBindingByRole(context.Background(), worker.ID)
	if err != nil {
		t.Fatalf("expected a live binding after resurrect: %v", err)
	}
	if live.ArgusTaskID != "fresh-task" {
		t.Fatalf("live binding task = %q; want fresh-task", live.ArgusTaskID)
	}
	if live.WorktreePath != "/wt/fresh" {
		t.Fatalf("live binding worktree = %q; want /wt/fresh", live.WorktreePath)
	}

	// Prompt auto-submitted via CR.
	if len(argus.postInputCalls) != 1 || string(argus.postInputCalls[0].Bytes) != "\r" {
		t.Fatalf("want one CR PostTaskInput; got %v", argus.postInputCalls)
	}

	// Role identity preserved: same id, name, kind, prompt — no new role.
	got, err := db.GetRoleByID(context.Background(), worker.ID)
	if err != nil {
		t.Fatalf("role disappeared: %v", err)
	}
	if got.ID != worker.ID || got.Name != "build-thing" || got.Kind != KindWorker || got.Prompt != "build the thing" {
		t.Fatalf("role identity changed: %+v", got)
	}
}

// A coordinator role resurrects the same way and gets the coordinator
// orientation (naming the orchestrator). The new binding ties the fresh task to
// the SAME coord role id, so the coordinator comes live in its existing place.
func TestResurrectRole_Coordinator_HappyPath(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "coord-fresh", Name: "2a-team"}
	orch := db.seedOrchestrator("2a-team", false)
	coord := db.seedRole(orch.ID, "coord", KindCoordinator, "2a-frontend", false)
	coord.Prompt = "coordinate the 2a team"
	db.seedBinding(coord.ID, "dead-coord-task", "/wt/coord-gone")

	res, err := s.ResurrectRole(context.Background(), coord.ID)
	if err != nil {
		t.Fatalf("ResurrectRole: %v", err)
	}
	if res.RoleID != coord.ID || res.ArgusTaskID != "coord-fresh" {
		t.Fatalf("result = %+v", res)
	}

	req := argus.createCalls[0]
	if req.Project != "2a-frontend" {
		t.Fatalf("Project = %q; want 2a-frontend", req.Project)
	}
	if req.Meta["role"] != "coordinator" {
		t.Fatalf("Meta[role] = %q; want coordinator", req.Meta["role"])
	}
	// Coordinator orientation names the orchestrator and carries the stored prompt.
	if !strings.Contains(req.Prompt, "2a-team") || !strings.Contains(req.Prompt, "coordinate the 2a team") {
		t.Fatalf("prompt missing coordinator orientation or stored prompt: %q", req.Prompt)
	}

	live, err := db.GetLiveBindingByRole(context.Background(), coord.ID)
	if err != nil {
		t.Fatalf("expected a live binding: %v", err)
	}
	if live.ArgusTaskID != "coord-fresh" || live.OrchestratorID != orch.ID {
		t.Fatalf("live binding = %+v; want fresh task under orch %d", live, orch.ID)
	}
}

// ResurrectRole on a role with an empty argus_project is a system error (the
// project is write-once; an empty value is an internal-consistency bug), NOT a
// user-correctable validation error.
func TestResurrectRole_RejectsEmptyArgusProject(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	orch := db.seedOrchestrator("foo", false)
	worker := db.seedRole(orch.ID, "w1", KindWorker, "", false)

	_, err := s.ResurrectRole(context.Background(), worker.ID)
	if err == nil {
		t.Fatalf("expected error")
	}
	if asValidation(err) != nil {
		t.Fatalf("empty argus_project is a system error, not validation: %v", err)
	}
	if len(argus.createCalls) != 0 {
		t.Fatalf("must not create a task when the project is empty; got %v", argus.createCalls)
	}
}

// ResurrectRole tolerates a role with no prior binding (the instance was already
// fully torn down): it simply creates the fresh task + binding with no EndBinding.
func TestResurrectRole_NoPriorBinding(t *testing.T) {
	s, db, argus, _, _ := newTestService()
	argus.createResp = &CreatedTask{ID: "fresh", Name: "w1"}
	orch := db.seedOrchestrator("foo", false)
	worker := db.seedRole(orch.ID, "w1", KindWorker, "foo-app", false)

	if _, err := s.ResurrectRole(context.Background(), worker.ID); err != nil {
		t.Fatalf("ResurrectRole: %v", err)
	}
	if len(db.endBindingCalls) != 0 {
		t.Fatalf("no prior binding to end; got %v", db.endBindingCalls)
	}
	live, err := db.GetLiveBindingByRole(context.Background(), worker.ID)
	if err != nil || live.ArgusTaskID != "fresh" {
		t.Fatalf("expected fresh live binding; got %+v err=%v", live, err)
	}
}
