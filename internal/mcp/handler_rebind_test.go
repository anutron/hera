package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

func decodeRebindOutput(t *testing.T, r Response) RebindOutput {
	t.Helper()
	if r.IsError {
		t.Fatalf("got error response: %s", r.Content[0].Text)
	}
	var out RebindOutput
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatalf("decode RebindOutput: %v", err)
	}
	return out
}

// TestRebind_RepairsDriftedBinding is the core repair: a live binding whose
// argus_task_id has drifted to a stale/ghost task is reconciled to the
// caller's real live task. After the repair both lookup paths agree, the old
// binding is ended, and the role (and its messages) survive.
func TestRebind_RepairsDriftedBinding(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "O")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p", Prompt: "ship it",
	})
	coord, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	old, _ := e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "ghost", WorktreePath: "/wt",
	})
	// A message to the worker role — must survive the reconcile (keyed on role_id).
	_, _ = e.db.Messages.Create(ctx, db.CreateMessageInput{
		FromRoleID: coord.ID, ToRoleID: role.ID, Body: "keep going", Tldr: "keep going",
	})
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	h := NewRebindHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, RebindInput{Cwd: "/wt", Orchestrator: "O"}))
	out := decodeRebindOutput(t, resp)

	if !out.Reconciled {
		t.Fatalf("expected reconciled=true, got %+v", out)
	}
	if out.ArgusTaskID != "live" {
		t.Fatalf("reconciled argus_task_id = %q, want live", out.ArgusTaskID)
	}
	if len(out.EndedBindingIDs) != 1 || out.EndedBindingIDs[0] != old.ID {
		t.Fatalf("expected old binding %d ended, got %+v", old.ID, out.EndedBindingIDs)
	}

	// Both lookup paths now resolve the SAME (new) binding — the invariant
	// the bug violated.
	byTask, err := e.db.Bindings.GetLiveByTaskAndOrchestrator(ctx, "live", orch.ID)
	if err != nil {
		t.Fatalf("post-repair task-keyed lookup: %v", err)
	}
	byWt, err := e.db.Bindings.GetLiveByWorktreeAndOrchestrator(ctx, "/wt", orch.ID)
	if err != nil {
		t.Fatalf("post-repair worktree-keyed lookup: %v", err)
	}
	if byTask.ID != out.BindingID || byWt.ID != out.BindingID {
		t.Fatalf("lookups disagree: task=%d worktree=%d out=%d", byTask.ID, byWt.ID, out.BindingID)
	}

	// The role's message survived.
	unread, _ := e.db.Messages.UnreadForRole(ctx, role.ID)
	if len(unread) != 1 {
		t.Fatalf("message lost across reconcile: %d unread, want 1", len(unread))
	}

	// meta:hera.role mirrored to the live task.
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	found := false
	for _, p := range e.fake.metaPuts {
		if p.taskID == "live" && p.value == "worker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected role meta mirrored to live task, got %+v", e.fake.metaPuts)
	}
}

// TestRebind_NoOpWhenAlreadyConsistent: a healthy binding is left untouched.
func TestRebind_NoOpWhenAlreadyConsistent(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "O")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	bnd, _ := e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "live", WorktreePath: "/wt",
	})
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	h := NewRebindHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, RebindInput{Cwd: "/wt", Orchestrator: "O"}))
	out := decodeRebindOutput(t, resp)
	if out.Reconciled {
		t.Fatalf("expected no-op (reconciled=false), got %+v", out)
	}
	if out.BindingID != bnd.ID {
		t.Fatalf("binding id changed on a no-op: got %d, want %d", out.BindingID, bnd.ID)
	}
}

// TestRebind_RoleNameSelectsBinding covers the explicit role_name branch on a
// single-candidate repair.
func TestRebind_RoleNameSelectsBinding(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "O")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "ghost", WorktreePath: "/wt",
	})
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	h := NewRebindHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, RebindInput{Cwd: "/wt", Orchestrator: "O", RoleName: "w"}))
	out := decodeRebindOutput(t, resp)
	if !out.Reconciled || out.RoleName != "w" || out.ArgusTaskID != "live" {
		t.Fatalf("role_name-selected repair failed: %+v", out)
	}
}

// TestRebind_RefusesAmbiguousCwd: two in_progress tasks share the worktree, so
// the caller's identity cannot be determined — refuse.
func TestRebind_RefusesAmbiguousCwd(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "O")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "a", WorktreePath: "/wt",
	})
	e.fake.addTask(argus.Task{ID: "a", WorktreePath: "/wt", Status: "in_progress"})
	e.fake.addTask(argus.Task{ID: "b", WorktreePath: "/wt", Status: "in_progress"})

	h := NewRebindHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, RebindInput{Cwd: "/wt", Orchestrator: "O"}))
	if !resp.IsError {
		t.Fatalf("expected refusal on ambiguous cwd, got success")
	}
	if !strings.Contains(resp.Content[0].Text, "multiple live argus tasks") {
		t.Fatalf("expected ambiguity hint, got: %q", resp.Content[0].Text)
	}
}

// TestRebind_RefusesMultipleRolesWithoutRoleName: the caller's task and its
// worktree are bound to DIFFERENT roles under the same orchestrator — a
// genuinely tangled state that requires an explicit role_name to resolve.
func TestRebind_RefusesMultipleRolesWithoutRoleName(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "O")
	r1, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r1", Kind: db.KindWorker, ArgusProject: "p",
	})
	r2, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "r2", Kind: db.KindWorker, ArgusProject: "p",
	})
	// r1 bound to the caller's task id but a DIFFERENT worktree.
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: r1.ID, OrchestratorID: orch.ID, ArgusTaskID: "live", WorktreePath: "/other",
	})
	// r2 bound to the caller's worktree but a ghost task id.
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: r2.ID, OrchestratorID: orch.ID, ArgusTaskID: "ghost", WorktreePath: "/wt",
	})
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	h := NewRebindHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, RebindInput{Cwd: "/wt", Orchestrator: "O"}))
	if !resp.IsError {
		t.Fatalf("expected refusal on multi-role ambiguity, got success")
	}
	if !strings.Contains(resp.Content[0].Text, "multiple roles") {
		t.Fatalf("expected multi-role hint, got: %q", resp.Content[0].Text)
	}
}

// TestRebind_RefusesWhenNothingToReconcile: no live binding for the
// orchestrator at this worktree/task — hera_rebind repairs, it does not create.
func TestRebind_RefusesWhenNothingToReconcile(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	_, _ = e.db.Orchestrators.Create(ctx, "O")
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	h := NewRebindHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, RebindInput{Cwd: "/wt", Orchestrator: "O"}))
	if !resp.IsError {
		t.Fatalf("expected refusal when nothing to reconcile, got success")
	}
	if !strings.Contains(resp.Content[0].Text, "nothing to reconcile") {
		t.Fatalf("expected nothing-to-reconcile hint, got: %q", resp.Content[0].Text)
	}
}

func TestRebind_UnknownOrchestrator(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})
	h := NewRebindHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, RebindInput{Cwd: "/wt", Orchestrator: "ghost-orch"}))
	if !resp.IsError {
		t.Fatalf("expected error for unknown orchestrator")
	}
	if !strings.Contains(resp.Content[0].Text, "does not exist") {
		t.Fatalf("error wording: %q", resp.Content[0].Text)
	}
}
