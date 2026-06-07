# hera-coordination Specification

## Purpose
TBD - created by archiving change hera-v1. Update Purpose after archive.
## Requirements
### Requirement: Role identity outlives argus task lifecycle

The system SHALL persist role identity, `prompt`, and accumulated history (messages, role status) across argus task lifecycle events. Archiving an argus task that incarnates a role MUST end that role's current binding without deleting the role row or its associated messages. The role's `prompt` (the only free-form field) MUST survive archive/resurrect intact.

#### Scenario: Coordinator task archived, role and prompt survive

- **WHEN** a coordinator role with `prompt="ship the thing"` is bound to argus task `T1` and `T1` is archived
- **THEN** hera MUST set the binding's `ended_at` and `end_reason` columns AND leave the role row with `prompt="ship the thing"` intact

#### Scenario: Same role rebound across multiple incarnations

- **WHEN** a role's current binding is ended and a fresh argus task `T2` is created for the same role
- **THEN** hera MUST insert a new binding row with `(role_id, T2, started_at)` while preserving the previous binding's history

#### Scenario: Multi-binding task archived, every binding ends

- **WHEN** an argus task `T1` holds live bindings to roles in orchestrators `foo` and `bar` and `T1` is archived
- **THEN** hera MUST end both bindings with `end_reason = "argus_archived"` and leave both role rows, their messages, and their status rows intact

### Requirement: Seven MCP tools exposed under the `hera_` prefix

