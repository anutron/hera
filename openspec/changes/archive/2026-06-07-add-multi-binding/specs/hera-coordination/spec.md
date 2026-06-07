## MODIFIED Requirements

### Requirement: Role identity outlives argus task lifecycle

The system SHALL persist role identity, mission, constraints, and accumulated history (messages, role status) across argus task lifecycle events. Archiving an argus task that incarnates one or more roles MUST end every live binding for that task without deleting any role row or its associated messages.

#### Scenario: Coordinator task archived, role survives

- **WHEN** a coordinator role is bound to argus task `T1` and `T1` is archived
- **THEN** hera MUST set the binding's `ended_at` and `end_reason` columns and leave the role row, all messages addressed to the role, and the role's status row intact

#### Scenario: Same role rebound across multiple incarnations

- **WHEN** a role's current binding is ended and a fresh argus task `T2` is created for the same role
- **THEN** hera MUST insert a new binding row with `(role_id, T2, started_at)` while preserving the previous binding's history

#### Scenario: Multi-binding task archived, every binding ends

- **WHEN** an argus task `T1` holds live bindings to roles in orchestrators `foo` and `bar` and `T1` is archived
- **THEN** hera MUST end both bindings with `end_reason = "argus_archived"` and leave both role rows, their messages, and their status rows intact

### Requirement: New orchestrator bootstrap via `hera_new_orchestrator`

The system SHALL provide `hera_new_orchestrator(cwd, name, coordinator_role_name, [mission], [constraints])` as the canonical "be an orchestrator" entry point. The call MUST create the orchestrator row (idempotent on name), the coordinator role under it (idempotent on (orchestrator, role_name) when the kind matches), a binding row tying the calling argus task to that coordinator role, AND mirror `meta:hera.role=coordinator` to the bound argus task. The call MUST reject if the calling argus task already has a live binding **to the orchestrator named in this call**; bindings to other orchestrators MUST NOT cause rejection.

#### Scenario: Fresh orchestrator and coordinator created

- **WHEN** `hera_new_orchestrator(cwd=$PWD, name="foo", coordinator_role_name="coord", mission="ship F", constraints="land by friday")` is called from an unbound argus task
- **THEN** hera MUST create orchestrator `foo`, create a coordinator role `coord` with the given mission and constraints, insert a live binding to the calling task, AND PUT `{key: "role", value: "coordinator"}` to the bound task's `/api/tasks/{id}/meta` endpoint

#### Scenario: Existing orchestrator with no live coordinator binding resumed

- **WHEN** `hera_new_orchestrator` is called with a `name` that already exists AND the matching coordinator role has no live binding
- **THEN** hera MUST reuse the existing orchestrator + role rows AND create a new binding tying the calling task to that role; the response payload MUST report `created: false`

#### Scenario: Coordinator already live elsewhere

- **WHEN** `hera_new_orchestrator` is called naming an existing orchestrator + coordinator role whose binding is currently live in another argus task
- **THEN** hera MUST return `isError: true` directing the operator to resume from that worktree via `hera_join`; no new binding MUST be created

#### Scenario: Calling task already bound to the named orchestrator

- **WHEN** `hera_new_orchestrator(cwd=$PWD, name="foo", ...)` is called from an argus task that already has a live binding to orchestrator `foo`
- **THEN** hera MUST return `isError: true` directing the operator to resume the existing role via `hera_join(cwd, orchestrator="foo")` instead

#### Scenario: Calling task bound to a different orchestrator

- **WHEN** `hera_new_orchestrator(cwd=$PWD, name="bar", ...)` is called from an argus task that already has a live binding to orchestrator `foo` (a different name)
- **THEN** hera MUST proceed with the bootstrap and create a new binding to `bar`; the existing `foo` binding MUST remain live and unchanged

### Requirement: Worker and freelance attach via `hera_join`

The system SHALL allow an existing argus task in any project to attach itself to an existing orchestrator post-hoc by calling `hera_join` with `orchestrator=<name>`, `role_name=<self-named>`, `kind="worker"` or `kind="freelance"`, and optional `mission`, `constraints`, `status`. Hera MUST create the role row + binding row atomically AND mirror `meta:hera.role=<kind>` to the bound argus task. The orchestrator named MUST already exist. Kind `coordinator` is NOT accepted by `hera_join`; bootstrap a coordinator via `hera_new_orchestrator`. The call MUST reject if the calling argus task already has a live binding **to the orchestrator named in this call**; bindings to other orchestrators MUST NOT cause rejection.

