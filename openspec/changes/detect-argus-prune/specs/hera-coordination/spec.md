# hera-coordination delta: detect-argus-prune

## ADDED Requirements

### Requirement: task.deleted event ends live bindings

The system SHALL handle the `task.deleted` argus event by ending every live binding
whose `ArgusTaskID` matches the deleted task's ID, using `end_reason = "task_deleted"`.

A deleted task with no live bindings MUST be silently ignored (no error).

#### Scenario: task.deleted ends a live binding

- **GIVEN** a task `T` has a live binding in hera
- **WHEN** a `task.deleted` event arrives with `task_id = T`
- **THEN** hera MUST call `Bindings.End(T, "task_deleted")` and log INFO `"binding ended on task.deleted"`

#### Scenario: task.deleted with no binding is a no-op

- **GIVEN** no live binding exists for task `T`
- **WHEN** a `task.deleted` event arrives with `task_id = T`
- **THEN** hera MUST return without error and without mutating any binding row

### Requirement: task.archived preserves the binding

The system MUST NOT end a binding when a `task.archived` event arrives. Archive is a
reversible visibility change — the worktree still exists, the agent may still be live,
and the role MUST remain resumable.

Only `task.deleted` ends a live binding. `task.archived` MUST be a no-op with respect
to binding lifecycle.

#### Scenario: task.archived does NOT end the binding

- **GIVEN** a task `T` has a live binding in hera
- **WHEN** a `task.archived` event arrives with `task_id = T`
- **THEN** hera MUST NOT end the binding — `T` remains resumable via `hera_join`

#### Scenario: task.archived multi-binding task preserves all bindings

- **GIVEN** a task `T` incarnates two roles (two live bindings)
- **WHEN** a `task.archived` event arrives with `task_id = T`
- **THEN** both bindings MUST remain live

### Requirement: boot reconcile on daemon startup

The system SHALL call `ResyncHandler.Reconcile` synchronously during daemon startup,
after the `ResyncHandler` is constructed, to end bindings for tasks deleted while hera
was offline.

A failure from the reconcile (e.g., argus temporarily unreachable) MUST be logged at
WARN and MUST NOT prevent the daemon from starting.

Reconcile MUST include archived tasks in its "live" set (using the `archived=all`
endpoint) so that merely-archived tasks do NOT have their bindings ended. Only tasks
that are fully absent from argus (deleted/pruned) trigger binding termination.

#### Scenario: boot reconcile runs at startup

- **WHEN** the hera daemon starts successfully
- **THEN** `GET /api/tasks` MUST have been called at least once synchronously within `Start()`

#### Scenario: boot reconcile failure does not block startup

- **WHEN** `GET /api/tasks` returns a non-200 response during `Start()`
- **THEN** `Start()` MUST return `(daemon, nil)` — no error propagated

#### Scenario: reconcile preserves bindings for archived tasks

- **GIVEN** a task `T` is archived in argus (absent from the default task list but present in `?archived=all`)
- **AND** `T` has a live binding in hera
- **WHEN** reconcile runs
- **THEN** hera MUST NOT end the binding for `T`
