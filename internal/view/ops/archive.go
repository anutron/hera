package ops

import (
	"context"
	"errors"
	"fmt"
)

// ToggleArchiveRole handles `a` against a role as a SYMMETRIC toggle:
// if active, archive it + POST argus archive; if archived, unarchive it
// + POST argus unarchive. The rail buckets a row into the Archive
// expando when EITHER side is archived (hera archived_at, argus
// archived, or dead), so a hera-only unarchive of an argus-archived
// task would produce zero visible change.
//
// The worktree is NOT touched (per spec — archive preserves the
// worktree; delete is the destructive verb).
//
// If the role has no live binding, the argus call is skipped in both
// directions (nothing to archive or unarchive on the argus side).
func (s *Service) ToggleArchiveRole(ctx context.Context, id int64) error {
	role, err := s.DB.GetRoleByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ToggleArchiveRole: load %d: %w", id, err)
	}

	if role.Archived {
		if err := s.DB.UnarchiveRole(ctx, id); err != nil {
			return fmt.Errorf("ops.ToggleArchiveRole: unarchive: %w", err)
		}
		return s.unarchiveBoundArgusTask(ctx, id, "ToggleArchiveRole")
	}

	// archive transition
	if err := s.DB.ArchiveRole(ctx, id); err != nil {
		return fmt.Errorf("ops.ToggleArchiveRole: archive: %w", err)
	}

	bnd, err := s.DB.GetLiveBindingByRole(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("ops.ToggleArchiveRole: live binding lookup: %w", err)
	}
	if bnd != nil && bnd.ArgusTaskID != "" {
		if err := s.Argus.ArchiveTask(ctx, bnd.ArgusTaskID); err != nil {
			return fmt.Errorf("ops.ToggleArchiveRole: argus archive %s: %w", bnd.ArgusTaskID, err)
		}
	}
	return nil
}

// unarchiveBoundArgusTask resolves the role's binding — live when one
// exists, otherwise the most recent ended one — and, when it carries an
// argus task id, POSTs argus unarchive. The fallback matters: archiving a
// task ENDS its binding (end_reason='argus_archived'), so EVERY archived
// row misses the live lookup; skipping there would silently defeat the
// symmetric unarchive for exactly the rows that need it (the argus side
// stays archived and the row never visibly leaves the Archive expando).
// A role with no binding at all (ErrNotFound) is a skip, not an error —
// the hera-side unarchive has already happened and there is nothing to
// unarchive on the argus side.
func (s *Service) unarchiveBoundArgusTask(ctx context.Context, roleID int64, op string) error {
	bnd, err := s.resolveBinding(ctx, roleID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("ops.%s: binding lookup: %w", op, err)
	}
	if bnd != nil && bnd.ArgusTaskID != "" {
		if err := s.Argus.UnarchiveTask(ctx, bnd.ArgusTaskID); err != nil {
			return fmt.Errorf("ops.%s: argus unarchive %s: %w", op, bnd.ArgusTaskID, err)
		}
	}
	return nil
}

// ToggleArchiveTask handles `a` against a freelance row — an unmanaged argus
// task with no hera role or binding — by addressing the argus task directly:
// POST archive when active, POST unarchive when archived. No hera DB row is
// touched (there is none). archived is the task's current argus archived
// state, supplied by the caller from the rail's argus state cache (the ops
// layer has no task-state getter of its own).
func (s *Service) ToggleArchiveTask(ctx context.Context, taskID string, archived bool) error {
	if taskID == "" {
		return fmt.Errorf("ops.ToggleArchiveTask: empty argus task id")
	}
	if archived {
		if err := s.Argus.UnarchiveTask(ctx, taskID); err != nil {
			return fmt.Errorf("ops.ToggleArchiveTask: argus unarchive %s: %w", taskID, err)
		}
		return nil
	}
	if err := s.Argus.ArchiveTask(ctx, taskID); err != nil {
		return fmt.Errorf("ops.ToggleArchiveTask: argus archive %s: %w", taskID, err)
	}
	return nil
}

// ToggleArchiveOrchestrator handles `a` against an orchestrator:
//   - If active: archive the orchestrator AND cascade-archive every
//     active role under it (each archive call also POSTs argus archive
//     on the role's live binding's argus task).
//   - If archived: unarchive the orchestrator AND its coord role's
//     bound argus task (symmetric toggle — the coord task is the
//     orchestrator's argus face, so the row visibly returns). Roles do
//     not auto-unarchive (per hera-coordination delta spec). Operators
//     unarchive individual roles via `a` against the role row.
func (s *Service) ToggleArchiveOrchestrator(ctx context.Context, id int64) error {
	orch, err := s.DB.GetOrchestratorByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ToggleArchiveOrchestrator: load %d: %w", id, err)
	}

	if orch.Archived {
		// Unarchive the orchestrator; roles stay archived hera-side.
		if err := s.DB.UnarchiveOrchestrator(ctx, id); err != nil {
			return fmt.Errorf("ops.ToggleArchiveOrchestrator: unarchive: %w", err)
		}
		// The inclusive list is required — the coord role is archived
		// at this point, so the default (active-only) list misses it.
		roles, err := s.DB.ListRolesByOrchestratorInclusive(ctx, id)
		if err != nil {
			return fmt.Errorf("ops.ToggleArchiveOrchestrator: list roles: %w", err)
		}
		for _, role := range roles {
			if role.Kind != KindCoordinator {
				continue
			}
			return s.unarchiveBoundArgusTask(ctx, role.ID, "ToggleArchiveOrchestrator")
		}
		return nil
	}

	// Cascade archive: every active role first, then the orchestrator.
	// Ordering doesn't matter for correctness — the DB rows are
	// independent — but we archive roles first so that if argus is
	// flaky the orchestrator row stays active and the operator can
	// retry cleanly.
	roles, err := s.DB.ListRolesByOrchestrator(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ToggleArchiveOrchestrator: list roles: %w", err)
	}
	for _, role := range roles {
		if role.Archived {
			continue
		}
		if err := s.ToggleArchiveRole(ctx, role.ID); err != nil {
			return fmt.Errorf("ops.ToggleArchiveOrchestrator: cascade role %d: %w", role.ID, err)
		}
	}

	if err := s.DB.ArchiveOrchestrator(ctx, id); err != nil {
		return fmt.Errorf("ops.ToggleArchiveOrchestrator: archive orchestrator: %w", err)
	}
	return nil
}
