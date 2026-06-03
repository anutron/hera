package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ArchiveRole handles the explicit ARCHIVE verb against a role: set the
// role's hera archived_at AND POST argus archive on the bound argus task.
// The caller (the view) decides the direction from the row's
// EFFECTIVE rendered state — never from a single backing flag — so a row
// that DISPLAYS as active always archives and one that displays as
// archived always unarchives, even when the hera and argus flags disagree
// (mixed state from historical asymmetric toggles).
//
// The worktree is NOT touched (per spec — archive preserves the
// worktree; delete is the destructive verb).
//
// The binding resolves live-first with the latest-binding fallback
// (symmetric with unarchive): a role whose binding was ended by a
// previous archive (end_reason='argus_archived') keeps its argus_task_id
// but misses the live-only lookup, and skipping there would stamp hera's
// archived_at while silently leaving the argus task active — recreating
// the mixed state. The argus call is skipped only when NO binding ever
// recorded a task id (nothing to archive on the argus side).
//
// The fallback carries a SHARED-TASK GUARD (archive direction only): a
// task id resolved via an ENDED binding may ALSO be the live-bound task
// of a DIFFERENT role (multi-binding history, reused sessions), and
// archiving through the stale binding would yank the task out from under
// the active role — the cascade-collateral hazard that archived an
// operator's live coord. When any OTHER role holds a live binding to the
// resolved task, the argus-side archive is skipped (logged with both
// role names); the hera-side role archive above stands. A task resolved
// via the role's OWN live binding archives unconditionally — that live
// binding IS the ownership claim.
func (s *Service) ArchiveRole(ctx context.Context, id int64) error {
	if err := s.DB.ArchiveRole(ctx, id); err != nil {
		return fmt.Errorf("ops.ArchiveRole: archive: %w", err)
	}

	// Resolve live-first, tracking WHICH lookup hit: the guard applies
	// only to the ended fallback, so the resolution can't go through
	// resolveBinding (which erases that distinction).
	bnd, err := s.DB.GetLiveBindingByRole(ctx, id)
	ownLive := err == nil
	if errors.Is(err, ErrNotFound) {
		bnd, err = s.DB.GetLatestBindingByRole(ctx, id)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("ops.ArchiveRole: binding lookup: %w", err)
	}
	if bnd == nil || bnd.ArgusTaskID == "" {
		return nil
	}
	if !ownLive {
		live, err := s.DB.ListLiveBindingsByTask(ctx, bnd.ArgusTaskID)
		if err != nil {
			return fmt.Errorf("ops.ArchiveRole: shared-task guard lookup %s: %w", bnd.ArgusTaskID, err)
		}
		for _, other := range live {
			if other.RoleID == id {
				continue
			}
			s.logf("archive: argus task %q (resolved via ended binding of role %q) is live-bound to role %q — skipping argus-side archive",
				bnd.ArgusTaskID, s.roleName(ctx, id), s.roleName(ctx, other.RoleID))
			return nil
		}
	}
	if err := s.Argus.ArchiveTask(ctx, bnd.ArgusTaskID); err != nil {
		if !errors.Is(err, ErrArgusTaskGone) {
			return fmt.Errorf("ops.ArchiveRole: argus archive %s: %w", bnd.ArgusTaskID, err)
		}
		// Pruned task: argus deleted it outright, so there is
		// nothing to archive argus-side. The hera flip above
		// stands and the operation succeeds — aborting here is
		// what used to strand orchestrator cascades on exactly
		// the old rows that most need cleaning.
		s.logf("archive: argus task %q pruned — skipping argus side: %v", bnd.ArgusTaskID, err)
	}
	return nil
}

// roleName resolves a role's display name for log lines, degrading to
// "role <id>" when the lookup fails — logging must never abort an op.
func (s *Service) roleName(ctx context.Context, id int64) string {
	r, err := s.DB.GetRoleByID(ctx, id)
	if err != nil || r == nil {
		return fmt.Sprintf("role %d", id)
	}
	return r.Name
}

// UnarchiveRole handles the explicit UNARCHIVE verb against a role: clear
// the role's hera archived_at AND POST argus unarchive on the bound task.
// Both sides clear unconditionally — on a mixed-state row (hera-active +
// argus-archived) the hera clear is a harmless no-op and the argus clear
// is the one that visibly returns the row; re-deriving the direction from
// role.Archived here would re-archive exactly that row.
func (s *Service) UnarchiveRole(ctx context.Context, id int64) error {
	if err := s.DB.UnarchiveRole(ctx, id); err != nil {
		return fmt.Errorf("ops.UnarchiveRole: unarchive: %w", err)
	}
	return s.unarchiveBoundArgusTask(ctx, id, "UnarchiveRole")
}

// ToggleArchiveRole keeps the legacy flag-derived toggle as a thin wrapper
// over the explicit verbs, dispatching on the role's HERA flag alone.
// Callers that can see the row's effective rendered state (the view's `a`
// handler) MUST call ArchiveRole/UnarchiveRole directly instead — on a
// mixed-flag row this wrapper picks the direction OPPOSITE to what the
// operator sees.
func (s *Service) ToggleArchiveRole(ctx context.Context, id int64) error {
	role, err := s.DB.GetRoleByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ToggleArchiveRole: load %d: %w", id, err)
	}
	if role.Archived {
		return s.UnarchiveRole(ctx, id)
	}
	return s.ArchiveRole(ctx, id)
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
			if !errors.Is(err, ErrArgusTaskGone) {
				return fmt.Errorf("ops.%s: argus unarchive %s: %w", op, bnd.ArgusTaskID, err)
			}
			// Pruned task: nothing to unarchive argus-side; the
			// hera-side clear has already happened, so the row still
			// visibly leaves the Archive expando.
			s.logf("%s: argus task %q pruned — skipping argus side: %v", op, bnd.ArgusTaskID, err)
		}
	}
	return nil
}

