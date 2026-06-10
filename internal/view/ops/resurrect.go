package ops

import (
	"context"
	"fmt"
)

// ResurrectOrchestrator handles Enter against an archived coord row
// when the Archive section is visible. It:
//  1. Verifies the selected role is a coordinator and is currently
//     archived.
//  2. Clears archived_at on the orchestrator and the coord role.
//  3. Spawns a fresh argus task in the role's stored argus_project with
//     a prompt invoking hera_join(cwd=$PWD). The new task's worktree is
//     brand new; the role's prompt is inherited when the rebinding
//     hera_join resolves to the dormant role.
//
// argusProject MUST be non-empty on the role row — it's a write-once
// field captured at first creation. A role with an empty argus_project
// is an internal-consistency bug; ResurrectOrchestrator surfaces it as
// an error rather than guessing a project.
func (s *Service) ResurrectOrchestrator(ctx context.Context, coordRoleID int64) (*CreatedTask, error) {
	role, err := s.DB.GetRoleByID(ctx, coordRoleID)
	if err != nil {
		return nil, fmt.Errorf("ops.ResurrectOrchestrator: load role %d: %w", coordRoleID, err)
	}
	if role.Kind != KindCoordinator {
		return nil, validation(fmt.Sprintf("role %d is not a coordinator", coordRoleID))
	}
	if !role.Archived {
		return nil, validation("orchestrator is not archived")
	}
	if role.ArgusProject == "" {
		return nil, fmt.Errorf("ops.ResurrectOrchestrator: role %d has empty argus_project", coordRoleID)
	}

	// Unarchive: orchestrator first, then the coord role. Order matches
	// the rail's read order so a midway crash leaves a consistent view
	// (orchestrator active, coord still archived = visible but
	// unbindable, which the UI can flag).
	if err := s.DB.UnarchiveOrchestrator(ctx, role.OrchestratorID); err != nil {
		return nil, fmt.Errorf("ops.ResurrectOrchestrator: unarchive orchestrator %d: %w", role.OrchestratorID, err)
	}
	if err := s.DB.UnarchiveRole(ctx, role.ID); err != nil {
		return nil, fmt.Errorf("ops.ResurrectOrchestrator: unarchive role %d: %w", role.ID, err)
	}

	req := CreateTaskRequest{
		Project: role.ArgusProject,
		Name:    role.ArgusProject + "-coord",
		Prompt:  buildResurrectPrompt(role.ArgusProject),
	}
	task, err := s.Argus.CreateTask(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ops.ResurrectOrchestrator: argus CreateTask: %w", err)
	}
	return task, nil
}

// buildResurrectPrompt returns the prompt for a resurrect-spawned argus
// task. The prompt's first action is a bare hera_join(cwd=$PWD); hera's
// existing rebind logic (hera-coordination delta spec, scenario "Bare
// hera_join in archived role's argus_project resurrects") resolves the
// cwd to the dormant role's binding-slot and inherits the role's prompt.
//
// Project is included in the prose for the human operator's benefit
// when they peek into the new task's PTY; the MCP call ignores it.
func buildResurrectPrompt(argusProject string) string {
	return fmt.Sprintf(
		`You are reclaiming the dormant coordinator role for hera project %q. As your first action call: hera_join(cwd=$PWD)`,
		argusProject,
	)
}