#### Scenario: Freelance attach with all attributes

- **WHEN** `hera_join(cwd=$PWD, orchestrator="foo", role_name="refactor-sidebar", kind="freelance", mission="...", constraints="...", status="working")` is invoked from a worktree whose argus task has no prior hera binding
- **THEN** hera MUST create a freelance role under `foo`, insert a binding row tying the calling task to it, populate mission/constraints from the call args, set role_status to `working`, mirror `meta:hera.role=freelance` to the bound argus task, AND return the role identity in the tool response

#### Scenario: Freelance attach referencing unknown orchestrator

- **WHEN** `hera_join` is called with `orchestrator="does-not-exist"` and `kind="freelance"`
- **THEN** hera MUST return `isError: true` with content explaining the orchestrator does not exist; no role or binding row MUST be created

#### Scenario: Freelance attach conflicts with existing role kind

- **WHEN** `hera_join` is called with `kind="freelance"` and `(orchestrator, role_name)` already exists with `kind="worker"`
- **THEN** hera MUST return `isError: true` with content explaining the existing role has a different kind; no row MUST be modified

#### Scenario: `kind=coordinator` rejected by `hera_join`

- **WHEN** `hera_join` is called with `kind="coordinator"`
- **THEN** hera MUST return `isError: true` directing the caller to use `hera_new_orchestrator` for coordinator bootstrap

#### Scenario: Worker attach when task is already bound to the same orchestrator

- **WHEN** `hera_join(cwd=$PWD, orchestrator="foo", role_name="impl-2", kind="worker", ...)` is called from a task that already has a live binding to orchestrator `foo`
- **THEN** hera MUST return `isError: true` directing the operator to resume via `hera_join(cwd, orchestrator="foo")` instead; no new role or binding row MUST be created

#### Scenario: Worker attach when task is bound to a different orchestrator

- **WHEN** `hera_join(cwd=$PWD, orchestrator="bar", role_name="coord", kind="worker", ...)` is called from a task that already has a live binding to orchestrator `foo` (a different name)
- **THEN** hera MUST proceed with the attach and create a new binding to a `coord` worker role under `bar`; the existing `foo` binding MUST remain live and unchanged

### Requirement: Bare `hera_join` claims existing binding

The system SHALL support `hera_join(cwd)` and `hera_join(cwd, orchestrator=<name>)` (with no `role_name` and no `kind`) as the re-incarnation claim. Hera MUST resolve the cwd to an argus task and:

- With no `orchestrator`: if the task has exactly one live binding, return that binding's role identity. If the task has zero live bindings, return `isError: true` with content suggesting either `hera_join` with explicit `orchestrator`, `role_name`, `kind` (for worker/freelance attach) or `hera_new_orchestrator` (to bootstrap a new orchestrator). If the task has two or more live bindings, return `isError: true` whose content lists each binding's `(orchestrator, role_name, kind)` triple and directs the operator to specify which to claim via `hera_join(cwd, orchestrator=X)`.
- With explicit `orchestrator` and no other attach args: look up the binding for that orchestrator on the calling task and return its role identity. If no binding exists for that orchestrator, return `isError: true` with content suggesting `hera_join` with explicit `orchestrator`, `role_name`, `kind` to attach.

#### Scenario: Re-incarnation claim succeeds (single binding)

- **WHEN** `hera_join(cwd=$PWD)` is called from a worktree whose argus task is bound to exactly one role (e.g., via auto-adoption)
- **THEN** hera MUST return the role's identity and a recent-inbox-count summary without modifying any database rows

#### Scenario: Bare join with no existing binding fails informatively

- **WHEN** `hera_join(cwd=$PWD)` is called from a worktree whose argus task has no hera binding
- **THEN** hera MUST return `isError: true` with content suggesting either `hera_join` with explicit `orchestrator`, `role_name`, `kind` (for worker/freelance attach) or `hera_new_orchestrator` (to bootstrap a new orchestrator)

