package ops

import (
	"context"
	"errors"
	"fmt"
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
