# Tasks

## 1. Implementation

- [x] 1.1 In `CompleteArchivedDescendants` (`internal/view/ops/status.go`), resolve the latest binding (live OR ended) to recover the argus task id + worktree path; a binding-lookup error is non-fatal (log + count, still prune the hera row).
- [x] 1.2 Drop the complete step's ability to abort: a `GetTaskStatus` / `SetTaskStatus` failure is logged and skipped (proceed to delete the task), never aborting.
- [x] 1.3 DELETE the underlying argus task via `s.Argus.DeleteTask` (BUG-021/#136 path) for every archived descendant; tolerate already-gone (`ErrArgusTaskGone`) and worktree-missing (`argus.IsWorktreeMissing`) as clean skips; any other delete failure is logged + counted, never aborting.
- [x] 1.4 Keep worktree removal best-effort (BUG-018 guard) and the hera row delete; a row-delete failure is logged + counted, the sweep continues.
- [x] 1.5 Add `Errors` to `PruneSummary`; the sweep returns `(summary, nil)` for per-role failures so the rail still refreshes.

## 2. Tests

- [x] 2.1 `TestCompleteArchivedDescendants_DeletesArgusTasks_AllStates`: complete + incomplete + `○` detached archived workers all clear AND each underlying argus task is DELETEd (no freelancer spray).
- [x] 2.2 `TestCompleteArchivedDescendants_DetachedDeleteWorktreeMissing_NoAbort`: a detached worker whose argus delete fails with the worktree-missing marker is a soft skip — it and its siblings still clear, `Errors` stays 0.
- [x] 2.3 `TestCompleteArchivedDescendants_ArgusDeleteError_CountedNotAborted`: a genuine argus-delete failure is counted in `summary.Errors` but does not abort; every row is still pruned.
- [x] 2.4 Update `TestCompleteArchivedDescendants_CompletesAndPrunesArchivedWorkers` to assert the argus task is DELETEd; add `deleteErrByTask` to the argus fake.
- [x] 2.5 `make test` passes with `-race` AND `golangci-lint run` is clean.