#### Scenario: Bare join with multiple bindings requires orchestrator

- **WHEN** `hera_join(cwd=$PWD)` is called from a worktree whose argus task holds live bindings to orchestrators `foo` and `bar`
- **THEN** hera MUST return `isError: true`; the content MUST name both `foo` and `bar` and direct the operator to call `hera_join(cwd, orchestrator=...)`

#### Scenario: Claim binding for a specific orchestrator

- **WHEN** `hera_join(cwd=$PWD, orchestrator="bar")` is called from a worktree whose argus task holds live bindings to `foo` and `bar`
- **THEN** hera MUST return the `bar` binding's role identity without modifying any database rows AND the `foo` binding MUST remain live and unchanged

#### Scenario: Claim binding for an orchestrator with no binding on this task

- **WHEN** `hera_join(cwd=$PWD, orchestrator="baz")` is called from a worktree whose argus task has no binding to `baz`
- **THEN** hera MUST return `isError: true` with content suggesting the attach signature (`hera_join` with `role_name` and `kind`)

### Requirement: Default message routing for worker and freelance senders

The system SHALL route `hera_send` calls from a worker or freelance role that omit the `to` parameter to the coordinator role of the same orchestrator. The coordinator role MUST exist for the send to succeed. When the calling task holds multiple live bindings, the caller MUST supply an `orchestrator` parameter to disambiguate which binding's role is the sender; when the calling task holds exactly one live binding, the `orchestrator` parameter MAY be omitted and the single binding's role is used.

#### Scenario: Worker without `to` routes to coordinator (single binding)

- **WHEN** a worker role under orchestrator `foo` calls `hera_send(cwd=$PWD, body="...")` with no `to` and no `orchestrator`, from a task that is bound only to that worker role
- **THEN** the message row's `to_role_id` MUST be the coordinator role's id under orchestrator `foo`, AND the injection path MUST be triggered

#### Scenario: Multi-binding sender without `orchestrator` rejected

- **WHEN** `hera_send(cwd=$PWD, body="...")` is called from a task with live bindings to two or more orchestrators and no `orchestrator` parameter
- **THEN** hera MUST return `isError: true`; the content MUST list each binding's orchestrator name AND direct the caller to re-invoke with `orchestrator=<name>`; no message row MUST be persisted

#### Scenario: Multi-binding sender with `orchestrator` resolves the correct binding

- **WHEN** `hera_send(cwd=$PWD, body="...", orchestrator="bar")` is called from a task with live bindings to `foo` and `bar`
- **THEN** the sender role used MUST be the `bar`-binding's role; default-route (no `to`) MUST go to the coordinator of `bar`, NOT the coordinator of `foo`

### Requirement: Coordinator senders must supply an explicit recipient

The system SHALL reject `hera_send` calls from a coordinator role that omit the `to` parameter. The coordinator's normal channel to the human is the coordinator's own Claude pane; messages emitted via `hera_send` MUST target a specific worker or freelance role by name. The same multi-binding disambiguation rule applies: when the calling task holds multiple live bindings, the `orchestrator` parameter is required to identify which coordinator role is the sender.

#### Scenario: Coordinator without `to` is rejected

- **WHEN** the coordinator role under orchestrator `foo` calls `hera_send(cwd=$PWD, body="...")` with no `to`
- **THEN** the call MUST return `isError: true` with content explaining that coordinator messages require an explicit recipient, AND no message row MUST be persisted

#### Scenario: Multi-binding coordinator with `orchestrator` and `to` succeeds

- **WHEN** a task holds live bindings to coordinator roles in both `foo` and `bar` AND calls `hera_send(cwd=$PWD, orchestrator="bar", to="impl-1", body="...")`
- **THEN** the message MUST be persisted with `from_role_id` set to the `bar` coordinator's role id AND `to_role_id` set to role `impl-1` under `bar`

### Requirement: Auto-adopt coordinator-spawned worker tasks

