package ops

import (
	"context"
	"errors"
)

// resolveBinding returns the role's live binding when one exists, FALLING
// BACK to the role's most recent binding regardless of ended_at. Archiving
// a task ENDS its hera binding (end_reason='argus_archived') while keeping
// the argus_task_id, so every archived row fails the live-only lookup even
// though its task is still perfectly addressable — status stepping and
// BOTH archive directions must keep working against rows whose binding
// has ended.
//
// Returns ErrNotFound only when the role has never had a binding at all.
// Callers still need to handle an empty ArgusTaskID on the resolved row.
func (s *Service) resolveBinding(ctx context.Context, roleID int64) (*Binding, error) {
	bnd, err := s.DB.GetLiveBindingByRole(ctx, roleID)
	if err == nil {
		return bnd, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return s.DB.GetLatestBindingByRole(ctx, roleID)
}
