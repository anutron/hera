# Worktree-missing reattach on a freelancer offers to delete its argus task (BUG-030)

## Why

BUG-028's revive-or-delete recovery (Enter on a dead-session row whose worktree is gone) promised, in its spec, that when revive is impossible — "a freelancer (no hera role) or a header with no coordinator role to rebind" — hera falls back to the BUG-020 delete-only confirmation. The implementation delivered that fallback for the mixed-coord header with no coord role, but NOT for a freelancer row: `OnReattach`'s worker/freelancer branch gated the recovery on `roleID != 0`, so a freelancer (`roleID == 0`) fell through to the raw argus 500 ("worktree path missing …") instead of any recovery offer. An orphaned freelancer was therefore stuck — un-reattachable AND with no way to clear it from the rail via Enter.

## What Changes

- **A dead-session freelancer (`roleID == 0`) whose reattach hits `worktree path missing` now gets the BUG-020 delete-only confirmation** instead of the raw argus 500. A freelancer has no durable hera role to rebind, so revive is correctly impossible; the operator is offered to delete the orphan.
- **The delete targets the argus task directly by id** — the freelancer IS the task (there is no hera role/orchestrator row to delete). New `ops.DeleteTaskByID(taskID)` destroys the argus task (worktree + branch, server-side), tolerating an already-gone (404) or worktree-missing task as success so the orphan clears from the rail.
- The managed-role and coord-header paths are unchanged (revive-or-delete picker, and delete-only when a coord role is absent).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the worktree-missing reattach recovery gains the freelancer branch — a freelancer falls back to a delete-only confirmation that deletes its argus task directly, fulfilling BUG-028's "revive impossible → delete-only" promise for the freelance case.

## Impact

- `internal/view/ops/delete.go` — new `DeleteTaskByID(taskID)` (task-direct delete, tolerates 404 + worktree-missing).
- `internal/view/mutations.go` — `mutationService` gains `DeleteTaskByID`; `OnReattach`'s worker/freelancer worktree-missing branch routes a freelancer (`roleID == 0`) to the delete-only confirmation instead of the raw error.
- `internal/view/ops/delete_test.go`, `internal/view/mutations_test.go` — coverage for the task-direct delete (success, worktree-missing soft success, other-error surface, empty id) and the freelancer delete-only offer (delete + decline); fake `DeleteTaskByID`.
