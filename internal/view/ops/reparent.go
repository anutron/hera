package ops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ReparentCoordInput describes an operator-side coordinator re-parent: nest an
// existing coordinator C (its own orchestrator) under another coordinator P as
// a sub-coordinator. The view supplies the child orchestrator id and the chosen
// parent orchestrator id; C's coordinator argus task + worktree are resolved
// from C's coordinator role's latest binding (see ReparentCoordinator).
//
// The re-parent linkage is the SAME multi-binding the renderer already nests
// (resolveSubCoordinators): a worker role under P whose binding's argus task is
// C's coordinator task. C's whole subtree (its roles/workers) moves with it
// because the subtree is derived from C, which is untouched — only its parent
// linkage changes.
type ReparentCoordInput struct {
	// ChildOrchestratorID is the orchestrator being re-parented (C).
	ChildOrchestratorID int64
	// CoordTaskID is an OPTIONAL hint for C's coordinator argus task (the
	// multi-binding key that nests C under its parent), carried from the rail
	// selection when the coord session is live. It may be empty for a dormant
	// coordinator whose coord session has ended; the op then resolves the task
	// id from C's coordinator role's latest binding (BUG-025).
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
//   - C must have a coordinator role with at least one binding (live OR ended).
//     Re-parenting is structural — it links P's new worker role to C's
//     coordinator argus TASK, which exists regardless of session liveness
//     (BUG-025). The coord task id + worktree are resolved from C's coordinator
//     role's LATEST binding, so a dormant/archived coordinator re-parents from
//     its most-recent ended binding (same fallback BUG-021's cascade uses). A
//     coordinator whose coord role never had a binding cannot be re-parented.
func (s *Service) ReparentCoordinator(ctx context.Context, in ReparentCoordInput) (*ReparentCoordResult, error) {
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

	// Resolve C's coordinator argus task id + worktree path from C's coordinator
	// role's LATEST binding — LIVE if one exists, otherwise the most-recent
	// ENDED binding (BUG-025). Re-parenting links to C's coordinator TASK, which
	// outlives its session; deriving the worktree from a live-only binding made
	// the gesture inapplicable to a dormant coordinator (the reported symptom).
	coordRole, err := s.coordRoleOf(ctx, in.ChildOrchestratorID, child.Name)
	if err != nil {
		return nil, err
	}
	latest, err := s.DB.GetLatestBindingByRole(ctx, coordRole.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, validation(fmt.Sprintf(
			"%q has never had a coordinator binding to re-parent", child.Name,
		))
	}
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: latest coord binding for role %d: %w", coordRole.ID, err)
	}
	taskID := strings.TrimSpace(latest.ArgusTaskID)
	if taskID == "" {
		return nil, validation(fmt.Sprintf("%q has no argus task id to re-parent", child.Name))
	}
	coordWorktree := latest.WorktreePath
	if coordWorktree == "" {
		return nil, validation(fmt.Sprintf(
			"%q has no coordinator worktree to re-parent", child.Name,
		))
	}

	// Tear down EVERY prior parent linkage for C's coord task so the re-parent is
	// IDEMPOTENT — pressing J repeatedly never piles up de-collided duplicate
	// link roles (BUG-026). A parent link is a binding of C's coord task on any
	// role OTHER than C's own coordinator role (identified by role id, robust
	// against bindings with a NULL orchestrator_id on legacy rows).
	//
	// Both LIVE and ENDED links must go: the resync reconciler ends a link's
	// binding when C's coord task is gone from argus (end_reason resync_missing),
	// but leaves the link ROLE row behind. The old live-only teardown missed
	// those, so the next re-parent's uniqueRoleName de-collided into "C-2",
	// "C-3", … under the same parent. Delete the role (its bindings cascade) so
	// the name frees up and exactly one clean link is recreated below.
	links, err := s.DB.ListBindingsByTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: list bindings for %s: %w", taskID, err)
	}
	// End live parent-link bindings first (audit: reparented) before the role
	// delete cascades them away — preserving the end-reason on the historical row
	// the same way a clean move always has.
	liveLinks, err := s.DB.ListLiveBindingsByTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: list live bindings for %s: %w", taskID, err)
	}
	for _, bnd := range liveLinks {
		if bnd.RoleID == coordRole.ID {
			continue // C's own coordinator binding — never a parent link.
		}
		if err := s.DB.EndBinding(ctx, bnd.ID, EndReasonReparented); err != nil {
			return nil, fmt.Errorf("ops.ReparentCoordinator: end prior parent binding %d: %w", bnd.ID, err)
		}
	}
	// Delete every distinct parent-link ROLE (live or ended binding).
	deleted := make(map[int64]bool)
	for _, bnd := range links {
		if bnd.RoleID == 0 || bnd.RoleID == coordRole.ID || deleted[bnd.RoleID] {
			continue
		}
		deleted[bnd.RoleID] = true
		if err := s.DB.DeleteRoleByID(ctx, bnd.RoleID); err != nil && !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("ops.ReparentCoordinator: delete prior parent role %d: %w", bnd.RoleID, err)
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

// coordRoleOf returns the coordinator role under orchID (the first
// KindCoordinator role, active or archived), or a validation error when the
// orchestrator has no coordinator role at all. Used by ReparentCoordinator to
// recover the coordinator argus task + worktree from the coord role's latest
// binding regardless of session liveness (BUG-025). name is the orchestrator's
// name, used only for the error message.
func (s *Service) coordRoleOf(ctx context.Context, orchID int64, name string) (*Role, error) {
	roles, err := s.DB.ListRolesByOrchestratorInclusive(ctx, orchID)
	if err != nil {
		return nil, fmt.Errorf("ops.ReparentCoordinator: list roles for %d: %w", orchID, err)
	}
	for _, r := range roles {
		if r.Kind == KindCoordinator {
			return r, nil
		}
	}
	return nil, validation(fmt.Sprintf("%q has no coordinator role to re-parent", name))
}

// defaultStr returns s trimmed, or fallback when s is blank.
func defaultStr(s, fallback string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return fallback
}
