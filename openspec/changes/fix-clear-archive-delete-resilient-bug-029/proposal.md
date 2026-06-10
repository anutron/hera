# `C` clears the archive resiliently and deletes the underlying argus tasks (BUG-029)

## Why

`C` (clear the archive under a coordinator) had two defects:

- **Freelancer spray.** `C`'s prune path (`CompleteArchivedDescendants` → `PruneArchivedRole`) deleted the hera role row + worktree but NOT the underlying argus task. The task stayed alive and unmanaged, so it resurfaced as a freelancer (in the Freelance archive). This is the SAME orphan class BUG-021/#136 fixed for `^d` coord-delete — the `C`/prune path simply never got the "delete the child argus task" treatment. Confirmed live: a session's bug-fix workers got sprayed into the Freelance archive after `C`.
- **Aborts on a `○` detached entry.** The sweep halted when it hit a fully-detached archived agent (`○` — no live session, worktree gone). BUG-018 made worktree removal resilient, but the argus complete/delete call on a dead task still errored and aborted the whole batch, stranding every remaining archived row.

## What Changes

- **`C` now DELETEs the underlying argus task for every archived descendant**, regardless of state (complete / incomplete / `○` detached). It reuses BUG-021/#136's task-delete path (`DELETE /api/tasks/{id}`, argus cleans the worktree + branch server-side). The task id is resolved from the role's latest binding (live OR ended). A task argus reports as already gone (404 → the client returns success) or whose worktree was removed out-of-band (BUG-020 `IsWorktreeMissing`) is a clean skip. No archived worker can resurface as a freelancer.
- **The complete (`:checked:`) step loses its ability to abort.** `C` is "clear the archive", not "must complete first". A status read or write that fails on a dead / pruned / `○` detached task is logged and skipped — the sweep proceeds straight to deleting the task.
- **The sweep NEVER aborts on a single failure.** Every per-role step (binding lookup, complete, argus delete, worktree removal, hera row delete) is best-effort and error-collecting: a failure is logged and counted in the summary, the role is carried as far through teardown as possible, and the sweep continues. The `○` detached case can no longer halt the batch. The returned error is reserved for the top-level role listing failing, so the rail still refreshes and the caller reports "N pruned, M errors".
- **`PruneSummary` gains an `Errors` count** alongside `Found` / `Pruned` / `WorktreeSkipped`.

Design parity (Aaron, 2026-06-10): `C` matches BUG-021/#136 coord-delete — DELETE the tasks (not archive), no freelancer spray, fully resilient.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the removal-verbs requirement gains `C` (clear-archive) as a distinct verb — `C` completes (best-effort), DELETEs the underlying argus task, removes the worktree (best-effort), and prunes the hera row for every archived descendant under a coordinator, never aborting on a single failure.

## Impact

- `internal/view/ops/status.go` — `CompleteArchivedDescendants` now deletes the underlying argus task (best-effort), drops the complete step's ability to abort, never aborts the batch, and counts errors; `PruneSummary` gains `Errors`.
- `internal/view/ops/prune_test.go`, `internal/view/ops/fakes_test.go` — coverage for task-delete across all states, the `○` detached no-abort case, and the genuine-delete-error counted-not-aborted case; fake `deleteErrByTask`.