The system SHALL register seven MCP tools with argus when the daemon starts: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`. Each tool MUST be force-prefixed `hera_` per the substrate's tool-name enforcement. Each tool's input schema MUST declare `cwd` as a required input parameter.

Note: v1 shipped six tools ("Six MCP tools, no more no less"). `hera_spawn_worker` is a sanctioned v1.x addition for coordinator-initiated born-bound worker spawning.

The settings_save handler registered at the hera callback listener is NOT counted as one of the seven MCP tools; it is a separate callback type addressed via the settings-section registration, not the MCP tool registration.

#### Scenario: Seven MCP tools registered on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** an HTTP GET against argus's MCP registry MUST show seven tools scoped to `hera`, with names `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`

#### Scenario: Settings_save callback present but not in MCP tool list

- **WHEN** the hera daemon completes startup successfully
- **THEN** the settings_save route on the callback listener MUST be reachable AND MUST NOT appear in the MCP tool registry; it MUST appear instead as a registered settings-section's callback under the settings-section registry

#### Scenario: Tool call with no `cwd` rejected

- **WHEN** a hera MCP tool is invoked with no `cwd` parameter
- **THEN** the callback MUST return `isError: true` with a content block explaining that `cwd` is required

#### Scenario: Tool call with unknown `cwd` rejected

- **WHEN** a hera MCP tool is invoked with a `cwd` that does not match any known argus task's worktree
- **THEN** the callback MUST return `isError: true` with a content block explaining that the cwd does not map to a tracked argus task

### Requirement: Message delivery modes

The system SHALL persist `delivery_mode` on every `messages` row to record how the message was (or was not) delivered to the recipient's PTY. The enumerated delivery modes are `pending` (initial state before delivery is attempted), `idle_submit`, `busy_buffer`, and `queued_no_binding`. Each mode corresponds to a specific delivery path described in the requirements below.

#### Scenario: New messages start in pending

- **WHEN** `hera_send` inserts a message row before invoking the injector
- **THEN** the row MUST have `delivery_mode = "pending"`; the final mode is written via `Messages.SetDelivered` once delivery is resolved

### Requirement: Messages auto-submitted when recipient is idle

The system SHALL inject `<formatted-body>\r` into the recipient's argus task PTY via `POST /api/tasks/{id}/input` when ALL of the following hold:

1. The recipient's bound task has been in `session.idle` state for at least `Config.IdleDebounce`.
2. `Config.AutoInjectEnabled` is `true`.

The trailing byte MUST be CR (`\r`, byte 0x0D), not LF. Claude Code's TUI puts the PTY into raw mode, so termios does NOT translate CR to LF on input. The keyboard's Return key emits CR, and CR is the only byte the input handler treats as submit. LF would land in the recipient's input buffer as a visible newline character and would NOT trigger submit.

#### Scenario: Recipient idle, auto-inject enabled, message auto-submits

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥ the configured debounce AND `AutoInjectEnabled = true`
- **THEN** hera MUST POST the formatted body terminated by `\r` (single CR, byte 0x0D — NOT `\n`, NOT `\r\n`) to argus's input endpoint AND record `delivery_mode = "idle_submit"` on the message row

#### Scenario: Recipient idle, auto-inject disabled, message busy-buffers

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥ the configured debounce AND `AutoInjectEnabled = false`
- **THEN** hera MUST POST the formatted body WITHOUT a trailing terminator AND record `delivery_mode = "busy_buffer"` on the message row

### Requirement: Messages buffered when recipient is not idle

The system SHALL inject `<formatted-body>` without a trailing newline into the recipient's argus task PTY when the recipient is not in the idle state. The text remains in the input buffer for the human user to review and submit.

#### Scenario: Recipient busy, message lands in input buffer

- **WHEN** `hera_send` is called with a recipient whose bound task is not idle (no `session.idle` event yet, or `session.started`/`session.exited` more recent than the last `session.idle`)
- **THEN** hera MUST POST the formatted body without `\n` AND record `delivery_mode = "busy_buffer"` on the message row

### Requirement: Messages queued when recipient has no live binding

The system SHALL persist messages addressed to a real recipient role whose current binding has ended (the role exists but has no live argus task) with `delivery_mode = "queued_no_binding"` and no PTY injection. This is the role-as-identity case: the recipient is a known role whose next incarnation will eventually need access to the message. A drain worker that delivers queued messages on next-binding is a v1.1 follow-up; v1 only persists the queue.

#### Scenario: Recipient role exists, no live binding

- **WHEN** `hera_send` is called with a recipient role whose current binding is ended (`Bindings.GetLiveByRole` returns `ErrNotFound`)
- **THEN** hera MUST persist the message with `delivery_mode = "queued_no_binding"` AND NOT POST anything to argus's input endpoint

### Requirement: Injected messages identify sender

The system SHALL prefix every injected message body with `[hera from <sender-role-name>] ` so the recipient agent can identify the source role without an additional tool call.

#### Scenario: Injected body carries sender prefix

- **WHEN** hera injects a message from role `foo-coordinator` with body `"please review"`
- **THEN** the bytes posted to argus's input endpoint MUST be `[hera from foo-coordinator] please review` (plus `\r` in the idle-submit case, no terminator in the busy-buffer case)

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

### Requirement: Auto-adopt copies prompt from task meta

The system SHALL read `meta:hera.prompt` from the new task's metadata at adoption time and populate the role row's `prompt` column from that value. `meta:hera.mission` and `meta:hera.constraints` are no longer recognized. The `prompt` key MUST be optional; absence MUST result in an empty-string `prompt` column.

#### Scenario: Prompt meta present

- **WHEN** an auto-adopted task has `meta:hera.prompt="implement schema migration"`
- **THEN** the created role row's `prompt` column MUST contain `"implement schema migration"` verbatim

#### Scenario: Prompt meta absent

- **WHEN** an auto-adopted task has `meta:hera.role=worker` but no `meta:hera.prompt`
- **THEN** the created role row's `prompt` column MUST be an empty string (not NULL)

### Requirement: New orchestrator bootstrap via `hera_new_orchestrator`

The system SHALL provide `hera_new_orchestrator(cwd, name, coordinator_role_name, [prompt])` as the canonical "be an orchestrator" entry point. The tool MUST accept a single optional `prompt` parameter (free-form prose) instead of the previous `mission` and `constraints` pair. The call MUST create the orchestrator row (idempotent on name), the coordinator role under it (idempotent on (orchestrator, role_name) when the kind matches), a binding row tying the calling argus task to that coordinator role, AND mirror `meta:hera.role=coordinator` to the bound argus task. The call MUST reject if the calling argus task already has a live binding **to the orchestrator named in this call**; bindings to other orchestrators MUST NOT cause rejection.

#### Scenario: Fresh orchestrator and coordinator created

- **WHEN** `hera_new_orchestrator(cwd=$PWD, name="foo", coordinator_role_name="coord", prompt="ship F by friday")` is called from an unbound argus task
- **THEN** hera MUST create orchestrator `foo`, create a coordinator role `coord` with `prompt="ship F by friday"`, insert a live binding to the calling task, AND PUT `{key: "role", value: "coordinator"}` to the bound task's `/api/tasks/{id}/meta` endpoint

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

The system SHALL allow an existing argus task in any project to attach itself to an existing orchestrator post-hoc by calling `hera_join` with `orchestrator=<name>`, `role_name=<self-named>`, `kind="worker"` or `kind="freelance"`, and optional `prompt`, `status`. Hera MUST create the role row + binding row atomically AND mirror `meta:hera.role=<kind>` to the bound argus task. The orchestrator named MUST already exist. Kind `coordinator` is NOT accepted by `hera_join`; bootstrap a coordinator via `hera_new_orchestrator`. The tool MUST NOT accept `mission` or `constraints` parameters. The call MUST reject if the calling argus task already has a live binding **to the orchestrator named in this call**; bindings to other orchestrators MUST NOT cause rejection.

#### Scenario: Freelance attach with all attributes

- **WHEN** `hera_join(cwd=$PWD, orchestrator="foo", role_name="refactor-sidebar", kind="freelance", prompt="...", status="working")` is invoked from a worktree whose argus task has no prior hera binding
- **THEN** hera MUST create a freelance role under `foo`, insert a binding row tying the calling task to it, populate `prompt` from the call arg, set role_status to `working`, mirror `meta:hera.role=freelance` to the bound argus task, AND return the role identity in the tool response

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

### Requirement: Roles live in argus projects; orchestrators do not

The system SHALL record `argus_project` on the role row at first binding and preserve it across subsequent incarnations. The orchestrators table MUST NOT carry an `argus_project` column. Multiple roles under the same orchestrator MAY have different `argus_project` values.

#### Scenario: Orchestrator with roles in different projects

- **WHEN** orchestrator `foo` has role `coordinator` in argus project `argus` and role `f2-impl` in argus project `hera`
- **THEN** both roles MUST coexist under orchestrator `foo` with their respective `argus_project` values preserved

#### Scenario: Role's argus_project preserved across incarnation

- **WHEN** a role's first binding was created in argus project `foo-frontend` and that binding ends, then `hera resume` creates a new binding for the same role
- **THEN** the new binding MUST be in argus project `foo-frontend` (the role's stored `argus_project`), regardless of which worktree the user is currently in

### Requirement: Stricter rule on auto-adoption logged

The system SHALL log at INFO level every event-stream sequence that would have triggered auto-adoption but was rejected due to missing or non-worker `meta:hera.role`. The log entry MUST include the new task's id, the parent task's id, and the missing or unexpected meta key.

#### Scenario: Adoption skipped (meta absent)

- **WHEN** a `link.created` event names a child task that has no `meta:hera.role`, with parent bound to a hera coordinator
- **THEN** hera MUST emit a log line at INFO level identifying the skipped child task id, the parent task id, and the missing meta key

#### Scenario: Adoption skipped (meta value not worker)

- **WHEN** a `link.created` event names a child task whose `meta:hera.role` is set to a value other than `worker`, with parent bound to a hera coordinator
- **THEN** hera MUST emit a log line at INFO level identifying the skipped child task id, the parent task id, the unexpected value, and the meta key in question

### Requirement: MCP tool registrations heartbeated and unregistered on shutdown

The system SHALL re-POST each of its six tool registrations to argus on a 5-minute cadence to stay within the substrate's 10-minute idle sweep. On graceful shutdown (SIGINT/SIGTERM), hera MUST DELETE each registered tool via `DELETE /api/mcp/tools/{name}` before exiting.

#### Scenario: Heartbeat keeps tools registered

- **WHEN** the hera daemon has been running for >5 minutes
- **THEN** hera MUST have re-POSTed each tool registration at least once since startup

#### Scenario: Graceful shutdown unregisters tools

- **WHEN** the hera daemon receives SIGTERM and shuts down cleanly
- **THEN** hera MUST issue DELETE requests for all six tool names before the process exits

### Requirement: Event stream cursor persisted and replayed

The system SHALL persist the last-seen event id in the `event_cursor` table and pass it as the `since=<id>` query param when reconnecting to `/api/events/stream`. On receipt of a `resync` event (cursor older than argus's retained ring), hera MUST snapshot the current task list via `GET /api/tasks` and reconcile its bindings before resuming the stream.

#### Scenario: Restart resumes from cursor

- **WHEN** the daemon is restarted with `last_seen_event_id = 1234` in the cursor table
- **THEN** the SSE subscription URL MUST include `since=1234` on reconnect

#### Scenario: Resync triggers task snapshot

- **WHEN** hera receives a `resync` event from argus
- **THEN** hera MUST call `GET /api/tasks` and mark any bindings whose argus task no longer exists as ended, before resuming live event processing

### Requirement: Idle gate requires sustained `session.idle` state

The system SHALL treat a bound argus task as eligible for auto-submit only when its most recent session event is `session.idle` AND that event was emitted at least `Config.IdleDebounce` ago. Any `session.started` or `session.exited` event more recent than the latest `session.idle` MUST cause the task to fall out of the idle-eligible set. The debounce duration MUST be reloadable at runtime via the settings save handler (calling `Tracker.SetDebounce`).

A debounce of zero seconds means "any `session.idle` event makes the task immediately eligible, gated only by no more-recent `session.started`/`session.exited`."

#### Scenario: Idle for less than configured debounce

- **WHEN** the configured debounce is 2 seconds AND `session.idle` for task `T1` fired 1 second ago
- **THEN** `T1` MUST NOT be treated as idle for injection purposes; messages addressed to `T1`'s role MUST be delivered in busy_buffer mode

#### Scenario: Idle for at least configured debounce

- **WHEN** the configured debounce is 2 seconds AND `session.idle` for task `T1` fired 3 seconds ago AND no `session.started` / `session.exited` has fired since
- **THEN** `T1` MUST be treated as idle; messages addressed to `T1`'s role MUST be delivered in idle_submit mode

#### Scenario: Session started after idle

- **WHEN** `session.idle` for `T1` fired at time X, then `session.started` for `T1` fired at time X+1
- **THEN** `T1` MUST NOT be treated as idle until a new `session.idle` event fires AND at least the configured debounce elapses without intervening session events

#### Scenario: Debounce hot-reload changes behavior immediately

- **WHEN** the configured debounce is 2 seconds AND `session.idle` for task `T1` fired 1.5 seconds ago AND a settings save changes the debounce to 1 second
- **THEN** an immediately-following `Tracker.IsIdle(T1)` call MUST return `true` (event is older than the new 1-second debounce)

#### Scenario: Zero debounce, idle event makes task immediately eligible

- **WHEN** the configured debounce is 0 seconds AND `session.idle` for task `T1` fired any time ago with no subsequent `session.started`/`session.exited`
- **THEN** `Tracker.IsIdle(T1)` MUST return `true`

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

### Requirement: Tool inputs and outputs documented

The system's six MCP tool registrations SHALL each include a non-empty `description` field and a JSON Schema `input_schema` describing every parameter (with types, required-flags, and a brief description per field).

#### Scenario: Tool registration carries description and schema

- **WHEN** hera POSTs a tool registration to argus's `/api/mcp/tools`
- **THEN** the registration body MUST include a `description` field of length ≥10 characters AND an `input_schema` object whose `properties` covers every documented parameter of that tool

### Requirement: Role metadata mirrored to argus task_meta

The system SHALL write `meta:hera.role=<kind>` to the bound argus task's metadata whenever a binding is created (via `hera_new_orchestrator`, `hera_join` freelance/worker attach, or auto-adopt). The write MUST use hera's scope token; the substrate's auto-namespacing MUST derive the `hera` namespace from the scope.

The role meta write on binding creation is **best-effort**: a transient failure of the argus PUT MUST NOT undo the binding row or surface as an error to the caller. The binding row is the source of truth for "this role is incarnated as this argus task"; the meta mirror is an observability convenience for other plugins. A failed mirror is recoverable on next state change.

The thread_status meta write on `hera_status` is also best-effort: see the next requirement.

#### Scenario: Role meta written on binding (auto-adopt)

- **WHEN** a new binding is created for a worker role on argus task `T2` by the auto-adopt event handler
- **THEN** hera MUST PUT `{key: "role", value: "worker"}` to `/api/tasks/T2/meta` using its scope token

#### Scenario: Role meta written on binding (freelance attach)

- **WHEN** a new binding is created for a freelance role on argus task `T3` by `hera_join`
- **THEN** hera MUST PUT `{key: "role", value: "freelance"}` to `/api/tasks/T3/meta` using its scope token

#### Scenario: Role meta written on binding (coordinator bootstrap)

- **WHEN** a new binding is created for a coordinator role on argus task `T4` by `hera_new_orchestrator`
- **THEN** hera MUST PUT `{key: "role", value: "coordinator"}` to `/api/tasks/T4/meta` using its scope token

#### Scenario: Role meta write failure does not undo binding

- **WHEN** the argus PUT for `meta:hera.role` returns an error (e.g., transient network failure) after the binding row is committed
- **THEN** the binding row MUST remain; the tool call MUST return success; the caller MAY observe a `meta_mirrored: false` (or equivalent) field on responses where it is exposed

### Requirement: Thread status meta mirror is best-effort

The system SHALL update the `role_status` row whenever `hera_status` is called with a valid status. The system SHALL also write `meta:hera.thread_status=<status>` to the bound argus task's metadata, but this argus write is **best-effort**: a failure (e.g., transient argus unavailability) MUST NOT cause the tool call to return an error. The local `role_status` update is the source of truth; the argus meta is a mirror. The handler MAY surface mirror-success information to the caller (e.g., `meta_mirrored: true|false` on the response payload).

#### Scenario: Status mirror succeeds

- **WHEN** `hera_status(cwd=$PWD, status="blocked")` is called and resolves to worker role `f2-impl`, AND argus accepts the meta PUT
- **THEN** hera MUST update `role_status` for `f2-impl` AND PUT `{key: "thread_status", value: "blocked"}` to the bound task's `/api/tasks/{id}/meta` endpoint AND return success

#### Scenario: Status mirror fails, status update succeeds

- **WHEN** `hera_status` is called with a valid status AND argus returns an error on the meta PUT
- **THEN** hera MUST update `role_status` locally AND return success to the caller; the response MAY include a flag indicating the mirror did not land

### Requirement: Scope token loaded from filesystem; missing token aborts startup

The system SHALL read its argus scope token from `~/.hera/api-token` at daemon startup. The file MUST contain a single token string. If the file is missing or empty, the daemon MUST exit with status code 1 and a stderr message instructing the user to run `argus token mint --scope hera` and write the result to `~/.hera/api-token`.

#### Scenario: Token file present, daemon starts

- **WHEN** `~/.hera/api-token` contains a valid scope token and the daemon is started
- **THEN** hera MUST proceed through the rest of its startup sequence (open DB, connect to argus, subscribe to events, register tools)

#### Scenario: Token file missing

- **WHEN** `~/.hera/api-token` does not exist and the daemon is started
- **THEN** hera MUST print an instructional error message to stderr AND exit with status 1

#### Scenario: Token file empty

- **WHEN** `~/.hera/api-token` exists but contains only whitespace
- **THEN** hera MUST print an instructional error message to stderr AND exit with status 1

### Requirement: Settings section registered with argus on startup

The system SHALL register exactly one settings-section with argus when the daemon starts, via `POST /api/plugins/settings/sections`. The section MUST have `type = "form"` and contain exactly two fields:

- `idle_debounce_seconds`: integer, `min = 0`, `max = 60`, default `2`.
- `auto_inject_enabled`: boolean, default `true`.

The registration MUST set the section's `callback_url` to hera's callback listener at the path that routes to the settings_save handler. The registration MUST carry the same per-session shared-secret auth header as the six MCP tool registrations.

#### Scenario: Exactly one settings-section registered on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** an HTTP GET against argus's settings-section registry MUST show exactly one section owned by hera, with `type = "form"` and the two fields above with the specified bounds and defaults

#### Scenario: Settings section heartbeated and unregistered on shutdown

- **WHEN** the hera daemon has been running for >5 minutes AND then receives SIGTERM and shuts down cleanly
- **THEN** hera MUST have re-POSTed the settings-section registration at least once during runtime AND MUST issue a DELETE for the settings-section before the process exits

### Requirement: Settings field descriptions explain impact

Each field in the registered settings-section SHALL carry a non-empty `description` string that documents BOTH:

1. What the field controls (plain-language identity).
2. The operational impact of changing its value — what happens with a lower value, a higher value, the minimum, and the maximum (or "on" vs "off" for booleans).

The description MUST be sufficient for an operator who has never read the hera source to make an informed setting change. Bare identity ("the idle debounce") is insufficient; the description MUST tell the operator what they trade by raising or lowering it.

#### Scenario: idle_debounce_seconds description covers low/high impact

- **WHEN** an HTTP GET against argus's settings-section registry retrieves hera's registered section
- **THEN** the `idle_debounce_seconds` field's `description` MUST mention BOTH the effect of a lower value (faster delivery, higher risk of submitting mid-burst) AND the effect of a higher value (more padding, slower delivery), AND name the meaning of `0` and `60`

#### Scenario: auto_inject_enabled description covers on/off impact

- **WHEN** an HTTP GET against argus's settings-section registry retrieves hera's registered section
- **THEN** the `auto_inject_enabled` field's `description` MUST describe BOTH what "on" does (auto-submit on idle) AND what "off" does (leave messages in the buffer for manual submit) AND name a concrete use case for "off"

### Requirement: Settings save handler persists values and applies them in-process

The system SHALL handle callbacks from argus's settings UI at hera's callback listener under the `settings_save` route. On a valid save the handler MUST:

1. Validate input: `idle_debounce_seconds` MUST be an integer in `[0, 60]`; `auto_inject_enabled` MUST be a boolean. Either field MAY be absent (partial save updates only the supplied keys).
2. Persist supplied values to the existing `config` table via `ConfigDAO.Set` using keys `idle_debounce_seconds` (value stringified int) and `auto_inject_enabled` (value `"true"` or `"false"`).
3. Apply the new values to the live components: `Tracker.SetDebounce(time.Duration(seconds) * time.Second)` and `Injector.SetAutoInjectEnabled(b)`.
4. Return a success response carrying the new effective values.

The handler MUST use the same constant-time auth check as the six MCP tool handlers. A request that fails auth MUST return `401` with no body (matching the existing tool callback contract).

#### Scenario: Valid save persists and hot-reloads

- **WHEN** the settings_save callback is invoked with `{ "idle_debounce_seconds": 3, "auto_inject_enabled": false }` and a valid auth header
- **THEN** the `config` table MUST contain rows `(idle_debounce_seconds, "3")` and `(auto_inject_enabled, "false")` AND a subsequent `Tracker.IsIdle` call for a task with `session.idle` 2.5 seconds ago MUST return `false` (because the debounce is now 3s) AND a subsequent `Injector.Inject` for an idle task MUST return `DeliveryBusyBuffer`

#### Scenario: Out-of-range debounce rejected, no rows written

- **WHEN** the settings_save callback is invoked with `{ "idle_debounce_seconds": 99 }`
- **THEN** the handler MUST return `isError: true` with a content block naming `idle_debounce_seconds` and the valid range `[0, 60]` AND the `config` table row for `idle_debounce_seconds` MUST be unchanged

#### Scenario: Non-boolean auto-inject rejected

- **WHEN** the settings_save callback is invoked with `{ "auto_inject_enabled": "maybe" }`
- **THEN** the handler MUST return `isError: true` with a content block naming `auto_inject_enabled` AND the `config` table row for `auto_inject_enabled` MUST be unchanged

#### Scenario: Partial save updates only supplied field

- **WHEN** the settings_save callback is invoked with `{ "idle_debounce_seconds": 5 }` and no `auto_inject_enabled` key
- **THEN** the `config` row for `idle_debounce_seconds` MUST be updated to `"5"` AND the `config` row for `auto_inject_enabled` MUST remain at its prior value (or absent, if never set)

### Requirement: Persisted settings override defaults on daemon start

The system SHALL, after opening the database and before instantiating the `Tracker` and `Injector`, read the two settings keys from the `config` table and overwrite `Config.IdleDebounce` and `Config.AutoInjectEnabled` from the persisted values. Missing keys MUST leave the corresponding `Config` field at its `Default()` value.

#### Scenario: Persisted debounce wins on restart

- **WHEN** the `config` table contains `(idle_debounce_seconds, "5")` AND the daemon is started
- **THEN** the `Tracker` instantiated by the daemon MUST be using a 5-second debounce, regardless of the compiled `Default()` value of 2 seconds

#### Scenario: Missing keys keep defaults

- **WHEN** the `config` table contains no row for `idle_debounce_seconds` AND the daemon is started
- **THEN** the `Tracker` MUST be using the `Default()` debounce of 2 seconds

#### Scenario: Corrupt persisted value aborts startup

- **WHEN** the `config` table contains `(idle_debounce_seconds, "not-an-int")` AND the daemon is started
- **THEN** the daemon MUST exit with a non-zero status and a stderr message naming the offending key and value

### Requirement: Auto-inject master switch gates idle-submit path

The system SHALL treat `Config.AutoInjectEnabled` as a master switch over the auto-submit branch of `Injector.Inject`. When `AutoInjectEnabled = false`, every message MUST be delivered in `busy_buffer` mode (formatted body, no trailing terminator) regardless of the recipient task's idle state. When `AutoInjectEnabled = true` (the default), v1 behavior is preserved: idle tasks get `idle_submit` (formatted body terminated by CR), non-idle tasks get `busy_buffer` (formatted body, no terminator).

#### Scenario: Auto-inject off, idle recipient still busy-buffers

- **WHEN** `AutoInjectEnabled = false` AND `hera_send` targets a worker whose bound task has been idle ≥ the configured debounce
- **THEN** the message MUST be persisted with `delivery_mode = "busy_buffer"` AND the bytes POSTed to argus's input endpoint MUST be the formatted body WITHOUT a trailing terminator

#### Scenario: Auto-inject toggles back to true, behavior restored

- **WHEN** `AutoInjectEnabled` transitions from `false` to `true` via a successful settings_save AND a subsequent `hera_send` targets an idle recipient
- **THEN** the message MUST be persisted with `delivery_mode = "idle_submit"` AND the bytes POSTed MUST include the trailing CR (`\r`)

### Requirement: Orchestrators and roles carry `archived_at` columns

The system SHALL add a nullable `archived_at` column (RFC3339 string) to both the `orchestrators` and `roles` tables. A NULL value MUST mean "active"; a non-NULL value MUST mean "archived at that timestamp". The migration MUST be additive and MUST NOT touch existing rows (they inherit NULL). An index MUST be present on each `archived_at` column to support `WHERE archived_at IS NULL` predicates used by the default list path.

#### Scenario: Migration runs on existing DB

- **WHEN** a daemon with the new migration is started against an existing hera DB whose `orchestrators` and `roles` tables predate the column
- **THEN** the migration MUST add `archived_at TEXT` (nullable) to both tables AND existing rows MUST have `archived_at IS NULL`

#### Scenario: Indexes present after migration

- **WHEN** the migration completes
- **THEN** `orchestrators` and `roles` MUST each have an index on the `archived_at` column

### Requirement: List* DAO methods default to active-only

The system SHALL, by default, filter `archived_at IS NOT NULL` rows out of every `List*` DAO method on `Orchestrators` and `Roles`. Callers MUST be able to opt in to inclusive listing via an explicit `IncludeArchived bool` parameter (or equivalent `ListInclusive` method). Internal lookups by primary key (e.g., `Get(id)`) MUST NOT filter on `archived_at` (archived rows are still resolvable when explicitly named).

#### Scenario: ListActive returns non-archived only

- **WHEN** the orchestrators table has rows with mixed `archived_at` values and the daemon calls the default `ListOrchestrators()` (no inclusive flag)
- **THEN** the result MUST contain only rows where `archived_at IS NULL`

#### Scenario: ListInclusive returns all rows

- **WHEN** the orchestrators table has rows with mixed `archived_at` values and the daemon calls `ListOrchestrators(IncludeArchived: true)` (or equivalent inclusive method)
- **THEN** the result MUST contain every row regardless of `archived_at` value

#### Scenario: Get by id resolves archived row

- **WHEN** an orchestrator row has `archived_at` set and the daemon calls `GetOrchestratorByID(id)`
- **THEN** the row MUST be returned (the archived flag MUST NOT cause a not-found)

### Requirement: Orchestrators may be renamed

The system SHALL support renaming an orchestrator via a DAO `RenameOrchestrator(id, newName)` method. The new name MUST be unique across non-archived orchestrators. The rename MUST NOT modify any other column on the orchestrator row, MUST NOT touch any role row, and MUST NOT affect any argus task name or worktree.

#### Scenario: Rename orchestrator updates only the name column

- **WHEN** `RenameOrchestrator(id=42, newName="bar")` is called and no other non-archived orchestrator is named `bar`
- **THEN** orchestrator id 42's `name` MUST be `bar` AND no other column on that row MUST be touched AND no role row MUST be modified AND no argus HTTP call MUST be issued

#### Scenario: Rename to existing non-archived name rejected

- **WHEN** `RenameOrchestrator(id=42, newName="foo")` is called and another non-archived orchestrator already has `name="foo"`
- **THEN** the method MUST return an error AND the orchestrator id 42's `name` column MUST be unchanged

#### Scenario: Rename to name of archived orchestrator allowed

- **WHEN** `RenameOrchestrator(id=42, newName="foo")` is called and an orchestrator named `foo` exists but has `archived_at IS NOT NULL`
- **THEN** the rename MUST succeed; the archived orchestrator's name MUST be unchanged (collision-free because the new name is in active scope and the existing one is archived)

### Requirement: Roles may be renamed within their orchestrator

The system SHALL support renaming a role via a DAO `RenameRole(id, newName)` method. The new name MUST be unique within the role's orchestrator across non-archived roles. The rename MUST NOT modify any other column, MUST NOT touch the orchestrator row, and MUST NOT affect the bound argus task's name or its worktree.

#### Scenario: Rename role updates only the name column

- **WHEN** `RenameRole(id=7, newName="lead")` is called and no other non-archived role under the same orchestrator is named `lead`
- **THEN** role id 7's `name` MUST be `lead` AND no other column MUST be touched AND no argus HTTP call MUST be issued

#### Scenario: Rename role to existing non-archived sibling name rejected

- **WHEN** `RenameRole(id=7, newName="coord")` is called and another non-archived role under the same orchestrator has `name="coord"`
- **THEN** the method MUST return an error AND role id 7's `name` column MUST be unchanged

#### Scenario: Same role name allowed across different orchestrators

- **WHEN** `RenameRole(id=7, newName="coord")` is called under orchestrator `foo` AND another orchestrator `bar` has a non-archived role named `coord`
- **THEN** the rename MUST succeed (uniqueness is scoped to the role's own orchestrator)

### Requirement: Orchestrators and roles may be archived

The system SHALL support setting `archived_at` on an orchestrator via `ArchiveOrchestrator(id)` and on a role via `ArchiveRole(id)`. Archive MUST be a soft delete: the row, its `prompt`, its `argus_project` (for roles), and all related historical rows (messages, role_status, prior bindings) MUST survive. Conversely, `UnarchiveOrchestrator(id)` and `UnarchiveRole(id)` MUST clear `archived_at` to NULL. Archive timestamps MUST be RFC3339 UTC.

#### Scenario: Archive role preserves identity columns

- **WHEN** a role with `prompt="ship F"` and `argus_project="foo-frontend"` is archived
- **THEN** the role row MUST have `archived_at` set to the current RFC3339 timestamp AND `prompt` AND `argus_project` MUST be unchanged AND all messages addressed to or from the role MUST remain in the `messages` table

#### Scenario: Unarchive role clears archived_at

- **WHEN** an archived role with `archived_at="2026-05-26T00:00:00Z"` is passed to `UnarchiveRole(id)`
- **THEN** the role's `archived_at` MUST be NULL AND all other columns MUST be unchanged

#### Scenario: Archive orchestrator does not auto-archive roles

- **WHEN** `ArchiveOrchestrator(id)` is called against an orchestrator with active roles
- **THEN** the orchestrator's `archived_at` MUST be set AND the roles' `archived_at` columns MUST be unchanged (the higher-level cascade is the caller's responsibility — see the hera-view rail-operations spec)

### Requirement: Resurrect — fresh task in role's argus_project rebinds an archived role

The system SHALL, when a new argus task is created in an archived role's stored `argus_project` AND that task calls `hera_join(cwd)`, treat the call as a rebind: clear the role's `archived_at` to NULL, create a new binding row linking the task to the role, AND mirror `meta:hera.role=<kind>` to the new task's metadata. The role's `prompt` and accumulated message history MUST survive the resurrect intact. If multiple archived roles in the same orchestrator share the same `argus_project`, hera MUST prefer the role whose most recent prior binding ended most recently; ties MUST resolve to the role with the lowest `id`.

#### Scenario: Bare hera_join in archived role's argus_project resurrects

- **WHEN** orchestrator `foo` has an archived coord role with `argus_project="foo-frontend"` AND a fresh argus task in project `foo-frontend` calls `hera_join(cwd=$PWD)` with no other arguments
- **THEN** hera MUST clear the role's `archived_at` to NULL, insert a new binding row tying the calling task to the role, AND PUT `{key:"role", value:"coordinator"}` to the bound task's `/api/tasks/{id}/meta` endpoint

#### Scenario: Resurrect preserves prompt

- **WHEN** an archived role with `prompt="ship F"` is resurrected by a fresh `hera_join` in its `argus_project`
- **THEN** the role row's `prompt` column MUST remain `"ship F"` AND the response to `hera_join` MUST surface this value to the caller

#### Scenario: Multiple archived candidates resolve by recency

- **WHEN** orchestrator `foo` has two archived roles `coord` and `lead`, both with `argus_project="foo-frontend"`, AND `coord`'s most recent prior binding ended later than `lead`'s, AND a fresh task in `foo-frontend` calls `hera_join(cwd=$PWD)` with no other arguments
- **THEN** hera MUST rebind to `coord` (most-recent-ended wins)

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

### Requirement: Coordinator-initiated atomic worker spawn

The system SHALL provide a `hera_spawn_worker` MCP tool that allows a coordinator agent to create a new worker task and bind it to its orchestrator in one atomic operation. The calling task MUST hold a live coordinator binding; any other role kind MUST be rejected with an explanatory error.

#### Scenario: Happy path – worker spawned and born bound

- **WHEN** a coordinator calls `hera_spawn_worker(cwd, prompt="<worker instructions>")` with a valid coordinator binding
- **THEN** hera MUST create a new argus task in the coordinator's `argus_project` with the prompt prefixed by an orientation sentence naming the coordinator
- **AND** hera MUST insert a `worker` role under the calling coordinator's orchestrator with a name derived from the prompt (or `role_name` if supplied)
- **AND** hera MUST insert a live binding tying the new argus task to the new worker role
- **AND** hera MUST return `{ orchestrator, role_name, kind: "worker", prompt, binding_id, argus_task_id, prompt_auto_submitted }`

#### Scenario: Auto-submit – prompt runs without manual Enter

- **WHEN** `hera_spawn_worker` creates the argus task successfully
- **THEN** hera MUST attempt `POST /api/tasks/{id}/input` with body `\r` (CR, byte 0x0D) to auto-run the prompt
- **AND** the response MUST include `prompt_auto_submitted: true` when the POST succeeds, `false` when it fails
- **AND** a POST failure MUST NOT cause `hera_spawn_worker` to return an error; the worker is already bound

#### Scenario: Caller is not a coordinator – rejected

- **WHEN** a worker or freelance role calls `hera_spawn_worker`
- **THEN** hera MUST return `isError: true` with a message explaining that only coordinators may spawn workers

#### Scenario: Prompt is empty – rejected

- **WHEN** `hera_spawn_worker` is called with an empty or whitespace-only `prompt`
- **THEN** hera MUST return `isError: true` with a message explaining that `prompt` is required

#### Scenario: Project override

- **WHEN** `hera_spawn_worker` is called with a non-empty `project` field
- **THEN** the argus task MUST be created in the specified project rather than the coordinator's default `argus_project`

#### Scenario: Role name derived from prompt when not supplied

- **WHEN** `hera_spawn_worker` is called without a `role_name`
- **THEN** the worker role name MUST be derived from the first 40 characters of `prompt` via slug normalization and uniqued within the orchestrator's existing non-archived roles

#### Scenario: Explicit role name used when supplied

- **WHEN** `hera_spawn_worker` is called with a non-empty `role_name`
- **THEN** that name MUST be used as the base for uniqueness checking with suffix `-2`/`-3`/… appended if a sibling role with that name already exists

#### Scenario: GetTask failure – binding inserted with empty worktree path

- **WHEN** `hera_spawn_worker` successfully creates the argus task but `GET /api/tasks/{id}` fails
- **THEN** the worker role and binding MUST still be inserted with an empty `worktree_path` and the spawn MUST complete successfully

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

