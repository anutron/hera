package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// statusOrder is argus's workflow ladder. `s` advances toward complete; `S`
// reverts toward pending. Mirrors argus's model.Status ordering.
var statusOrder = []string{"pending", "in_progress", "in_review", "complete"}

// statusIndex returns the position of status in the ladder, or -1 when the
// status is unknown.
func statusIndex(status string) int {
	for i, s := range statusOrder {
		if s == status {
			return i
		}
	}
	return -1
}

// NextStatus returns the next status one rung toward complete. Exported for
// the view layer's optimistic-status overlay (BUG-032). Mirrors nextStatus.
func NextStatus(status string) string { return nextStatus(status) }

// PrevStatus returns the previous status one rung toward pending. Exported
// for the view layer's optimistic-status overlay (BUG-032). Mirrors prevStatus.
func PrevStatus(status string) string { return prevStatus(status) }

// nextStatus / prevStatus clamp at the ends of the ladder (complete stays
// complete; pending stays pending) — matching argus's Status.Next/Prev.
func nextStatus(status string) string {
	i := statusIndex(status)
	if i < 0 || i >= len(statusOrder)-1 {
		if i < 0 {
			return statusOrder[0]
		}
		return statusOrder[len(statusOrder)-1]
	}
	return statusOrder[i+1]
}

func prevStatus(status string) string {
	i := statusIndex(status)
	if i <= 0 {
		return statusOrder[0]
	}
	return statusOrder[i-1]
}

// AdvanceStatus implements `s`: read the role's bound argus task status and
// step it one rung toward complete (pending → in_progress → in_review →
// complete), clamping at complete. Returns the resolved status. No-op (returns
// the current status) when already complete. Status stepping is independent of
// archive state: the task is resolved via the live binding when one exists,
// falling back to the role's most recent (ended) binding — archiving ends the
// binding while keeping its argus task id. Errors only when no binding ever
// recorded an argus task (nothing to step).
func (s *Service) AdvanceStatus(ctx context.Context, roleID int64) (string, error) {
	return s.stepStatus(ctx, roleID, true)
}

// RevertStatus implements `S`: step the bound argus task status one rung
// toward pending, clamping at pending.
func (s *Service) RevertStatus(ctx context.Context, roleID int64) (string, error) {
	return s.stepStatus(ctx, roleID, false)
}

func (s *Service) stepStatus(ctx context.Context, roleID int64, advance bool) (string, error) {
	bnd, err := s.resolveBinding(ctx, roleID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("ops.stepStatus: role %d has no argus task recorded", roleID)
		}
		return "", fmt.Errorf("ops.stepStatus: binding lookup for role %d: %w", roleID, err)
	}
	if bnd.ArgusTaskID == "" {
		return "", fmt.Errorf("ops.stepStatus: role %d has no argus task recorded", roleID)
	}
	return s.StepTaskStatus(ctx, bnd.ArgusTaskID, advance)
}

// MarkRoleDone updates the hera role's thread_status to "done" and mirrors
// the value to argus task_meta (best-effort, like hera_status MCP). It does
// NOT advance the argus workflow status — this is the `s`→done→confirm-no
// path: the operator chose to mark the hera role done without flipping the
// argus task to :checked:.
func (s *Service) MarkRoleDone(ctx context.Context, roleID int64) error {
	if err := s.DB.UpsertRoleStatus(ctx, roleID, "done"); err != nil {
		return fmt.Errorf("ops.MarkRoleDone: persist status: %w", err)
	}
	// Mirror to argus task meta — best-effort; skip silently on any error
	// (same contract as the hera_status MCP handler).
	bnd, err := s.resolveBinding(ctx, roleID)
	if err == nil && bnd.ArgusTaskID != "" {
		_ = s.Argus.PutTaskMeta(ctx, bnd.ArgusTaskID, "thread_status", "done")
	}
	return nil
}

// CompleteRole sets the argus task status directly to "complete" for the given
// role, bypassing the get-then-advance logic of AdvanceStatus. Backs the
// BUG-048 y-path in hera-view: the operator confirmed "yes" to ":checked: in
// argus?" so we unconditionally target "complete" rather than stepping one rung
// from whatever the current argus status happens to be. Unlike AdvanceStatus,
// this never silently advances to an intermediate rung (e.g. in_review) if the
// argus status was already behind the cache's optimistic view.
func (s *Service) CompleteRole(ctx context.Context, roleID int64) error {
	bnd, err := s.resolveBinding(ctx, roleID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("ops.CompleteRole: role %d has no argus task recorded", roleID)
		}
		return fmt.Errorf("ops.CompleteRole: binding lookup for role %d: %w", roleID, err)
	}
	if bnd.ArgusTaskID == "" {
		return fmt.Errorf("ops.CompleteRole: role %d has no argus task recorded", roleID)
	}
	_, err = s.Argus.SetTaskStatus(ctx, bnd.ArgusTaskID, "complete")
	if err != nil {
		if errors.Is(err, ErrArgusTaskGone) {
			return fmt.Errorf("ops.CompleteRole: task %s no longer exists in argus (pruned)", bnd.ArgusTaskID)
		}
		return fmt.Errorf("ops.CompleteRole: set status %s -> complete: %w", bnd.ArgusTaskID, err)
	}
	return nil
}

