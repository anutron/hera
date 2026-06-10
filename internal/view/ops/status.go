package ops

import (
	"context"
	"errors"
	"fmt"

	"github.com/anutron/hera/internal/argus"
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
// Found is the number of archived non-coordinator descendants the sweep
// ENCOUNTERED — every one is a prune candidate regardless of its completion
// state (BUG-023). The view uses Found==0 (not Pruned==0) to decide there was
// genuinely nothing to do: a coordinator with no archived descendants at all,
// as opposed to one whose archived workers were merely already complete.
// Pruned is the number of roles removed from hera's DB (and therefore the
// rail). WorktreeSkipped counts roles whose worktree could not be removed
// (already gone, detached, or otherwise) but which were pruned from the DB
// anyway — disk cleanup is best-effort and never blocks clearing the rail.
// Errors counts roles that hit a non-fatal failure during the sweep (a
// binding lookup that errored, an argus task delete that failed for a reason
// other than already-gone, or a hera role-row delete that failed). The sweep
// never aborts on these (BUG-029); the count lets the caller report "N pruned,
// M errors" without the whole batch halting.
type PruneSummary struct {
	Found           int
	Pruned          int
	WorktreeSkipped int
	Errors          int
}

// CompleteArchivedDescendants clears the ENTIRE archive under the given
// orchestrator: every archived non-coordinator role, regardless of completion
// state (complete / incomplete / ○ fully-detached), is torn down from argus,
// disk, and hera's DB. Backs the `C` rail key. The coordinator role itself is
// skipped.
//
// For each archived descendant the sweep, in order:
//  1. Marks the bound argus task :checked: (best-effort — `C` is "clear the
//     archive", not "must complete first": a status read/write that fails on a
//     dead or pruned task is logged and skipped, never aborting).
//  2. DELETEs the underlying argus task (argus removes its worktree + branch
//     server-side). This is the BUG-029 fix: pruning only hera's role row left
//     the argus task alive, so it resurfaced as a freelancer — the same orphan
//     class BUG-021 fixed for coord-delete. Best-effort: an already-gone task
//     (404 → the client returns nil) or one whose worktree was removed
//     out-of-band (BUG-020 IsWorktreeMissing) is a clean skip.
//  3. Removes the worktree locally as a defensive fallback (best-effort,
//     BUG-018 guard: a stale/detached/missing worktree never blocks the row).
//  4. Deletes the hera role row.
//
// The sweep NEVER aborts on a single failure (BUG-029): the ○ detached case —
// no live session, worktree gone, argus task dead — must not halt the batch.
// Every per-role failure is logged and counted (summary.Errors / Worktree
// Skipped), the role is carried as far through the teardown as possible, and
// the sweep continues. The returned error is reserved for the top-level role
// listing failing; per-role failures surface only through the summary so the
// rail still refreshes and the operator sees "N pruned, M errors".
//
// The summary's Found counts every archived descendant encountered (the prune
// candidates); the caller fires "nothing to do" only when Found==0, so already-
// complete archived workers are cleared rather than short-circuited (BUG-023).
func (s *Service) CompleteArchivedDescendants(ctx context.Context, orchID int64) (PruneSummary, error) {
	roles, err := s.DB.ListRolesByOrchestratorInclusive(ctx, orchID)
	if err != nil {
		return PruneSummary{}, fmt.Errorf("ops.CompleteArchivedDescendants: list roles: %w", err)
	}
	var summary PruneSummary
	for _, role := range roles {
		if !role.Archived {
			continue
		}
		if role.Kind == KindCoordinator {
			continue
		}
		// An archived non-coordinator descendant: a prune candidate regardless
		// of its completion state. `C` clears the WHOLE archive under the
		// coordinator, so already-complete workers count too (BUG-023). Counting
		// every one in Found lets the caller distinguish "nothing archived to
		// prune" from "nothing that still needed completing".
		summary.Found++

		// Resolve the latest binding (live OR ended) to recover the argus task
		// id + worktree path. A binding-lookup error is non-fatal: we still
		// delete the hera role row below so the rail clears.
		bnd, err := s.resolveBinding(ctx, role.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			s.logf("complete archived: binding lookup failed for role %q (%d), pruning hera row anyway: %v", role.Name, role.ID, err)
			summary.Errors++
		}
		argusTaskID := ""
		worktreePath := ""
		if bnd != nil {
			argusTaskID = bnd.ArgusTaskID
			worktreePath = bnd.WorktreePath
		}

		if argusTaskID != "" {
			// Mark :checked: ONLY when the task is not already complete. An
			// already-complete worker just needs deleting — re-issuing the status
			// write is redundant and argus may reject a no-op transition
			// (BUG-023). Completion is best-effort (BUG-029): a status read or
			// write that fails (dead/pruned/○ detached task) is logged and
			// skipped — we proceed straight to deleting the task.
			if cur, statErr := s.Argus.GetTaskStatus(ctx, argusTaskID); statErr != nil {
				s.logf("complete archived: status read failed for role %q (%d), deleting task without completing: %v", role.Name, role.ID, statErr)
			} else if cur != "complete" {
				if _, err := s.Argus.SetTaskStatus(ctx, argusTaskID, "complete"); err != nil {
					s.logf("complete archived: mark complete failed for role %q (%d), deleting task anyway: %v", role.Name, role.ID, err)
				}
			}

			// DELETE the underlying argus task so it never resurfaces as a
			// freelancer (BUG-029, same orphan class as BUG-021). argus cleans
			// the task's worktree + branch server-side. Best-effort: an
			// already-gone task (404, returned as nil by the client) or one whose
			// worktree was removed out-of-band (BUG-020 IsWorktreeMissing) is a
			// clean skip; any other delete failure is logged + counted but never
			// aborts the sweep (the ○ detached case must not halt the batch).
			s.logf("complete archived: destroying argus task %q (worktree+branch)", argusTaskID)
			if err := s.Argus.DeleteTask(ctx, argusTaskID); err != nil {
				if errors.Is(err, ErrArgusTaskGone) || argus.IsWorktreeMissing(err) {
					s.logf("complete archived: argus task %q already gone, skipping delete: %v", argusTaskID, err)
				} else {
					s.logf("complete archived: argus delete failed for task %q, pruning hera row anyway: %v", argusTaskID, err)
					summary.Errors++
				}
			}
		}

		// Local worktree remove is a defensive fallback — argus already deleted
		// the worktree above in the common case, so this is usually a soft no-op
		// (directory gone). Best-effort (BUG-018): a stale/detached/missing
		// worktree must never block clearing the role from the rail. The guard
		// in removeWorktree soft-skips already-gone and detached paths (no
		// error); a genuine removal failure is logged + counted, the row still
		// deleted below.
		if err := s.removeWorktree(ctx, worktreePath); err != nil {
			s.logf("complete archived: worktree remove failed for role %q (%d), pruning DB row anyway: %v", role.Name, role.ID, err)
			summary.WorktreeSkipped++
		}

		// Delete the hera role row. The one failure that genuinely leaves a row
		// in the rail — still non-fatal to the batch: log, count, continue.
		if err := s.DB.DeleteRoleByID(ctx, role.ID); err != nil && !errors.Is(err, ErrNotFound) {
			s.logf("complete archived: delete role row failed for role %q (%d): %v", role.Name, role.ID, err)
			summary.Errors++
			continue
		}
		summary.Pruned++
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
