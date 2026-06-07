package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

func decodeNewOrchOutput(t *testing.T, r Response) NewOrchestratorOutput {
	t.Helper()
	if r.IsError {
		t.Fatalf("got error response: %s", r.Content[0].Text)
	}
	var out NewOrchestratorOutput
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestNewOrchestrator_HappyPath(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "t-coord", Name: "coord", Project: "hera", WorktreePath: "/tmp/coord"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/tmp/coord", Name: "foo", CoordinatorRoleName: "coord",
		Prompt: "build foo",
	}))
	out := decodeNewOrchOutput(t, resp)
	if out.Orchestrator != "foo" {
		t.Fatalf("orchestrator = %q", out.Orchestrator)
	}
	if out.RoleName != "coord" {
		t.Fatalf("role_name = %q", out.RoleName)
	}
	if out.Kind != "coordinator" {
		t.Fatalf("kind = %q", out.Kind)
	}
	if !out.Created {
		t.Fatalf("expected Created=true on first-creation")
	}

	// Confirm DB rows exist.
	orch, err := e.db.Orchestrators.GetByName(ctx, "foo")
	if err != nil {
		t.Fatalf("orchestrator lookup: %v", err)
	}
	role, err := e.db.Roles.GetByOrchestratorAndName(ctx, orch.ID, "coord")
	if err != nil {
		t.Fatalf("role lookup: %v", err)
	}
	if role.Kind != db.KindCoordinator {
		t.Fatalf("role.Kind = %s", role.Kind)
	}
	if role.Prompt != "build foo" {
		t.Fatalf("prompt = %q", role.Prompt)
	}
	bnd, err := e.db.Bindings.GetLiveByTaskID(ctx, "t-coord")
	if err != nil {
		t.Fatalf("binding lookup: %v", err)
	}
	if bnd.RoleID != role.ID {
		t.Fatalf("binding.RoleID mismatch")
	}

	// Meta mirror happened.
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	found := false
	for _, p := range e.fake.metaPuts {
		if p.taskID == "t-coord" && p.key == "role" && p.value == "coordinator" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected meta:hera.role=coordinator PUT, got %+v", e.fake.metaPuts)
	}
}

func TestNewOrchestrator_RequiredArgs(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "t-1", Project: "p", WorktreePath: "/tmp/x"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	// Missing name
	if r := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{Cwd: "/tmp/x", CoordinatorRoleName: "c"})); !r.IsError {
		t.Fatalf("expected error for missing name")
	}
	// Missing coordinator_role_name
	if r := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{Cwd: "/tmp/x", Name: "foo"})); !r.IsError {
		t.Fatalf("expected error for missing coordinator_role_name")
	}
	// Missing cwd
	if r := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{Name: "foo", CoordinatorRoleName: "c"})); !r.IsError {
		t.Fatalf("expected error for missing cwd")
	}
}

func TestNewOrchestrator_RejectsAlreadyBoundToSameOrchestrator(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	// Pre-seed a binding for this task to orchestrator "foo".
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "x", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-bound", WorktreePath: "/tmp/bound",
	})
	e.fake.addTask(argus.Task{ID: "t-bound", Project: "p", WorktreePath: "/tmp/bound"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/tmp/bound", Name: "foo", CoordinatorRoleName: "coord",
	}))
	if !resp.IsError {
		t.Fatalf("expected error: task already bound to orchestrator foo")
	}
	if !strings.Contains(resp.Content[0].Text, "already bound to orchestrator") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestNewOrchestrator_AllowsBootstrapWhenBoundToDifferentOrchestrator(t *testing.T) {
	// Multi-binding: a task already bound to orchestrator A may still
	// bootstrap a new orchestrator B.
	ctx := context.Background()
	e := setupHandlers(t)
	orchA, _ := e.db.Orchestrators.Create(ctx, "A")
	roleA, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orchA.ID, Name: "worker", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: roleA.ID, ArgusTaskID: "t-multi", WorktreePath: "/tmp/multi",
	})
	e.fake.addTask(argus.Task{ID: "t-multi", Project: "p", WorktreePath: "/tmp/multi"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/tmp/multi", Name: "B", CoordinatorRoleName: "coord",
	}))
	if resp.IsError {
		t.Fatalf("expected success bootstrapping B while bound to A; got error: %q", resp.Content[0].Text)
	}
	// Verify both bindings now live for the same task.
	got, err := e.db.Bindings.ListLiveByTaskID(ctx, "t-multi")
	if err != nil {
		t.Fatalf("ListLiveByTaskID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 live bindings for t-multi, got %d", len(got))
	}
}

