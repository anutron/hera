package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ReparentCoordInput describes an operator-side coordinator re-parent: nest an
// existing coordinator C (its own orchestrator) under another coordinator P as
// a sub-coordinator. The view supplies the child orchestrator id + its
// coordinator argus task (resolved from the rail selection) and the chosen
// parent orchestrator id.
//
// The re-parent linkage is the SAME multi-binding the renderer already nests
// (resolveSubCoordinators): a worker role under P whose binding's argus task is
// C's coordinator task. C's whole subtree (its roles/workers) moves with it
// because the subtree is derived from C, which is untouched — only its parent
// linkage changes.
type ReparentCoordInput struct {
	// ChildOrchestratorID is the orchestrator being re-parented (C).
	ChildOrchestratorID int64
	// CoordTaskID is C's coordinator argus task — the multi-binding key that
	// nests C under its parent.
	CoordTaskID string
	// ParentOrchestratorID is the chosen new parent (P).
	ParentOrchestratorID int64
	// RoleName is the desired name for the worker role created under P
	// (defaults to C's coordinator name); de-collided against P's active roles.
	RoleName string
	// ArgusProject is C's argus repo, recorded on the new worker role
	// (write-once, consistent with adopt). Optional.
	ArgusProject string
}

// ReparentCoordResult reports what the re-parent created.
type ReparentCoordResult struct {
	ParentOrchestratorName string
	ChildOrchestratorName  string
	RoleName               string
	RoleID                 int64
	BindingID              int64
}

// EndReasonReparented marks the old parent linkage binding ended when a
// coordinator is moved to a new parent.
const EndReasonReparented = "reparented"

// ReparentCoordinator nests coordinator C (ChildOrchestratorID, identified by
// its coordinator argus task CoordTaskID) under parent coordinator P
// (ParentOrchestratorID) by creating a worker role under P bound to C's
// coordinator argus task — the multi-binding the rail renders as a nested
// sub-coordinator. If C is already nested under some other parent, that prior
// parent linkage (worker role + binding) is torn down first so the move is
// clean (C never appears under two parents).
//
// Guards:
//   - P must exist and differ from C (a coordinator cannot adopt itself).
//   - P must not be a descendant of C (SubtreeOrchIDs(C) — reusing the BUG-021
//     subtree walk): nesting C under its own descendant would create a cycle.
//   - C must have a LIVE coordinator binding for CoordTaskID (the worktree path
//     for the new binding is derived from it). A dormant/archived coordinator
//     has no live binding to re-parent.
func (s *Service) ReparentCoordinator(ctx context.Context, in ReparentCoordInput) (*ReparentCoordResult, error) {
	taskID := strings.TrimSpace(in.CoordTaskID)
	if taskID == "" {
		return nil, validation("this coordinator has no argus task id to re-parent")
	}
	if in.ParentOrchestratorID == in.ChildOrchestratorID {
		return nil, validation("a coordinator cannot be adopted under itself")
	}

	child, err := s.DB.GetOrchestratorByID(ctx, in.ChildOrchestratorID)
	if errors.Is(err, ErrNotFound) {
		return nil, validation(fmt.Sprintf("coordinator %d no longer exists", in.ChildOrchestratorID))
	}
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: load child %d: %w", in.ChildOrchestratorID, err)
	}

	parent, err := s.DB.GetOrchestratorByID(ctx, in.ParentOrchestratorID)
	if errors.Is(err, ErrNotFound) {
		return nil, validation(fmt.Sprintf("coordinator %d no longer exists", in.ParentOrchestratorID))
	}
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: load parent %d: %w", in.ParentOrchestratorID, err)
	}

	// Cycle guard: the chosen parent must not be the child or any of the
	// child's descendants — otherwise C would be nested under its own subtree.
	subtree, err := s.DB.SubtreeOrchIDs(ctx, in.ChildOrchestratorID)
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: subtree of %d: %w", in.ChildOrchestratorID, err)
	}
	for _, id := range subtree {
		if id == in.ParentOrchestratorID {
			return nil, validation(fmt.Sprintf(
				"cannot adopt %q under %q — %q is one of %q's own sub-coordinators (would create a cycle)",
				child.Name, parent.Name, parent.Name, child.Name,
			))
		}
	}

	// Resolve C's coordinator binding (for the worktree path) and any existing
	// parent linkage to tear down. C's coordinator task may hold multiple live
	// bindings: its coordinator binding in C, plus a worker binding in a current
	// parent if it is already nested.
	live, err := s.DB.ListLiveBindingsByTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: list bindings for %s: %w", taskID, err)
	}
	var coordWorktree string
	var priorParentBindings []*Binding
	for _, bnd := range live {
		if bnd.OrchestratorID == in.ChildOrchestratorID {
			// C's own coordinator binding — source of the worktree path.
			coordWorktree = bnd.WorktreePath
			continue
		}
		// A live binding of C's coord task in any OTHER orchestrator is a parent
		// linkage (C nested under that orchestrator). Tear it down for a clean
		// move. Includes the case where C is already nested under P (the move is
		// then a refresh — end it and recreate so the unique-binding indexes
		// never collide).
		priorParentBindings = append(priorParentBindings, bnd)
	}
	if coordWorktree == "" {
		return nil, validation(fmt.Sprintf(
			"%q has no live coordinator binding to re-parent — resurrect it first", child.Name,
		))
	}

	// Tear down each prior parent linkage: end the binding and delete the
	// worker role that existed solely to nest C under the old parent.
	for _, bnd := range priorParentBindings {
		if err := s.DB.EndBinding(ctx, bnd.ID, EndReasonReparented); err != nil {
			return nil, fmt.Errorf("ops.ReparentCoordinator: end prior parent binding %d: %w", bnd.ID, err)
		}
		if bnd.RoleID != 0 {
			if err := s.DB.DeleteRoleByID(ctx, bnd.RoleID); err != nil && !errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("ops.ReparentCoordinator: delete prior parent role %d: %w", bnd.RoleID, err)
			}
		}
	}

	name, err := s.uniqueRoleName(ctx, in.ParentOrchestratorID, defaultStr(in.RoleName, child.Name))
	if err != nil {
		return nil, err
	}

	role, err := s.DB.CreateRole(ctx, CreateRoleInput{
		OrchestratorID: in.ParentOrchestratorID,
		Name:           name,
		Kind:           KindWorker,
		ArgusProject:   strings.TrimSpace(in.ArgusProject),
	})
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: create role: %w", err)
	}

	bnd, err := s.DB.CreateBinding(ctx, CreateBindingInput{
		RoleID:         role.ID,
		OrchestratorID: in.ParentOrchestratorID,
		ArgusTaskID:    taskID,
		WorktreePath:   coordWorktree,
	})
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: create binding: %w", err)
	}

	return &ReparentCoordResult{
		ParentOrchestratorName: parent.Name,
		ChildOrchestratorName:  child.Name,
		RoleName:               role.Name,
		RoleID:                 role.ID,
		BindingID:              bnd.ID,
	}, nil
}

// defaultStr returns s trimmed, or fallback when s is blank.
func defaultStr(s, fallback string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return fallback
}
