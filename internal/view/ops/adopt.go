package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// AdoptInput describes an operator-side adoption: fold the freelancer's
// argus task into an existing orchestrator as a worker. The view supplies
// the argus task id + orchestrator id (resolved from the rail selection),
// a default role name (the freelancer's task name), and the freelancer's
// repo + worktree (carried on the selection).
type AdoptInput struct {
	ArgusTaskID    string
	OrchestratorID int64
	RoleName       string
	ArgusProject   string
	WorktreePath   string
}

// AdoptResult reports what the adoption created.
type AdoptResult struct {
	OrchestratorName string
	RoleName         string
	RoleID           int64
	BindingID        int64
}

// AdoptTaskIntoOrchestrator performs operator-side rail adoption: it creates a
// worker role under the chosen orchestrator and a live binding from the
// freelancer's argus task to that role — the SAME role+binding creation that
// hera_join's attach-mode performs (db.Roles.Create + db.Bindings.Create via
// the ops.DB interface), invoked server-side from the rail instead of by the
// agent.
//
// Differences from hera_join attach-mode, all operator-facing:
//   - The orchestrator is addressed by id (the rail selection carries it),
//     not by name.
//   - The role name is de-collided automatically against the orchestrator's
//     existing ACTIVE roles, so the operator never has to pick a unique name.
//   - A task with ANY live binding is rejected: a real freelancer has none,
//     so a hit is a race or a mislabeled row, and the operator should see
//     "already managed" rather than gain a second binding.
//
// The meta:role=worker stamp is best-effort (a transient argus failure must
// not undo the binding), exactly as in attach-mode.
func (s *Service) AdoptTaskIntoOrchestrator(ctx context.Context, in AdoptInput) (*AdoptResult, error) {
	taskID := strings.TrimSpace(in.ArgusTaskID)
	if taskID == "" {
		return nil, validation("this freelancer has no argus task id to adopt")
	}

	orch, err := s.DB.GetOrchestratorByID(ctx, in.OrchestratorID)
	if errors.Is(err, ErrNotFound) {
		return nil, validation(fmt.Sprintf("orchestrator %d no longer exists", in.OrchestratorID))
	}
	if err != nil {
		return nil, fmt.Errorf("ops.AdoptTaskIntoOrchestrator: load orchestrator %d: %w", in.OrchestratorID, err)
	}

	// Already-bound guard: a freelancer has no live binding anywhere. A hit
	// means a concurrent agent-side hera_join (or a mislabeled row) — reject
	// rather than create a second binding.
	if existing, err := s.DB.ListLiveBindingsByTask(ctx, taskID); err != nil {
		return nil, fmt.Errorf("ops.AdoptTaskIntoOrchestrator: check existing bindings: %w", err)
	} else if len(existing) > 0 {
		return nil, validation("this argus task is already bound to a coordinator — it is not a freelancer")
	}

	name, err := s.uniqueRoleName(ctx, in.OrchestratorID, in.RoleName)
	if err != nil {
		return nil, err
	}

	role, err := s.DB.CreateRole(ctx, CreateRoleInput{
		OrchestratorID: in.OrchestratorID,
		Name:           name,
		Kind:           KindWorker,
		ArgusProject:   strings.TrimSpace(in.ArgusProject),
	})
	if err != nil {
		return nil, fmt.Errorf("ops.AdoptTaskIntoOrchestrator: create role: %w", err)
	}

	bnd, err := s.DB.CreateBinding(ctx, CreateBindingInput{
		RoleID:         role.ID,
		OrchestratorID: in.OrchestratorID,
		ArgusTaskID:    taskID,
		WorktreePath:   strings.TrimSpace(in.WorktreePath),
	})
	if err != nil {
		return nil, fmt.Errorf("ops.AdoptTaskIntoOrchestrator: create binding: %w", err)
	}

	// Mirror meta:hera.role to the adopted task. Best-effort: a transient
	// argus failure must not undo the binding (matches hera_join attach-mode).
	if s.Argus != nil {
		if err := s.Argus.PutTaskMeta(ctx, taskID, "role", string(KindWorker)); err != nil && s.Logger != nil {
			s.Logger.Printf("ops.AdoptTaskIntoOrchestrator: best-effort PutTaskMeta failed for task %s: %v", taskID, err)
		}
	}

	return &AdoptResult{
		OrchestratorName: orch.Name,
		RoleName:         role.Name,
		RoleID:           role.ID,
		BindingID:        bnd.ID,
	}, nil
}

// uniqueRoleName derives a role name that does not collide with an existing
// ACTIVE role under the orchestrator. It starts from the trimmed requested
// name (defaulting to "worker" when empty) and appends -2, -3, … until free.
// Archived siblings do not block (matching db.Roles.Create semantics).
func (s *Service) uniqueRoleName(ctx context.Context, orchID int64, requested string) (string, error) {
	base := strings.TrimSpace(requested)
	if base == "" {
		base = "worker"
	}
	candidate := base
	for i := 2; ; i++ {
		_, err := s.DB.GetRoleByOrchestratorAndName(ctx, orchID, candidate)
		if errors.Is(err, ErrNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("ops.AdoptTaskIntoOrchestrator: de-collide role name: %w", err)
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

// ListActiveOrchestrators returns the active (non-archived) orchestrators for
// the `J` adopt picker. Order follows DB.ListOrchestrators.
func (s *Service) ListActiveOrchestrators(ctx context.Context) ([]*Orchestrator, error) {
	orchs, err := s.DB.ListOrchestrators(ctx)
	if err != nil {
		return nil, fmt.Errorf("ops.ListActiveOrchestrators: %w", err)
	}
	return orchs, nil
}