The system SHALL adopt a new argus task as a worker role of orchestrator X when both: (1) the new task has a `link.created` event whose parent is a task with exactly one live coordinator binding under orchestrator X, AND (2) the new task has `meta:hera.role=worker`. Tasks meeting only one of these conditions MUST NOT be adopted. When the parent task has zero coordinator bindings (or two or more coordinator bindings), adoption MUST be skipped and the event MUST be logged at INFO level with the parent's coordinator-binding count and the offending task ids.

#### Scenario: Both conditions met, task adopted

- **WHEN** argus emits `task.created` for task `T2` followed by `link.created` (`child=T2, parent=T1`) where `T1` has exactly one live coordinator binding under orchestrator `foo`, AND `T2`'s `meta:hera.role=worker`
- **THEN** hera MUST create a new worker role under orchestrator `foo` AND insert a binding row linking the role to `T2`

#### Scenario: Parent link present, meta absent — not adopted

- **WHEN** argus emits `link.created` (`child=T3, parent=T1`) where `T1` has a hera coordinator binding, but `T3` has no `meta:hera.role`
- **THEN** hera MUST NOT create a role or binding for `T3` AND MUST log the skipped adoption with the missing meta key

#### Scenario: Meta present, parent link absent — not adopted

- **WHEN** argus emits `task.created` for `T4` with `meta:hera.role=worker` but no `link.created` event names `T4` as a child of any hera coordinator binding
- **THEN** hera MUST NOT create a role or binding for `T4`

#### Scenario: Parent has multiple coordinator bindings — adoption skipped

- **WHEN** argus emits `link.created` (`child=T5, parent=T1`) where `T1` has live coordinator bindings under both `foo` AND `bar`, AND `T5`'s `meta:hera.role=worker`
- **THEN** hera MUST NOT create a role or binding for `T5` AND MUST log the skipped adoption at WARN level identifying both orchestrators; the operator may attach `T5` explicitly via `hera_join`

#### Scenario: Parent has a worker binding and a coordinator binding — adopt under the coordinator

- **WHEN** argus emits `link.created` (`child=T6, parent=T1`) where `T1` has a worker binding under `foo` AND a coordinator binding under `bar`, AND `T6`'s `meta:hera.role=worker`
- **THEN** hera MUST create a worker role under `bar` (NOT `foo`) AND insert a binding row linking the role to `T6`

## ADDED Requirements

### Requirement: Bindings are unique per (argus_task, orchestrator), not per argus_task

The system SHALL allow a single argus task to hold multiple simultaneous live bindings, one per orchestrator, but MUST enforce that no two live bindings share both `argus_task_id` AND `orchestrator_id`. The same constraint MUST hold for `worktree_path` AND `orchestrator_id`. The role-side uniqueness MUST be preserved: no two live bindings MAY share `role_id` (a role is incarnated at most once at any time).

The constraints SHALL be enforced at the storage layer via SQLite partial unique indexes scoped to `ended_at IS NULL`, AND at the application layer by every attach handler (which pre-checks before INSERT). The combination closes the TOCTOU race between two concurrent attach calls that would otherwise both observe "no existing binding" and both INSERT successfully.

#### Scenario: Two live bindings on the same task in different orchestrators

- **WHEN** argus task `T1` has a live binding to a worker role in orchestrator `foo` AND `hera_new_orchestrator(cwd=$PWD-of-T1, name="bar", coordinator_role_name="coord")` is invoked
- **THEN** the call MUST succeed AND the bindings table MUST contain two live rows for `T1`: one with `orchestrator_id` matching `foo`, one with `orchestrator_id` matching `bar`

#### Scenario: Two live bindings on the same task in the same orchestrator rejected

- **WHEN** argus task `T1` has a live binding to a worker role in orchestrator `foo` AND a second attach call attempts to create another live binding on `T1` with the same orchestrator (race or duplicate call)
- **THEN** the second binding MUST be rejected — at the application layer by the pre-check in the attach handler, AND at the storage layer by the `bindings_live_unique_task_orch` partial unique index in case the pre-check is bypassed

#### Scenario: Two live bindings on the same worktree in different orchestrators

- **WHEN** an argus task whose worktree path is `/p/q` has a live binding to a role in orchestrator `foo` AND a new binding is created tying the same worktree to a role in orchestrator `bar`
- **THEN** both bindings MUST coexist live AND the `bindings_live_unique_worktree_orch` partial unique index MUST be satisfied (different `orchestrator_id` values for the same `worktree_path`)