// ToggleArchiveTask handles `a` against a freelance row — an unmanaged argus
// task with no hera role or binding — by addressing the argus task directly:
// POST archive when active, POST unarchive when archived. No hera DB row is
// touched (there is none). archived is the task's current argus archived
// state, supplied by the caller from the rail's argus state cache (the ops
// layer has no task-state getter of its own) — so this toggle's direction
// already follows the effective rendered state.
//
// No shared-task guard here: a freelance row only renders when NO live
// binding exists for its task (the rail's freelancer exclusion (a)), so
// this path cannot address a task that a role is live-bound to — the
// guard belongs to ArchiveRole's ended-binding fallback, where a stale
// binding CAN point at another role's live task.
func (s *Service) ToggleArchiveTask(ctx context.Context, taskID string, archived bool) error {
	if taskID == "" {
		return fmt.Errorf("ops.ToggleArchiveTask: empty argus task id")
	}
	if archived {
		if err := s.Argus.UnarchiveTask(ctx, taskID); err != nil {
			if !errors.Is(err, ErrArgusTaskGone) {
				return fmt.Errorf("ops.ToggleArchiveTask: argus unarchive %s: %w", taskID, err)
			}
			s.logf("toggle-archive: argus task %q pruned — nothing to unarchive: %v", taskID, err)
		}
		return nil
	}
	if err := s.Argus.ArchiveTask(ctx, taskID); err != nil {
		if !errors.Is(err, ErrArgusTaskGone) {
			return fmt.Errorf("ops.ToggleArchiveTask: argus archive %s: %w", taskID, err)
		}
		s.logf("toggle-archive: argus task %q pruned — nothing to archive: %v", taskID, err)
	}
	return nil
}

// ArchiveOrchestrator handles the explicit ARCHIVE verb against an
// orchestrator: archive the orchestrator AND cascade-archive every active
// role under it (each role archive also POSTs argus archive on the role's
// resolved binding's argus task — live preferred, latest fallback, via
// the role-level ArchiveRole).
//
// The cascade is failure-tolerant: a per-role failure (argus flaky for one
// call) does NOT abort it — the remaining roles are still attempted and the
// failures are aggregated into one summary error naming the roles that
// remain. The orchestrator itself is archived ONLY when every role
// succeeded; on partial failure it stays active so the operator's retry
// reaches the remainder (already-archived roles are skipped, making the
// retry idempotent). Pruned tasks never count as failures — ArchiveRole
// treats an argus 404 as a skip — so old orchestrators full of pruned
// tasks archive cleanly.
func (s *Service) ArchiveOrchestrator(ctx context.Context, id int64) error {
	// Cascade archive: every active role first, then the orchestrator.
	// Roles go first so that if argus is flaky the orchestrator row
	// stays active and the operator can retry cleanly.
	roles, err := s.DB.ListRolesByOrchestrator(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ArchiveOrchestrator: list roles: %w", err)
	}
	var failed []string
	var errs []error
	for _, role := range roles {
		if role.Archived {
			continue
		}
		if err := s.ArchiveRole(ctx, role.ID); err != nil {
			failed = append(failed, fmt.Sprintf("%s (role %d)", role.Name, role.ID))
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("ops.ArchiveOrchestrator: %d of %d role(s) failed to archive — orchestrator left active, retry `a` to archive the remainder: %s: %w",
			len(errs), len(roles), strings.Join(failed, ", "), errors.Join(errs...))
	}

	if err := s.DB.ArchiveOrchestrator(ctx, id); err != nil {
		return fmt.Errorf("ops.ArchiveOrchestrator: archive orchestrator: %w", err)
	}
	return nil
}

// UnarchiveOrchestrator handles the explicit UNARCHIVE verb against an
// orchestrator: clear the orchestrator's archived_at AND unarchive its
// coord role's bound argus task (the coord task is the orchestrator's
// argus face, so the row visibly returns). Roles do not auto-unarchive
// (per hera-coordination delta spec). Operators unarchive individual
// roles via `a` against the role row.
func (s *Service) UnarchiveOrchestrator(ctx context.Context, id int64) error {
	if err := s.DB.UnarchiveOrchestrator(ctx, id); err != nil {
		return fmt.Errorf("ops.UnarchiveOrchestrator: unarchive: %w", err)
	}
	// The inclusive list is required — the coord role is archived
	// at this point, so the default (active-only) list misses it.
	roles, err := s.DB.ListRolesByOrchestratorInclusive(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.UnarchiveOrchestrator: list roles: %w", err)
	}
	for _, role := range roles {
		if role.Kind != KindCoordinator {
			continue
		}
		return s.unarchiveBoundArgusTask(ctx, role.ID, "UnarchiveOrchestrator")
	}
	return nil
}

// ToggleArchiveOrchestrator keeps the legacy flag-derived toggle as a thin
// wrapper over the explicit verbs, dispatching on the orchestrator's
// archived flag (which IS the orchestrator's effective rendered state —
// unlike roles, an orchestrator header displays archived from this single
// flag). The view's `a` handler still calls the explicit verbs so the
// dispatch decision lives where the rendered state is known.
func (s *Service) ToggleArchiveOrchestrator(ctx context.Context, id int64) error {
	orch, err := s.DB.GetOrchestratorByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.ToggleArchiveOrchestrator: load %d: %w", id, err)
	}
	if orch.Archived {
		return s.UnarchiveOrchestrator(ctx, id)
	}
	return s.ArchiveOrchestrator(ctx, id)
}
