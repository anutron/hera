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

### Requirement: boot reconcile on daemon startup

The system SHALL call `ResyncHandler.Reconcile` synchronously during daemon startup,
after the `ResyncHandler` is constructed, to end bindings for tasks deleted while hera
was offline.

A failure from the reconcile (e.g., argus temporarily unreachable) MUST be logged at
WARN and MUST NOT prevent the daemon from starting.

#### Scenario: boot reconcile runs at startup

- **WHEN** the hera daemon starts successfully
- **THEN** `GET /api/tasks` MUST have been called at least once synchronously within `Start()`

#### Scenario: boot reconcile failure does not block startup

- **WHEN** `GET /api/tasks` returns a non-200 response during `Start()`
- **THEN** `Start()` MUST return `(daemon, nil)` — no error propagated
