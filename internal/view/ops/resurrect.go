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