func TestNewOrchestrator_IdempotentOnExistingOrchestratorWithoutLiveCoordinator(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	// Pre-seed: orchestrator "foo" exists with a coordinator role that has no live binding.
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	e.fake.addTask(argus.Task{ID: "t-resume", Project: "p", WorktreePath: "/tmp/resume"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/tmp/resume", Name: "foo", CoordinatorRoleName: "coord",
	}))
	out := decodeNewOrchOutput(t, resp)
	if out.Created {
		t.Fatalf("expected Created=false on pre-existing orchestrator")
	}
	if out.RoleName != "coord" || out.Kind != "coordinator" {
		t.Fatalf("out = %+v", out)
	}
}

func TestNewOrchestrator_RejectsLiveCoordinatorElsewhere(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	// Existing live binding on a different argus task.
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t-elsewhere", WorktreePath: "/tmp/elsewhere",
	})
	// Caller is in a fresh task.
	e.fake.addTask(argus.Task{ID: "t-new", Project: "p", WorktreePath: "/tmp/new"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/tmp/new", Name: "foo", CoordinatorRoleName: "coord",
	}))
	if !resp.IsError {
		t.Fatalf("expected error for live coordinator elsewhere")
	}
	if !strings.Contains(resp.Content[0].Text, "already bound to a live argus task") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestNewOrchestrator_RejectsConflictingRoleKind(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	// Pre-seed a worker role with the name we want to use for coordinator.
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindWorker, ArgusProject: "p",
	})
	e.fake.addTask(argus.Task{ID: "t-1", Project: "p", WorktreePath: "/tmp/x"})

	h := NewNewOrchestratorHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, NewOrchestratorInput{
		Cwd: "/tmp/x", Name: "foo", CoordinatorRoleName: "coord",
	}))
	if !resp.IsError {
		t.Fatalf("expected error for conflicting kind")
	}
	if !strings.Contains(resp.Content[0].Text, "kind") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}

func TestJoin_RejectsKindCoordinator(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "t-1", Project: "p", WorktreePath: "/tmp/x"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/tmp/x", Orchestrator: "foo", RoleName: "c", Kind: "coordinator",
	}))
	if !resp.IsError {
		t.Fatalf("expected error: kind=coordinator should be rejected by hera_join")
	}
	if !strings.Contains(resp.Content[0].Text, "hera_new_orchestrator") {
		t.Fatalf("error should direct to hera_new_orchestrator: %q", resp.Content[0].Text)
	}
}

func TestJoin_RejectsAttachWhenAlreadyBoundToSameOrchestrator(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	// Seed: task is already bound to a worker role under orchestrator "foo".
	foo, _ := e.db.Orchestrators.Create(ctx, "foo")
	worker, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: foo.ID, Name: "x", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-bound", WorktreePath: "/tmp/bound",
	})
	e.fake.addTask(argus.Task{ID: "t-bound", Project: "p", WorktreePath: "/tmp/bound"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/tmp/bound", Orchestrator: "foo", RoleName: "scout", Kind: "freelance",
	}))
	if !resp.IsError {
		t.Fatalf("expected error: task is already bound to foo, attach should reject")
	}
	if !strings.Contains(resp.Content[0].Text, "already bound to orchestrator") {
		t.Fatalf("error wording should say 'already bound to orchestrator', got: %q", resp.Content[0].Text)
	}
}

func TestJoin_AllowsAttachWhenBoundToDifferentOrchestrator(t *testing.T) {
	// Multi-binding: a task already bound to orchestrator A may attach
	// as a worker/freelance under orchestrator B.
	ctx := context.Background()
	e := setupHandlers(t)
	other, _ := e.db.Orchestrators.Create(ctx, "A")
	otherRole, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: other.ID, Name: "x", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: otherRole.ID, ArgusTaskID: "t-multi", WorktreePath: "/tmp/multi",
	})
	foo, _ := e.db.Orchestrators.Create(ctx, "B")
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: foo.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	e.fake.addTask(argus.Task{ID: "t-multi", Project: "p", WorktreePath: "/tmp/multi"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/tmp/multi", Orchestrator: "B", RoleName: "scout", Kind: "freelance",
	}))
	if resp.IsError {
		t.Fatalf("expected success attaching to B while bound to A; got: %q", resp.Content[0].Text)
	}
	got, err := e.db.Bindings.ListLiveByTaskID(ctx, "t-multi")
	if err != nil {
		t.Fatalf("ListLiveByTaskID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 live bindings, got %d", len(got))
	}
}

func TestJoin_FreelanceAttach_MirrorsRoleMeta(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "foo")
	_, _ = e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	e.fake.addTask(argus.Task{ID: "t-free", Project: "p", WorktreePath: "/tmp/free"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/tmp/free", Orchestrator: "foo", RoleName: "scout", Kind: "freelance",
	}))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	// Meta mirror happened on the freelance attach.
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	found := false
	for _, p := range e.fake.metaPuts {
		if p.taskID == "t-free" && p.key == "role" && p.value == "freelance" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected meta:hera.role=freelance PUT on freelance attach; got %+v", e.fake.metaPuts)
	}
}
