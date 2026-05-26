package ops

import (
	"context"
	"errors"
	"fmt"
)

// ToggleArchiveRole handles `a` against a role: if active, archive it +
// POST argus archive; if archived, unarchive it (argus side is not
// auto-unarchived — that's an operator concern post-resurrect).
//
// The worktree is NOT touched (per spec — archive preserves the
// worktree; delete is the destructive verb).
//
// If the role has no live binding, the argus archive call is skipped
// (nothing to archive on the argus side).
func (s *Service) ToggleArchiveRole(ctx context.Context, id int64) error {
	role, err := s.DB.GetRoleByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ToggleArchiveRole: load %d: %w", id, err)
	}

	if role.Archived {
		if err := s.DB.UnarchiveRole(ctx, id); err != nil {
			return fmt.Errorf("ops.ToggleArchiveRole: unarchive: %w", err)
		}
		return nil
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

// ToggleArchiveOrchestrator handles `a` against an orchestrator:
//   - If active: archive the orchestrator AND cascade-archive every
//     active role under it (each archive call also POSTs argus archive
//     on the role's live binding's argus task).
//   - If archived: unarchive the orchestrator ONLY — roles do not
//     auto-unarchive (per hera-coordination delta spec). Operators
//     unarchive individual roles via `a` against the role row.
func (s *Service) ToggleArchiveOrchestrator(ctx context.Context, id int64) error {
	orch, err := s.DB.GetOrchestratorByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ToggleArchiveOrchestrator: load %d: %w", id, err)
	}

	if orch.Archived {
		// Unarchive ONLY the orchestrator; roles stay archived.
		if err := s.DB.UnarchiveOrchestrator(ctx, id); err != nil {
			return fmt.Errorf("ops.ToggleArchiveOrchestrator: unarchive: %w", err)
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