### Requirement: `bindings` table carries `orchestrator_id`

The system SHALL store an `orchestrator_id` column on every `bindings` row, denormalized from the role's `orchestrator_id`. The column SHALL be populated at INSERT time by the `BindingsDAO.Create` path (the handler resolves the role first, then passes its `OrchestratorID` into the input). A schema migration SHALL add the column to existing databases and backfill it from `roles.orchestrator_id` for every existing row.

#### Scenario: Migration backfills existing rows

- **WHEN** a database predating this change holds live bindings AND the daemon with the new migration is started
- **THEN** every row in `bindings` MUST have a non-NULL `orchestrator_id` after migration; the value of `orchestrator_id` for each row MUST equal `(SELECT orchestrator_id FROM roles WHERE roles.id = bindings.role_id)`

#### Scenario: New bindings populate orchestrator_id

- **WHEN** any attach handler (`hera_new_orchestrator`, `hera_join` worker attach, `hera_join` freelance attach, auto-adopt) creates a new binding row
- **THEN** the row's `orchestrator_id` MUST equal the role's `orchestrator_id`

### Requirement: Optional `orchestrator` parameter on caller-resolution tools

The system SHALL accept an optional `orchestrator` string parameter on `hera_send`, `hera_inbox`, `hera_mark_read`, and `hera_status`. The parameter resolves the caller's binding when the calling task holds multiple live bindings; it MAY be omitted when the calling task holds exactly one live binding.

The resolution rules are:

1. If `orchestrator` is supplied: look up the calling task's live binding under that orchestrator. If no such binding exists, return `isError: true` with content explaining the task is not bound to that orchestrator.
2. If `orchestrator` is empty AND the calling task has exactly one live binding: use that binding.
3. If `orchestrator` is empty AND the calling task has zero live bindings: return `isError: true` with the existing "not bound" message.
4. If `orchestrator` is empty AND the calling task has two or more live bindings: return `isError: true`; the content MUST list each binding's orchestrator name AND direct the caller to re-invoke with `orchestrator=<name>`.

The tool registration `input_schema` SHALL declare `orchestrator` as an optional string for each of the four tools.

#### Scenario: Single-binding caller omits orchestrator

- **WHEN** `hera_inbox(cwd=$PWD)` is called from a task with exactly one live binding
- **THEN** the call MUST succeed and return the inbox for the resolved binding's role

#### Scenario: Multi-binding caller without orchestrator rejected

- **WHEN** `hera_inbox(cwd=$PWD)` is called from a task with two or more live bindings AND no `orchestrator` parameter
- **THEN** the call MUST return `isError: true`; the content MUST list each binding's orchestrator name

#### Scenario: Multi-binding caller with orchestrator resolves correctly

- **WHEN** `hera_status(cwd=$PWD, status="working", orchestrator="bar")` is called from a task with live bindings to `foo` and `bar`
- **THEN** the `role_status` row for the `bar`-binding's role MUST be updated to `working` AND the `foo`-binding's role status MUST remain unchanged

#### Scenario: Caller specifies an orchestrator they have no binding to

- **WHEN** `hera_send(cwd=$PWD, body="...", orchestrator="baz")` is called from a task with no live binding to `baz`
- **THEN** the call MUST return `isError: true` with content stating the task is not bound to `baz`

### Requirement: Tool input schemas declare optional `orchestrator`

The system SHALL declare the `orchestrator` field in the `input_schema.properties` of the tool registrations for `hera_send`, `hera_inbox`, `hera_mark_read`, and `hera_status`. The field MUST be typed as `string`, MUST NOT appear in the `required` array, and MUST carry a `description` explaining when it is required (multi-binding caller) and what it does (selects which of the caller's live bindings is the sender's identity for this call).

#### Scenario: Registered schema exposes orchestrator field

- **WHEN** an HTTP GET against argus's MCP registry retrieves any of the four tools (`hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`)
- **THEN** the returned `input_schema.properties` MUST contain an `orchestrator` entry of type `string` with a non-empty `description` AND the `required` array MUST NOT contain `orchestrator`