// ResurrectRole mints a FRESH argus instance for an existing hera role whose
// previous instance (argus task + worktree + session) is gone — typically
// because the operator pruned argus :check: worktrees to reclaim disk, leaving
// the role dormant and uninteractable (BUG-028).
//
// It differs from the two neighbouring recovery paths:
//   - ReattachAgent (BUG-033): `claude --resume <session>` — works only while
//     the worktree still exists.
//   - ResurrectOrchestrator: an LLM-driven rebind that spawns a task whose first
//     action is hera_join(cwd=$PWD); the AGENT reclaims the dormant role.
//
// ResurrectRole is fully PROGRAMMATIC and born-bound (the SpawnWorker /
// NewOrchestrator pattern): it creates a fresh argus task in the role's stored
// project, ends the role's stale live binding (the dead instance's binding,
// never ended because the worktree was pruned out-of-band), and inserts a NEW
// binding tying the fresh task to the SAME role id. Role identity — id, name,
// prompt, orchestrator — is preserved; NO new role is created. The new session
// is seeded with the role's stored (verbatim) prompt re-wrapped in the
// kind-appropriate orientation, then auto-submitted via a CR so it runs without
// a manual Enter.
//
// Works for both workers and coordinators. A resurrected coordinator role comes
// live in its existing place in the tree because its OrchestratorID is unchanged.
//
// argusProject MUST be non-empty on the role row — it is a write-once field
// captured at first creation. A role with an empty argus_project is an
// internal-consistency bug ResurrectRole surfaces as an error rather than
// guessing a project. Partial-failure tolerance mirrors SpawnWorker: a failed
// GetTask soft-degrades to an empty worktree path; an EndBinding failure is
// logged but never aborts the resurrection.
func (s *Service) ResurrectRole(ctx context.Context, roleID int64) (*ResurrectRoleResult, error) {
	role, err := s.DB.GetRoleByID(ctx, roleID)
	if err != nil {
		return nil, fmt.Errorf("ops.ResurrectRole: load role %d: %w", roleID, err)
	}
	if role.ArgusProject == "" {
		return nil, fmt.Errorf("ops.ResurrectRole: role %d has empty argus_project", roleID)
	}

	// Re-wrap the role's stored prompt in the kind-appropriate orientation so
	// the new instance behaves like the original spawn would have. The role's
	// Prompt holds ONLY the operator's verbatim instructions; the orientation
	// (worker-under-coordinator vs coordinator) is reconstructed here.
	taskPrompt, roleMeta := s.resurrectPrompt(ctx, role)

	// Create the fresh argus task in the role's stored project. Name it after
	// the role so argus titles the task correctly (not after the orientation
	// preamble that leads the prompt body).
	created, err := s.Argus.CreateTask(ctx, CreateTaskRequest{
		Project: role.ArgusProject,
		Name:    role.Name,
		Prompt:  taskPrompt,
		Meta:    map[string]string{"role": roleMeta},
	})
	if err != nil {
		return nil, fmt.Errorf("ops.ResurrectRole: argus CreateTask: %w", err)
	}

	// Resolve the new worktree path. Soft-degrade on failure: the binding is
	// still inserted (possibly with an empty path) so the resurrection is not
	// lost — argus creates the worktree synchronously inside POST /api/tasks.
	worktreePath := ""
	if gt, gtErr := s.Argus.GetTask(ctx, created.ID); gtErr != nil {
		s.Logger.Printf("ops.ResurrectRole: GetTask(%s) failed — binding will have empty worktree_path: %v", created.ID, gtErr)
	} else if gt != nil {
		worktreePath = gt.WorktreePath
	}

	// End the role's stale live binding — the dead instance's binding, which was
	// never ended because the worktree vanished out-of-band. Without this the
	// role would carry TWO live bindings and the rail's pane binding would be
	// ambiguous. Best-effort: a missing binding (ErrNotFound) is fine.
	if old, gbErr := s.DB.GetLiveBindingByRole(ctx, roleID); gbErr == nil && old != nil {
		if err := s.DB.EndBinding(ctx, old.ID, "resurrected"); err != nil {
			s.Logger.Printf("ops.ResurrectRole: EndBinding(%d) failed — role may briefly show two live bindings: %v", old.ID, err)
		}
	}

	// Insert the NEW binding tying the fresh argus task to the EXISTING role.
	// Orphan-tolerance mirrors SpawnWorker: a failure here leaves the argus task
	// running unbound (visible as a freelancer) but is NOT rolled back.
	if _, err := s.DB.CreateBinding(ctx, CreateBindingInput{
		RoleID:         role.ID,
		OrchestratorID: role.OrchestratorID,
		ArgusTaskID:    created.ID,
		WorktreePath:   worktreePath,
	}); err != nil {
		s.Logger.Printf("ops.ResurrectRole: CreateBinding failed for role %d / argus task %s — partial state: %v", role.ID, created.ID, err)
		return nil, fmt.Errorf("ops.ResurrectRole: insert binding: %w", err)
	}

	// Auto-submit the prompt via CR so the new session starts without a manual
	// Enter (reuses the BUG-030 fix; mirrors hera_spawn_worker). Best-effort.
	if _, inputErr := s.Argus.PostTaskInput(ctx, created.ID, []byte("\r")); inputErr != nil {
		s.Logger.Printf("ops.ResurrectRole: PostTaskInput CR failed for %s — prompt not auto-submitted: %v", created.ID, inputErr)
	}

	return &ResurrectRoleResult{RoleID: role.ID, ArgusTaskID: created.ID}, nil
}

// resurrectPrompt re-wraps a role's stored (verbatim) prompt in the orientation
// the role's original spawn would have applied, and returns the meta:role value
// for the new argus task. A coordinator gets the coordinator orientation
// (orchestrator name resolved best-effort, falling back to the role name); a
// worker gets the worker orientation naming its coordinator (resolved
// best-effort from the orchestrator's coordinator role, empty when absent).
func (s *Service) resurrectPrompt(ctx context.Context, role *Role) (prompt, roleMeta string) {
	if role.Kind == KindCoordinator {
		orchName := role.Name
		if orch, err := s.DB.GetOrchestratorByID(ctx, role.OrchestratorID); err == nil && orch != nil {
			orchName = orch.Name
		}
		return buildCoordPrompt(orchName, role.Prompt), string(KindCoordinator)
	}
	return buildWorkerPrompt(s.coordNameFor(ctx, role.OrchestratorID), role.Prompt), string(KindWorker)
}

// coordNameFor returns the name of the coordinator role under orchID, or "" when
// the lookup fails or no coordinator role exists. Used to name the coordinator
// in a resurrected worker's orientation suffix.
func (s *Service) coordNameFor(ctx context.Context, orchID int64) string {
	roles, err := s.DB.ListRolesByOrchestrator(ctx, orchID)
	if err != nil {
		return ""
	}
	for _, r := range roles {
		if r.Kind == KindCoordinator {
			return r.Name
		}
	}
	return ""
}
