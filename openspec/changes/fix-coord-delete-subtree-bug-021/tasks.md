# Tasks

## 1. Implementation

- [x] 1.1 Add `SubtreeOrchIDs(ctx, rootOrchID) ([]int64, error)` to the `ops.DB` interface (`internal/view/ops/service.go`).
- [x] 1.2 Implement the adapter (`internal/view/ops_adapters.go`) delegating to `db.SubtreeOrchIDs` with maxDepth 6 (matching the other callers).
- [x] 1.3 Rewrite `DeleteOrchestrator` (`internal/view/ops/delete.go`): snapshot the subtree up front, then for every orchestrator enumerate INCLUSIVE roles (active + archived) and destroy each, then physically delete every orchestrator row in the subtree.
- [x] 1.4 In `deleteRoleInternal`, fall back to the latest ended binding (when no live binding) to recover the argus task id + worktree path, so archived roles still surrender their task; end the binding only when live.
- [x] 1.5 Make worktree removal best-effort in `deleteRoleInternal` — log and continue on failure instead of aborting (BUG-018 / BUG-021).
- [x] 1.6 Update the orchestrator delete confirm message (`internal/view/mutations.go`) to state the subtree scope and the no-freelancer guarantee.

## 2. Tests

- [x] 2.1 `TestDeleteOrchestrator_DeletesSubtree`: parent coord + sub-coordinator (shared task) + sub-coordinator's workers → every argus task destroyed, both orchestrators deleted.
- [x] 2.2 `TestDeleteOrchestrator_DestroysArchivedChildArgusTask`: an archived child role's still-alive argus task is destroyed.
- [x] 2.3 `TestDeleteOrchestrator_WorktreeFailureDoesNotAbort`: a worktree-removal failure does not abort the cascade — sibling task still destroyed, orchestrator still removed.
- [x] 2.4 Fake `SubtreeOrchIDs` mirrors the SQL BFS over the in-memory maps.
- [x] 2.5 `make test` passes with `-race`.
