# hera-coordination Specification

## Purpose
TBD - created by archiving change hera-v1. Update Purpose after archive.
## Requirements
### Requirement: Role identity outlives argus task lifecycle

The system SHALL persist role identity, mission, constraints, and accumulated history (messages, role status) across argus task lifecycle events. Archiving an argus task that incarnates a role MUST end that role's current binding without deleting the role row or its associated messages.

#### Scenario: Coordinator task archived, role survives

- **WHEN** a coordinator role is bound to argus task `T1` and `T1` is archived
- **THEN** hera MUST set the binding's `ended_at` and `end_reason` columns and leave the role row, all messages addressed to the role, and the role's status row intact

#### Scenario: Same role rebound across multiple incarnations

- **WHEN** a role's current binding is ended and a fresh argus task `T2` is created for the same role
- **THEN** hera MUST insert a new binding row with `(role_id, T2, started_at)` while preserving the previous binding's history

### Requirement: Six MCP tools exposed under the `hera_` prefix

The system SHALL register exactly six MCP tools with argus when the daemon starts: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`. Each tool MUST be force-prefixed `hera_` per the substrate's tool-name enforcement. Each tool's input schema MUST declare `cwd` as a required input parameter.

The settings_save handler registered at the hera callback listener is NOT counted as one of the six MCP tools; it is a separate callback type addressed via the settings-section registration, not the MCP tool registration.

#### Scenario: Exactly six MCP tools registered on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** an HTTP GET against argus's MCP registry MUST show exactly six tools scoped to `hera`, with names `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`

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

The system SHALL adopt a new argus task as a worker role of orchestrator X when both: (1) the new task has a `link.created` event whose parent is a task currently bound to orchestrator X's coordinator role, AND (2) the new task has `meta:hera.role=worker`. Tasks meeting only one of these conditions MUST NOT be adopted.

#### Scenario: Both conditions met, task adopted

- **WHEN** argus emits `task.created` for task `T2` followed by `link.created` (`child=T2, parent=T1`) where `T1` is bound to orchestrator `foo`'s coordinator role, AND `T2`'s `meta:hera.role=worker`
- **THEN** hera MUST create a new worker role under orchestrator `foo` AND insert a binding row linking the role to `T2`

#### Scenario: Parent link present, meta absent — not adopted

- **WHEN** argus emits `link.created` (`child=T3, parent=T1`) where `T1` is a hera coordinator binding, but `T3` has no `meta:hera.role`
- **THEN** hera MUST NOT create a role or binding for `T3` AND MUST log the skipped adoption with the missing meta key

#### Scenario: Meta present, parent link absent — not adopted

- **WHEN** argus emits `task.created` for `T4` with `meta:hera.role=worker` but no `link.created` event names `T4` as a child of any hera coordinator binding
- **THEN** hera MUST NOT create a role or binding for `T4`

### Requirement: Auto-adopt copies mission and constraints from task meta

The system SHALL read `meta:hera.mission` and `meta:hera.constraints` from the new task's metadata at adoption time and populate the role row's `mission` and `constraints` columns from those values. Both keys MUST be optional; absence MUST result in empty-string columns.

#### Scenario: Mission and constraints meta present

- **WHEN** an auto-adopted task has `meta:hera.mission="implement schema migration"` and `meta:hera.constraints="must not block on F2"`
- **THEN** the created role row's `mission` and `constraints` columns MUST contain those values verbatim

#### Scenario: Mission and constraints meta absent

- **WHEN** an auto-adopted task has `meta:hera.role=worker` but no `meta:hera.mission` or `meta:hera.constraints`
- **THEN** the created role row's `mission` and `constraints` columns MUST be empty strings (not NULL)

### Requirement: New orchestrator bootstrap via `hera_new_orchestrator`

The system SHALL provide `hera_new_orchestrator(cwd, name, coordinator_role_name, [mission], [constraints])` as the canonical "be an orchestrator" entry point. The call MUST create the orchestrator row (idempotent on name), the coordinator role under it (idempotent on (orchestrator, role_name) when the kind matches), a binding row tying the calling argus task to that coordinator role, AND mirror `meta:hera.role=coordinator` to the bound argus task. The call MUST reject if the calling argus task already has a live binding to any role.

#### Scenario: Fresh orchestrator and coordinator created

- **WHEN** `hera_new_orchestrator(cwd=$PWD, name="foo", coordinator_role_name="coord", mission="ship F", constraints="land by friday")` is called from an unbound argus task
- **THEN** hera MUST create orchestrator `foo`, create a coordinator role `coord` with the given mission and constraints, insert a live binding to the calling argus task, AND PUT `{key: "role", value: "coordinator"}` to the bound task's `/api/tasks/{id}/meta` endpoint

#### Scenario: Existing orchestrator with no live coordinator binding resumed

- **WHEN** `hera_new_orchestrator` is called with a `name` that already exists AND the matching coordinator role has no live binding
- **THEN** hera MUST reuse the existing orchestrator + role rows AND create a new binding tying the calling task to that role; the response payload MUST report `created: false`

#### Scenario: Coordinator already live elsewhere

- **WHEN** `hera_new_orchestrator` is called naming an existing orchestrator + coordinator role whose binding is currently live in another argus task
- **THEN** hera MUST return `isError: true` directing the operator to resume from that worktree via `hera_join`; no new binding MUST be created

#### Scenario: Calling task already bound

- **WHEN** `hera_new_orchestrator` is called from an argus task that already has any live hera binding
- **THEN** hera MUST return `isError: true` directing the operator to resume the existing role via `hera_join(cwd)` instead

### Requirement: Worker and freelance attach via `hera_join`

The system SHALL allow an existing argus task in any project to attach itself to an existing orchestrator post-hoc by calling `hera_join` with `orchestrator=<name>`, `role_name=<self-named>`, `kind="worker"` or `kind="freelance"`, and optional `mission`, `constraints`, `status`. Hera MUST create the role row + binding row atomically AND mirror `meta:hera.role=<kind>` to the bound argus task. The orchestrator named MUST already exist. Kind `coordinator` is NOT accepted by `hera_join`; bootstrap a coordinator via `hera_new_orchestrator`.

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

### Requirement: Bare `hera_join` claims existing binding

The system SHALL support `hera_join(cwd)` with no other arguments as the re-incarnation claim. Hera MUST resolve the cwd to an argus task, look up the binding for that task, and return the bound role's identity (orchestrator, name, kind, mission, constraints, current status, recent message count).

#### Scenario: Re-incarnation claim succeeds

- **WHEN** `hera_join(cwd=$PWD)` is called from a worktree whose argus task is already bound to a role (e.g., via auto-adoption)
- **THEN** hera MUST return the role's identity and a recent-inbox-count summary without modifying any database rows

#### Scenario: Bare join with no existing binding fails informatively

- **WHEN** `hera_join(cwd=$PWD)` is called from a worktree whose argus task has no hera binding
- **THEN** hera MUST return `isError: true` with content suggesting either `hera_join` with explicit `orchestrator`, `role_name`, `kind` (for worker/freelance attach) or `hera_new_orchestrator` (to bootstrap a new orchestrator)

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

The system SHALL route `hera_send` calls from a worker or freelance role that omit the `to` parameter to the coordinator role of the same orchestrator. The coordinator role MUST exist for the send to succeed.

#### Scenario: Worker without `to` routes to coordinator

- **WHEN** a worker role under orchestrator `foo` calls `hera_send(cwd=$PWD, body="...")` with no `to`
- **THEN** the message row's `to_role_id` MUST be the coordinator role's id under orchestrator `foo`, AND the injection path MUST be triggered

### Requirement: Coordinator senders must supply an explicit recipient

The system SHALL reject `hera_send` calls from a coordinator role that omit the `to` parameter. The coordinator's normal channel to the human is the coordinator's own Claude pane; messages emitted via `hera_send` MUST target a specific worker or freelance role by name.

#### Scenario: Coordinator without `to` is rejected

- **WHEN** the coordinator role under orchestrator `foo` calls `hera_send(cwd=$PWD, body="...")` with no `to`
- **THEN** the call MUST return `isError: true` with content explaining that coordinator messages require an explicit recipient, AND no message row MUST be persisted

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

