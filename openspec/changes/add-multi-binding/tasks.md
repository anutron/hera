## 1. Stage A — Schema migration + DAO surface

- [ ] 1.1 Add migration `0004_bindings_per_orchestrator` to `internal/db/schema.go`. Steps inside the migration SQL (one transaction): `ALTER TABLE bindings ADD COLUMN orchestrator_id INTEGER REFERENCES orchestrators(id) ON DELETE CASCADE`; `UPDATE bindings SET orchestrator_id = (SELECT orchestrator_id FROM roles WHERE roles.id = bindings.role_id)`; `DROP INDEX bindings_live_unique_task`; `DROP INDEX bindings_live_unique_worktree`; `CREATE UNIQUE INDEX bindings_live_unique_task_orch ON bindings(argus_task_id, orchestrator_id) WHERE ended_at IS NULL`; `CREATE UNIQUE INDEX bindings_live_unique_worktree_orch ON bindings(worktree_path, orchestrator_id) WHERE ended_at IS NULL`. Keep `bindings_live_unique_role` and the non-unique by-task / by-worktree helper indexes unchanged.
- [ ] 1.2 Update the `Binding` struct (`internal/db/types.go`) to add `OrchestratorID int64` after `RoleID`.
- [ ] 1.3 Update `CreateBindingInput` in `internal/db/bindings.go` to add `OrchestratorID int64`; update `Create()` to insert the new column.
- [ ] 1.4 Update every `scanOne` / `scanBindingRow` SELECT in `internal/db/bindings.go` to include `orchestrator_id` and populate `Binding.OrchestratorID`.
- [ ] 1.5 Add `GetLiveByTaskAndOrchestrator(ctx, taskID string, orchID int64) (*Binding, error)` returning `ErrNotFound` if no such binding exists.
- [ ] 1.6 Add `ListLiveByTaskID(ctx, taskID string) ([]*Binding, error)` returning every live binding for a task, ordered by `started_at ASC`.
- [ ] 1.7 Add `ErrAmbiguous` sentinel to `internal/db/types.go` (or wherever `ErrNotFound` lives). Update `GetLiveByTaskID` to return `ErrAmbiguous` if 2+ live rows exist for the task; today's `ErrNotFound` semantics for 0-row stays unchanged; 1-row returns the row.
- [ ] 1.8 Tests in `internal/db/db_test.go`:
  - migration leaves existing rows with non-NULL `orchestrator_id` matching the role's orchestrator_id.
  - `GetLiveByTaskAndOrchestrator` returns the right binding when 2 bindings exist for the task across different orchestrators.
  - `ListLiveByTaskID` returns 0/1/2-row results correctly.
  - `GetLiveByTaskID` returns `ErrAmbiguous` when 2+ live bindings exist; returns the single row when exactly one; returns `ErrNotFound` when zero.
  - Inserting a second binding with the same `(argus_task_id, orchestrator_id)` while the first is live FAILS at the DB layer (unique index violation).
- [ ] 1.9 Run `go test ./internal/db/... -race -count=1` until green.

## 2. Stage B — Resolver + caller-role surface

