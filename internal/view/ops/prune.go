package ops

import (
	"context"
	"errors"
	"fmt"
)

// CompletedAgent identifies one managed agent eligible for `^r` prune: a live
// hera binding whose argus task reports status=complete. Name + ArgusTaskID
// feed the confirmation modal so the operator sees exactly what disappears.
type CompletedAgent struct {
	RoleID      int64
	Name        string
	ArgusTaskID string
}

// ListCompletedAgents returns every managed agent whose bound argus task is
// in the completed state, fleet-wide. Backs the `^r` confirmation list (the
// bridge shows these before any destruction). It inspects each live binding's
// argus status; bindings whose status can't be read are skipped (logged) so a
// single flaky task doesn't block the prune.
//
// Mirrors argus's prune-completed semantics, but scoped to hera-managed
// tasks. (argus's own POST /api/maintenance/prune-completed is master-gated
// and rejects hera's scope token, so hera prunes its managed set itself via
// per-task DeleteTask, which cleans each worktree + branch server-side.)
func (s *Service) ListCompletedAgents(ctx context.Context) ([]CompletedAgent, error) {
	bindings, err := s.DB.ListLiveBindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("ops.ListCompletedAgents: list live bindings: %w", err)
	}

	var out []CompletedAgent
	for _, bnd := range bindings {
		if bnd.ArgusTaskID == "" {
			continue
		}
		status, err := s.Argus.GetTaskStatus(ctx, bnd.ArgusTaskID)
		if err != nil {
			s.logf("prune: skipping %q — status read failed: %v", bnd.ArgusTaskID, err)
			continue
		}
		if status != "complete" {
			continue
		}
		name := bnd.ArgusTaskID
		if role, rerr := s.DB.GetRoleByID(ctx, bnd.RoleID); rerr == nil && role != nil {
			name = role.Name
		}
		out = append(out, CompletedAgent{
			RoleID:      bnd.RoleID,
			Name:        name,
			ArgusTaskID: bnd.ArgusTaskID,
		})
	}
	return out, nil
}

// PruneCompleted destroys the given completed agents: for each, it ends the
// role's live binding (end_reason=user_deleted), archives the role, and
// destroys the argus task (which cleans its worktree + branch server-side).
// The caller passes the agents returned by ListCompletedAgents after operator
// confirmation, so PruneCompleted never decides on its own what to remove.
//
// Returns the count pruned. A per-agent error stops the sweep and is returned
// wrapped — already-pruned agents stay pruned (no rollback), matching the
// delete-cascade policy.
func (s *Service) PruneCompleted(ctx context.Context, agents []CompletedAgent) (int, error) {
	pruned := 0
	for _, ag := range agents {
		role, err := s.DB.GetRoleByID(ctx, ag.RoleID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// Role vanished between list and prune — nothing to do.
				continue
			}
			return pruned, fmt.Errorf("ops.PruneCompleted: load role %d: %w", ag.RoleID, err)
		}
		if err := s.deleteRoleInternal(ctx, role); err != nil {
			return pruned, fmt.Errorf("ops.PruneCompleted: prune role %d (%s): %w", ag.RoleID, ag.Name, err)
		}
		pruned++
	}
	return pruned, nil
}
