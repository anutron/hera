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
// the current status) when already complete. Errors when the role has no live
// binding (nothing to step).
func (s *Service) AdvanceStatus(ctx context.Context, roleID int64) (string, error) {
	return s.stepStatus(ctx, roleID, true)
}

// RevertStatus implements `S`: step the bound argus task status one rung
// toward pending, clamping at pending.
func (s *Service) RevertStatus(ctx context.Context, roleID int64) (string, error) {
	return s.stepStatus(ctx, roleID, false)
}

func (s *Service) stepStatus(ctx context.Context, roleID int64, advance bool) (string, error) {
	bnd, err := s.DB.GetLiveBindingByRole(ctx, roleID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("ops.stepStatus: role %d has no live binding", roleID)
		}
		return "", fmt.Errorf("ops.stepStatus: live binding lookup for role %d: %w", roleID, err)
	}
	if bnd.ArgusTaskID == "" {
		return "", fmt.Errorf("ops.stepStatus: role %d binding has no argus task", roleID)
	}

	cur, err := s.Argus.GetTaskStatus(ctx, bnd.ArgusTaskID)
	if err != nil {
		return "", fmt.Errorf("ops.stepStatus: get status %s: %w", bnd.ArgusTaskID, err)
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

	resolved, err := s.Argus.SetTaskStatus(ctx, bnd.ArgusTaskID, target)
	if err != nil {
		return "", fmt.Errorf("ops.stepStatus: set status %s -> %s: %w", bnd.ArgusTaskID, target, err)
	}
	return resolved, nil
}