- [ ] 2.1 Update `internal/mcp/resolve.go::CallerRole` signature to `CallerRole(ctx, cwd, orchestrator string) (*argus.Task, *db.Role, *db.Binding, error)`. Resolution rules:
  - `orchestrator != ""`: call `Bindings.GetLiveByTaskAndOrchestrator(task.ID, orch.ID)` after resolving the orchestrator by name; ErrNotFound from either layer maps to a new `ErrNoBindingForOrchestrator` sentinel.
  - `orchestrator == ""` and exactly one live binding: return that binding (today's behavior).
  - `orchestrator == ""` and zero live bindings: `ErrNoBinding` (today's behavior).
  - `orchestrator == ""` and 2+ live bindings: `ErrAmbiguousBinding` (new sentinel) with the orchestrator names attached (as a typed error wrapping a list).
- [ ] 2.2 Add a helper `formatAmbiguousBindingError(bindings []*db.Binding, db *db.DB) string` returning a human-readable list of `(orchestrator, role_name, kind)` triples — used by every handler that needs to surface the ambiguous-binding error.
- [ ] 2.3 Update every existing call site of `CallerRole(ctx, cwd)` in `internal/mcp/` to thread through the `orchestrator` parameter from the tool input. (Specifically `handler_send.go`, `handler_inbox.go`, `handler_mark_read.go`, `handler_status.go`.)
- [ ] 2.4 Run `go test ./internal/mcp/... -race -count=1` until green.

## 3. Stage C — Handler updates

- [ ] 3.1 `handler_new_orchestrator.go`: replace the existing `Bindings.GetLiveByTaskID(task.ID)` rejection check with `Bindings.GetLiveByTaskAndOrchestrator(task.ID, <name>)`. Need to resolve the orchestrator first (or after the `Orchestrators.Create` idempotent step — pre-resolve to know whether a same-name orchestrator exists). On hit, return the error message updated to suggest `hera_join(cwd, orchestrator="<name>")`. Populate `CreateBindingInput.OrchestratorID = orch.ID`.
- [ ] 3.2 `handler_join.go::attach`: replace the `Bindings.GetLiveByTaskID(argusTaskID)` rejection check with `Bindings.GetLiveByTaskAndOrchestrator(argusTaskID, orch.ID)`. On hit, update the error message to suggest `hera_join(cwd, orchestrator="<name>")`. Populate `CreateBindingInput.OrchestratorID = orch.ID`.
- [ ] 3.3 `handler_join.go::reincarnation` is currently `reincarnation(ctx, taskID string)`. Generalize: rename to `claim(ctx, taskID string, orchestrator string)` (or similar). Resolution rules:
  - empty orchestrator + 0 bindings: existing not-bound error.
  - empty orchestrator + 1 binding: return identity (today's path).
  - empty orchestrator + 2+ bindings: ambiguous error with binding list.
  - explicit orchestrator: return identity for that binding if exists, else attach-suggestion error.
- [ ] 3.4 `handler_join.go::Handle`: update the branch logic so that `in.Orchestrator != "" && in.RoleName == "" && in.Kind == ""` routes to the claim path, NOT the attach path. The attach path requires `role_name AND kind` AND optionally `orchestrator`.
- [ ] 3.5 `handler_send.go`: pull `in.Orchestrator` from the input (add `Orchestrator string \`json:"orchestrator,omitempty"\`` to `SendInput`); pass to `resolver.CallerRole(ctx, in.Cwd, in.Orchestrator)`. Surface the ambiguous-binding error as `ErrorResponse` with the formatted hint.
- [ ] 3.6 `handler_inbox.go`: same pattern — add `Orchestrator` to `InboxInput`; pass through `CallerRole`.
- [ ] 3.7 `handler_mark_read.go`: same pattern.
- [ ] 3.8 `handler_status.go`: same pattern. The thread-status meta mirror (`PutTaskMeta(task, "thread_status", status)`) is per-task — document in code that on multi-binding tasks the latest writer wins.
- [ ] 3.9 Update `internal/mcp/registrar.go` (or wherever tool registrations are built) to add the optional `orchestrator` field to the four tools' `input_schema.properties`, with a description naming the multi-binding disambiguation use case. Do NOT add `orchestrator` to `required`.
- [ ] 3.10 Run `go test ./internal/mcp/... -race -count=1` until green.

## 4. Stage D — Events package (adopt + archive)

- [ ] 4.1 `internal/events/adopt.go::handleLinkCreated`: replace the `GetLiveByTaskID(parent)` single-binding check with `ListLiveByTaskID(parent)`. Filter the parent's bindings by role kind = coordinator. If exactly one coordinator binding → use its `OrchestratorID` to scope the new worker role. If zero → return (today's behavior — parent isn't a coordinator). If 2+ → log at WARN level with both orchestrator names AND return (skip adoption).
- [ ] 4.2 `internal/events/adopt.go::handleTaskArchived`: replace the `GetLiveByTaskID(taskID)` single-row lookup with `ListLiveByTaskID(taskID)`. Loop and call `Bindings.End(bnd.ID, "argus_archived")` for each. Log one line per ended binding (preserves the existing audit trail granularity).
- [ ] 4.3 Update `internal/events/adopt_test.go` to add the multi-coordinator-parent skip scenario and the multi-binding-task-archive cleanup scenario.
- [ ] 4.4 Audit `internal/events/resync*.go` — `GetLiveByTaskID` usage there is for "does this task still exist in argus?" rather than "is this task bound?" If the resync iterates the live-binding list and ends rows whose task is gone, multi-binding is handled correctly by iterating per-binding-row rather than per-task. Confirm with a test.
- [ ] 4.5 Run `go test ./internal/events/... -race -count=1` until green.

## 5. Stage E — Integration smoke (in-process)

- [ ] 5.1 New integration-style test in `internal/mcp/handlers_test.go` (or a new `multi_binding_test.go`): set up two orchestrators, attach the same fake-argus-task to both (one worker binding + one coordinator binding), then exercise `hera_send`, `hera_inbox`, `hera_status`, `hera_mark_read` with and without `orchestrator`. Assert: single-orchestrator-binding paths still work; multi-binding paths reject without `orchestrator`; multi-binding paths with `orchestrator` route to the right role.
- [ ] 5.2 Test: a bare `hera_join(cwd)` against the multi-binding task returns the ambiguous error; `hera_join(cwd, orchestrator="foo")` returns the `foo` binding's identity.
- [ ] 5.3 Test: `hera_new_orchestrator("baz", ...)` from a task already bound to `foo` and `bar` succeeds; afterwards `ListLiveByTaskID` returns three rows.
- [ ] 5.4 Run `go test ./... -race -count=1` until green.

## 6. Stage F — Ship

- [ ] 6.1 `make build` to ensure the binary compiles.
- [ ] 6.2 Commit all stages as logical units (one commit per stage or two — schema+DAO, handlers+events, tests is also acceptable).
- [ ] 6.3 `mcp__argus__iris_publish(task_id=$ARGUS_TASK_ID, reset=true, push=true)` — publishes to `main` in the source repo, pushes to origin, runs `make build`, restarts the launchagent.
- [ ] 6.4 Verify via `mcp__argus__iris_status(task_id=$ARGUS_TASK_ID)` that the new HEAD matches the worktree and the reload outcome is success.
- [ ] 6.5 `hera_status done`; `task_set_result` with shipped_sha and milestone.
