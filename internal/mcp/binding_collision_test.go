package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// BUG-059: two argus tasks reuse the same worktree_path across their
// lifecycles (a task name / branch reused after the prior task went to
// in_review / archived without its worktree being cleared). cwd→task
// resolution then keys identity off the WRONG task id, so a born-bound
// worker can neither claim its binding (task-keyed lookup misses) nor attach
// a new one (worktree-keyed uniqueness rejects). These tests pin the
// resolver's disambiguation and the join handler's task-then-worktree
// agreement.

// --- Resolver.TaskForCwd disambiguation -------------------------------------

func TestTaskForCwd_PrefersInProgressOverStaleArchived(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	// Stale task added FIRST — pre-fix "first match wins" returned this one.
	e.fake.addTask(argus.Task{ID: "stale", WorktreePath: "/wt", Status: "in_review", Archived: true})
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	got, err := e.resolver.TaskForCwd(ctx, "/wt")
	if err != nil {
		t.Fatalf("TaskForCwd: %v", err)
	}
	if got.ID != "live" {
		t.Fatalf("resolved task %q, want the in_progress live task", got.ID)
	}
}

func TestTaskForCwd_AmbiguousWhenTwoInProgressShareWorktree(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "a", WorktreePath: "/wt", Status: "in_progress"})
	e.fake.addTask(argus.Task{ID: "b", WorktreePath: "/wt", Status: "in_progress"})

	_, err := e.resolver.TaskForCwd(ctx, "/wt")
	var amb *CwdAmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("want CwdAmbiguousError, got %v", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(amb.Candidates))
	}
}

func TestTaskForCwd_AllArchivedIsUnknown(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "a", WorktreePath: "/wt", Status: "complete", Archived: true})
	e.fake.addTask(argus.Task{ID: "b", WorktreePath: "/wt", Status: "in_review", Archived: true})

	if _, err := e.resolver.TaskForCwd(ctx, "/wt"); !errors.Is(err, ErrCwdUnknown) {
		t.Fatalf("want ErrCwdUnknown, got %v", err)
	}
}

func TestTaskForCwd_SingleMatchUnchanged(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	// No status set — the common case must still resolve.
	e.fake.addTask(argus.Task{ID: "only", WorktreePath: "/wt"})
	got, err := e.resolver.TaskForCwd(ctx, "/wt")
	if err != nil || got.ID != "only" {
		t.Fatalf("single-match resolve: got %+v, err %v", got, err)
	}
}

// --- hera_join claim / attach agreement -------------------------------------

// TestJoinClaim_ResolvesLiveTaskDespiteStaleCollision proves the reported
// symptom is gone: a born-bound worker whose binding is rooted at the LIVE
// task can claim it even though a stale archived task shares the worktree and
// was listed first.
func TestJoinClaim_ResolvesLiveTaskDespiteStaleCollision(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "O")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p", Prompt: "do the thing",
	})
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "live", WorktreePath: "/wt",
	})
	e.fake.addTask(argus.Task{ID: "stale", WorktreePath: "/wt", Status: "in_review", Archived: true})
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/wt", Orchestrator: "O"}))
	out := decodeJoinOutput(t, resp)
	if out.RoleName != "w" || out.Prompt != "do the thing" {
		t.Fatalf("claim did not resolve the live worker: %+v", out)
	}
	if out.ArgusTaskID != "live" {
		t.Fatalf("claimed binding argus_task_id = %q, want live", out.ArgusTaskID)
	}
}

// TestJoinClaim_WorktreeFallbackWhenBindingTaskDrifted isolates the worktree
// fallback: the live binding's own argus_task_id has drifted away from the
// only in_progress task at the worktree, so the task-keyed lookup misses — the
// worktree-keyed fallback still claims it.
func TestJoinClaim_WorktreeFallbackWhenBindingTaskDrifted(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	orch, _ := e.db.Orchestrators.Create(ctx, "O")
	role, _ := e.db.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "p",
	})
	// Binding rooted at a drifted/ghost task id; worktree still /wt.
	_, _ = e.db.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "ghost", WorktreePath: "/wt",
	})
	e.fake.addTask(argus.Task{ID: "live", WorktreePath: "/wt", Status: "in_progress"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/wt", Orchestrator: "O"}))
	out := decodeJoinOutput(t, resp)
	if out.RoleName != "w" {
		t.Fatalf("worktree fallback failed to claim: %+v", out)
	}
}

// TestJoinAttach_CollisionReturnsFriendlyMessage proves attach no longer
// bubbles a raw UNIQUE constraint error: it detects the worktree-keyed live
// binding and returns an actionable message pointing at claim / hera_rebind.
func TestJoinAttach_CollisionReturnsFriendlyMessage(t *testing.T) {
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

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{
		Cwd: "/wt", Orchestrator: "O", RoleName: "w2", Kind: "worker",
	}))
	if !resp.IsError {
		t.Fatalf("expected a friendly rejection, got success: %q", resp.Content[0].Text)
	}
	msg := resp.Content[0].Text
	if strings.Contains(strings.ToUpper(msg), "UNIQUE") {
		t.Fatalf("attach leaked a raw UNIQUE constraint error: %q", msg)
	}
	if !strings.Contains(msg, "already holds a live binding") {
		t.Fatalf("expected worktree-collision hint, got: %q", msg)
	}
	if !strings.Contains(msg, "hera_rebind") {
		t.Fatalf("expected hera_rebind repair hint (task id drifted), got: %q", msg)
	}
}

// TestJoinClaim_AmbiguousCwdSurfaces confirms the resolver's ambiguity refusal
// reaches the caller rather than silently binding to a guessed task.
func TestJoinClaim_AmbiguousCwdSurfaces(t *testing.T) {
	ctx := context.Background()
	e := setupHandlers(t)
	e.fake.addTask(argus.Task{ID: "a", WorktreePath: "/wt", Status: "in_progress"})
	e.fake.addTask(argus.Task{ID: "b", WorktreePath: "/wt", Status: "in_progress"})

	h := NewJoinHandler(e.resolver, e.db, e.client)
	resp := h.Handle(ctx, mustMarshal(t, JoinInput{Cwd: "/wt", Orchestrator: "O"}))
	if !resp.IsError {
		t.Fatalf("expected ambiguity error, got success")
	}
	if !strings.Contains(resp.Content[0].Text, "multiple live argus tasks") {
		t.Fatalf("expected ambiguity hint, got: %q", resp.Content[0].Text)
	}
}
