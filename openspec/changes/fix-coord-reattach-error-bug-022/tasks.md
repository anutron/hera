# Tasks

## 1. Implementation

- [x] 1.1 Add `IsAlreadyRunning(err)` to `internal/argus/errors.go`: true for a `409` HTTPError, or a `500` whose body contains the `session already exists` marker.
- [x] 1.2 In `ops.ReattachAgent` (`internal/view/ops/reattach.go`), return nil when `argus.IsAlreadyRunning(err)` — AFTER the `ErrNoTaskRestart` and `IsWorktreeMissing` checks so terminal failures still win.

## 2. Tests

- [x] 2.1 `IsAlreadyRunning`: 409, race-500, wrapped variants true; generic 500, worktree-missing 500, 404, non-HTTP error false.
- [x] 2.2 `ReattachAgent`: 409 and race-500 return nil (success); a generic start-failure 500 still propagates and maps to no typed sentinel.
- [x] 2.3 `make test` passes with `-race`.
