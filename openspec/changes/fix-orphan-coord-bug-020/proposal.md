# Fix orphaned coords whose worktree was deleted (BUG-020)

## Why

After BUG-019, pressing Enter on a `⊘` mixed-coord header surfaces a clear error instead of bouncing — but for a whole class of coordinators that error is a dead end. When an earlier bulk cleanup DELETED the coord's argus task worktree, argus can no longer resume a session there: the restart returns `HTTP 500: worktree path missing: <path> (delete the task or recreate the worktree)`. These coordinators are genuinely unrecoverable by reattach — they are orphans. The operator's only sensible action is to DELETE them, yet:

- Reattach treats the argus 500 as a terminal error with no recovery action — the operator sees a raw HTTP error and no way forward.
- The coord-delete path (`^d`) may ALSO choke: argus's delete can fail on the missing worktree, aborting the cascade before the orchestrator + roles + bindings clear from hera's DB, so the orphan stays on the rail.

## What Changes

- **Reattach recognizes the worktree-missing condition and offers delete:** when argus's restart fails because the task's worktree is gone, hera surfaces the typed `ErrWorktreeMissing` (recognized via a new `argus.IsWorktreeMissing` helper that matches argus's stable `worktree path missing` body marker). `OnReattach` then opens a confirmation — "This coordinator's worktree is gone and can't be revived. Delete it? (y/N)" — and on confirm routes to the orchestrator-delete path. The same recovery applies to a dead-session worker row (routing to role-delete) so the gap can't strand a leaf agent either.
- **Delete is resilient to a missing worktree:** the `^d` delete path treats an argus delete that chokes on the missing worktree (`worktree path missing`) as a soft skip — the same spirit as the BUG-018 local `git worktree remove` guard — so the orchestrator + roles + bindings are removed from hera's DB regardless of worktree state and the orphan clears from the rail. Any OTHER argus delete failure still aborts (we do not silently orphan DB rows on a transient error).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the `⊘` mixed-coord Enter requirement gains a worktree-missing recovery — reattach offers to delete an unrecoverable orphan instead of dead-ending on a raw 500; the `^d` delete requirement gains resilience to an argus delete that chokes on a missing worktree.

## Impact

- `internal/argus/errors.go` — new `IsWorktreeMissing(err)` helper (typed `*HTTPError` unwrap + stable body-marker match).
- `internal/view/ops/errors.go`, `internal/view/ops/reattach.go` — new `ErrWorktreeMissing` sentinel; `ReattachAgent` surfaces it on the worktree-missing 500.
- `internal/view/ops/delete.go` — `deleteRoleInternal` treats an argus worktree-missing delete failure as a soft skip; other failures still abort.
- `internal/view/mutations.go` — `OnReattach` recognizes `ErrWorktreeMissing` and offers delete (orchestrator → `DeleteOrchestrator`, role → `DeleteRole`) via the new `offerDeleteOrphaned` helper.
- Tests across `internal/argus`, `internal/view/ops`, and `internal/view`.
