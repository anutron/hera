# Delete a coordinator's entire subtree, not just its direct roles (BUG-021)

## Why

`^d` on a coordinator orphans its workers' argus tasks into freelancers. The operator wanted the whole tree gone; instead deleting one `⊘` coord (BUG-058-fix) sprayed a bunch of new hera freelancers — unmanaged argus tasks — many of them with already-removed worktrees (zombie freelancers).

Two enumeration gaps cause it:

- **Direct roles only.** `DeleteOrchestrator` enumerated the orchestrator's own roles. A sub-coordinator's workers live in a DESCENDANT orchestrator (linked by the shared sub-coordinator argus task), which was never visited — so those descendant tasks stayed alive and unmanaged.
- **Active roles only.** It enumerated active roles via the active-only list, but the physical orchestrator delete cascades to ARCHIVED role rows too. An archived (completed) worker whose argus task was still alive lost its hera row and resurfaced as a freelancer — frequently with its worktree already cleaned, hence "zombie."

## What Changes

- **`^d` on a coordinator now tears down the ENTIRE subtree.** It enumerates every descendant orchestrator reachable via shared sub-coordinator argus tasks (the same BFS the message/tree-update handlers use), and under each it destroys every role's argus task — ACTIVE and ARCHIVED — then physically deletes every orchestrator row in the subtree. Nothing is left behind to resurface as a freelancer.
- **Archived child roles surrender their argus task.** Role-level destruction now resolves the argus task id from the live binding when present, FALLING BACK to the latest ended binding (the archived-role shape), so an archived worker whose task is still alive gets that task destroyed instead of orphaned.
- **Worktree removal is best-effort across the cascade (BUG-018 / BUG-021).** A stale or unremovable worktree is logged and skipped, never aborting the teardown — one bad worktree must not strand every sibling task.
- **The confirm message states the true scope.** The orchestrator delete confirm now says the ENTIRE agent subtree is destroyed (direct children plus sub-coordinators and their descendants, including completed/archived agents), and that nothing is left behind as a freelancer.

Design decision (Aaron, 2026-06-10): DELETE the child argus tasks (not archive) so nothing resurfaces.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the `^d` delete requirement — orchestrator delete now destroys the whole subtree's argus tasks (active + archived roles, descendant orchestrators), physically deletes every orchestrator row, and treats worktree removal as best-effort.

## Impact

- `internal/view/ops/delete.go` — `DeleteOrchestrator` enumerates the subtree + inclusive roles and physically deletes every orchestrator; `deleteRoleInternal` falls back to the latest ended binding for the argus task id and makes worktree removal best-effort.
- `internal/view/ops/service.go` — `ops.DB` gains `SubtreeOrchIDs`.
- `internal/view/ops_adapters.go` — adapter wires `db.SubtreeOrchIDs` (maxDepth 6).
- `internal/view/mutations.go` — orchestrator delete confirm states the subtree scope.
- `internal/view/ops/delete_test.go`, `internal/view/ops/fakes_test.go` — subtree, archived-child, and worktree-failure coverage; fake `SubtreeOrchIDs`.
