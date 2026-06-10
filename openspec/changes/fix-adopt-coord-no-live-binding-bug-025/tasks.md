# Tasks

## 1. Implementation

- [x] 1.1 In `internal/view/ops/reparent.go`, resolve the child's coordinator argus task id + worktree from the child orchestrator's coordinator role's LATEST binding (live OR ended) via `GetLatestBindingByRole`, instead of requiring a live binding. A coordinator with no coordinator role at all, or a coordinator role that never had a binding, surfaces a validation error.
- [x] 1.2 Keep the prior-parent-linkage teardown keyed off LIVE bindings (`ListLiveBindingsByTask`) — only a live parent link is torn down.
- [x] 1.3 In `internal/view/mutations.go`, loosen `coordAdoptTarget` so an orchestrator-header selection routes to coord-adopt on `CoordRoleID != 0` (a coordinator role exists, live or dead) rather than `CoordTaskID != ""`. Update the doc comment.
- [x] 1.4 Drop "live" from the `J` not-applicable notice in `OnAdopt`.

## 2. Tests

- [x] 2.1 `internal/view/ops/reparent_test.go`: invert the dormant-coordinator case — a coordinator whose coord binding has ended is now re-parented, with the worktree recovered from the ended binding.
- [x] 2.2 `internal/view/mutations_test.go`: a coordinator header with no live coord task (CoordTaskID empty, CoordRoleID set) routes `J` to the coordinator picker and re-parents.
- [x] 2.3 The cycle guard (self/descendant) still rejects.
- [x] 2.4 `make test` passes with `-race`.
