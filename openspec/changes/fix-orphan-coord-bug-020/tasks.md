# Tasks

## 1. Implementation

- [x] 1.1 Add `argus.IsWorktreeMissing(err)` (`internal/argus/errors.go`): unwrap the typed `*HTTPError` via `errors.As` and match the stable `worktree path missing` body marker.
- [x] 1.2 Add the `ops.ErrWorktreeMissing` sentinel (`internal/view/ops/errors.go`) and surface it from `ReattachAgent` (`internal/view/ops/reattach.go`) when argus reports the worktree-missing 500, distinct from `ErrRestartNotSupported`.
- [x] 1.3 Make `deleteRoleInternal` (`internal/view/ops/delete.go`) treat an argus delete that fails with the worktree-missing condition as a soft skip (logged), so the cascade + DB deletion still complete; any OTHER argus delete failure still aborts.
- [x] 1.4 In `OnReattach` (`internal/view/mutations.go`), recognize `ErrWorktreeMissing` on both the mixed-coord header path and the dead-session role path; suppress the raw error and call `offerDeleteOrphaned` (orchestrator → `DeleteOrchestrator`, role → `DeleteRole`).
- [x] 1.5 Add `offerDeleteOrphaned`: opens a y/N confirmation and runs the delete off the event loop via `goUI` (NOT `mutate`, to avoid racing the in-flight flag held by the enclosing reattach op), refreshing the rail on success.

## 2. Tests

- [x] 2.1 `argus.IsWorktreeMissing`: true for the worktree-missing 500 (plain + wrapped), false for other 500s, 404s, and non-HTTP errors.
- [x] 2.2 `ReattachAgent` surfaces `ErrWorktreeMissing` (not `ErrRestartNotSupported`) on the worktree-missing 500.
- [x] 2.3 `DeleteOrchestrator` still removes the orchestrator + roles from the DB when argus's delete fails with worktree-missing (audit log records the skip); a non-worktree-missing argus error still aborts the cascade and leaves the orchestrator intact.
- [x] 2.4 `OnReattach` on a worktree-missing mixed-coord header shows no raw error, offers a delete confirm, and on Yes calls `DeleteOrchestrator` and refreshes; confirm=No does not delete.
- [x] 2.5 `OnReattach` on a worktree-missing dead-session worker offers a delete confirm routing to `DeleteRole`.
- [x] 2.6 `make test` passes with `-race`.