// CompleteTaskByID sets the argus task status directly to "complete" by task
// ID, bypassing hera-binding lookup. Backs the BUG-048 y-path for freelance
// rows (unmanaged argus tasks with no hera role or binding).
func (s *Service) CompleteTaskByID(ctx context.Context, taskID string) error {
	if taskID == "" {
		return fmt.Errorf("ops.CompleteTaskByID: empty argus task id")
	}
	_, err := s.Argus.SetTaskStatus(ctx, taskID, "complete")
	if err != nil {
		if errors.Is(err, ErrArgusTaskGone) {
			return fmt.Errorf("ops.CompleteTaskByID: task %s no longer exists in argus (pruned)", taskID)
		}
		return fmt.Errorf("ops.CompleteTaskByID: set status %s -> complete: %w", taskID, err)
	}
	return nil
}

// PruneSummary reports the outcome of a CompleteArchivedDescendants sweep.
// Pruned is the number of roles removed from hera's DB (and therefore the
// rail). WorktreeSkipped counts roles whose worktree could not be removed
// (already gone, detached, or otherwise) but which were pruned from the DB
// anyway — disk cleanup is best-effort and never blocks clearing the rail.
type PruneSummary struct {
	Pruned          int
	WorktreeSkipped int
}

// CompleteArchivedDescendants marks every archived non-coordinator role under
// the given orchestrator as :checked: in argus AND prunes each from hera's
// DB + disk. Backs the `C` rail key. The coordinator role itself is skipped.
//
// The sweep is resilient (BUG-018): a single worktree that can't be removed
// (already deleted by an earlier cleanup, detached from its git admin entry,
// etc.) must NOT abort the batch. Worktree removal is best-effort — failures
// are logged and counted, the DB role row is deleted regardless, and the sweep
// continues. The returned error is reserved for genuine failures (binding
// lookup, argus status writes, DB deletes) that leave a role in the rail;
// worktree skips are surfaced via the summary, not the error.
func (s *Service) CompleteArchivedDescendants(ctx context.Context, orchID int64) (PruneSummary, error) {
	roles, err := s.DB.ListRolesByOrchestratorInclusive(ctx, orchID)
	if err != nil {
		return PruneSummary{}, fmt.Errorf("ops.CompleteArchivedDescendants: list roles: %w", err)
	}
	var summary PruneSummary
	var errMsgs []string
	for _, role := range roles {
		if !role.Archived {
			continue
		}
		if role.Kind == KindCoordinator {
			continue
		}
		bnd, err := s.resolveBinding(ctx, role.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			errMsgs = append(errMsgs, fmt.Sprintf("role %q (%d): %v", role.Name, role.ID, err))
			continue
		}
		if bnd != nil && bnd.ArgusTaskID != "" {
			if _, err := s.Argus.SetTaskStatus(ctx, bnd.ArgusTaskID, "complete"); err != nil {
				if !errors.Is(err, ErrArgusTaskGone) {
					errMsgs = append(errMsgs, fmt.Sprintf("role %q (%d): set complete: %v", role.Name, role.ID, err))
					continue
				}
				// Pruned argus task: still prune the hera row below.
			}
		}
		// Prune the hera role row + worktree after completing. Worktree
		// removal is best-effort: a stale/missing worktree must never block
		// clearing the role from the rail. Log + count the skip and still
		// delete the DB row below.
		worktreePath := ""
		if bnd != nil {
			worktreePath = bnd.WorktreePath
		}
		if err := s.removeWorktree(ctx, worktreePath); err != nil {
			s.logf("prune: worktree remove failed for role %q (%d), pruning DB row anyway: %v", role.Name, role.ID, err)
			summary.WorktreeSkipped++
		}
		if err := s.DB.DeleteRoleByID(ctx, role.ID); err != nil && !errors.Is(err, ErrNotFound) {
			errMsgs = append(errMsgs, fmt.Sprintf("role %q (%d): delete role: %v", role.Name, role.ID, err))
			continue
		}
		summary.Pruned++
	}
	if len(errMsgs) > 0 {
		return summary, fmt.Errorf("ops.CompleteArchivedDescendants: %d error(s): %s",
			len(errMsgs), strings.Join(errMsgs, "; "))
	}
	return summary, nil
}

// StepTaskStatus steps an argus task's status directly by task id, bypassing
// the hera-binding lookup. Backs `s`/`S` against a freelance row — an
// unmanaged argus task with no hera role or binding to resolve — where the
// rail already knows the task id. advance=true steps toward complete;
// advance=false toward pending; both clamp at the ends (no argus write when
// already clamped). Returns the resolved status.
func (s *Service) StepTaskStatus(ctx context.Context, taskID string, advance bool) (string, error) {
	if taskID == "" {
		return "", fmt.Errorf("ops.StepTaskStatus: empty argus task id")
	}

	cur, err := s.Argus.GetTaskStatus(ctx, taskID)
	if err != nil {
		// A pruned task stays an error — you cannot step a task that
		// does not exist — but the operator gets the plain story, not
		// a raw HTTP 404 dump.
		if errors.Is(err, ErrArgusTaskGone) {
			return "", fmt.Errorf("ops.StepTaskStatus: task %s no longer exists in argus (pruned) — nothing to step", taskID)
		}
		return "", fmt.Errorf("ops.StepTaskStatus: get status %s: %w", taskID, err)
	}

	var target string
	if advance {
		target = nextStatus(cur)
	} else {
		target = prevStatus(cur)
	}

	// Already at the clamp — skip the write so an idempotent `s` on a
	// complete task (or `S` on pending) makes no argus call.
	if target == cur {
		return cur, nil
	}

	resolved, err := s.Argus.SetTaskStatus(ctx, taskID, target)
	if err != nil {
		if errors.Is(err, ErrArgusTaskGone) {
			return "", fmt.Errorf("ops.StepTaskStatus: task %s no longer exists in argus (pruned) — nothing to step", taskID)
		}
		return "", fmt.Errorf("ops.StepTaskStatus: set status %s -> %s: %w", taskID, target, err)
	}
	return resolved, nil
}
