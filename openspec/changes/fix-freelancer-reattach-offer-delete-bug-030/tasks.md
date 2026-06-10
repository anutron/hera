# Tasks

## 1. Implementation

- [x] 1.1 Add `ops.DeleteTaskByID(ctx, taskID)` (`internal/view/ops/delete.go`): delete the argus task directly by id; tolerate already-gone (404, client returns nil) and worktree-missing (BUG-020) as success; error on empty id.
- [x] 1.2 Add `DeleteTaskByID` to the `mutationService` interface (`internal/view/mutations.go`).
- [x] 1.3 In `OnReattach`'s worker/freelancer worktree-missing branch, route `roleID == 0` (freelancer) to `offerDeleteOrphaned` with a do-func calling `DeleteTaskByID(taskID)`, keeping the managed-role revive-or-delete path on `roleID != 0`.

## 2. Tests

- [x] 2.1 `TestDeleteTaskByID_DeletesArgusTask` / `_WorktreeMissingIsSoftSuccess` / `_OtherErrorSurfaces` / `_EmptyID_Errors` (`internal/view/ops/delete_test.go`).
- [x] 2.2 `TestBridge_OnReattach_DeadSessionFreelancer_WorktreeMissing_DeleteOnly`: a freelancer worktree-missing reattach shows the delete-only confirm (NOT the revive picker, NOT a raw error modal) and deletes the argus task by id, refreshing the rail.
- [x] 2.3 `TestBridge_OnReattach_DeadSessionFreelancer_WorktreeMissing_NoChoice_NoAction`: declining the confirm deletes nothing.
- [x] 2.4 Fake `mutationService.DeleteTaskByID` records calls.
- [x] 2.5 `make test` passes with `-race` AND `golangci-lint run` is clean.
