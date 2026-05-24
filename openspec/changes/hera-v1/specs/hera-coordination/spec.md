## ADDED Requirements

### Requirement: Role identity outlives argus task lifecycle

The system SHALL persist role identity, mission, constraints, and accumulated history (messages, role status) across argus task lifecycle events. Archiving an argus task that incarnates a role MUST end that role's current binding without deleting the role row or its associated messages.

#### Scenario: Coordinator task archived, role survives

- **WHEN** a coordinator role is bound to argus task `T1` and `T1` is archived
- **THEN** hera MUST set the binding's `ended_at` and `end_reason` columns and leave the role row, all messages addressed to the role, and the role's status row intact

#### Scenario: Same role rebound across multiple incarnations

- **WHEN** a role's current binding is ended and a fresh argus task `T2` is created for the same role
- **THEN** hera MUST insert a new binding row with `(role_id, T2, started_at)` while preserving the previous binding's history

### Requirement: Five MCP tools exposed under the `hera_` prefix

The system SHALL register exactly five MCP tools with argus when the daemon starts: `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`. Each tool MUST be force-prefixed `hera_` per the substrate's tool-name enforcement. Each tool's input schema MUST declare `cwd` as the first input parameter.

#### Scenario: Exactly five tools registered on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** an HTTP GET against argus's MCP registry (via the substrate's listing surface) MUST show exactly five tools scoped to `hera`, with names `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`

#### Scenario: Tool call with no `cwd` rejected

- **WHEN** a hera MCP tool is invoked with no `cwd` parameter
- **THEN** the callback MUST return `isError: true` with a content block explaining that `cwd` is required

#### Scenario: Tool call with unknown `cwd` rejected

- **WHEN** a hera MCP tool is invoked with a `cwd` that does not match any known argus task's worktree
- **THEN** the callback MUST return `isError: true` with a content block explaining that the cwd does not map to a tracked argus task

### Requirement: Messages auto-submitted when recipient is idle

The system SHALL inject `<formatted-body>\n` into the recipient's argus task PTY via `POST /api/tasks/{id}/input` when the recipient's bound task has been in `session.idle` state for at least 2 seconds. The trailing newline causes Claude Code's input handler to submit the buffer immediately.

#### Scenario: Recipient idle, message auto-submits

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥2 seconds
- **THEN** hera MUST POST the formatted body terminated by `\n` to argus's input endpoint AND record `delivery_mode = "idle_submit"` on the message row

### Requirement: Messages buffered when recipient is not idle

The system SHALL inject `<formatted-body>` without a trailing newline into the recipient's argus task PTY when the recipient is not in the idle state. The text remains in the input buffer for the human user to review and submit.

#### Scenario: Recipient busy, message lands in input buffer

- **WHEN** `hera_send` is called with a recipient whose bound task is not idle (no `session.idle` event yet, or `session.started`/`session.exited` more recent than the last `session.idle`)
- **THEN** hera MUST POST the formatted body without `\n` AND record `delivery_mode = "busy_buffer"` on the message row

### Requirement: Injected messages identify sender

The system SHALL prefix every injected message body with `[hera from <sender-role-name>] ` so the recipient agent can identify the source role without an additional tool call.

#### Scenario: Injected body carries sender prefix

- **WHEN** hera injects a message from role `foo-coordinator` with body `"please review"`
- **THEN** the bytes posted to argus's input endpoint MUST be `[hera from foo-coordinator] please review` (plus `\n` in the idle case)

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

### Requirement: Freelance join from an existing task

The system SHALL allow an existing argus task in any project to attach itself to an existing orchestrator post-hoc by calling `hera_join` with `orchestrator=<name>`, `role_name=<self-named>`, `kind="freelance"`, and optional `mission`, `constraints`, `status`. Hera MUST create the role row + binding row atomically. The orchestrator named MUST already exist.

#### Scenario: Freelance join with all attributes

- **WHEN** `hera_join(cwd=$PWD, orchestrator="foo", role_name="refactor-sidebar", kind="freelance", mission="...", constraints="...", status="working")` is invoked from a worktree whose argus task has no prior hera binding
- **THEN** hera MUST create a freelance role under `foo`, insert a binding row tying the calling task to it, populate mission/constraints from the call args, set role_status to `working`, AND return the role identity in the tool response

#### Scenario: Freelance join referencing unknown orchestrator

- **WHEN** `hera_join` is called with `orchestrator="does-not-exist"` and `kind="freelance"`
- **THEN** hera MUST return `isError: true` with content explaining the orchestrator does not exist; no role or binding row MUST be created

#### Scenario: Freelance join conflicts with existing role kind

- **WHEN** `hera_join` is called with `kind="freelance"` and `(orchestrator, role_name)` already exists with `kind="worker"`
- **THEN** hera MUST return `isError: true` with content explaining the existing role has a different kind; no row MUST be modified

### Requirement: Bare `hera_join` claims existing binding

The system SHALL support `hera_join(cwd)` with no other arguments as the re-incarnation claim. Hera MUST resolve the cwd to an argus task, look up the binding for that task, and return the bound role's identity (orchestrator, name, kind, mission, constraints, current status, recent message count).

#### Scenario: Re-incarnation claim succeeds

- **WHEN** `hera_join(cwd=$PWD)` is called from a worktree whose argus task is already bound to a role (e.g., via auto-adoption)
- **THEN** hera MUST return the role's identity and a recent-inbox-count summary without modifying any database rows

#### Scenario: Bare join with no existing binding fails informatively

- **WHEN** `hera_join(cwd=$PWD)` is called from a worktree whose argus task has no hera binding
- **THEN** hera MUST return `isError: true` with content suggesting the caller invoke `hera_join` with explicit `orchestrator`, `role_name`, and `kind` to attach as a freelance

### Requirement: Roles live in argus projects; orchestrators do not

The system SHALL record `argus_project` on the role row at first binding and preserve it across subsequent incarnations. The orchestrators table MUST NOT carry an `argus_project` column. Multiple roles under the same orchestrator MAY have different `argus_project` values.

#### Scenario: Orchestrator with roles in different projects

- **WHEN** orchestrator `foo` has role `coordinator` in argus project `argus` and role `f2-impl` in argus project `hera`
- **THEN** both roles MUST coexist under orchestrator `foo` with their respective `argus_project` values preserved

#### Scenario: Role's argus_project preserved across incarnation

- **WHEN** a role's first binding was created in argus project `foo-frontend` and that binding ends, then `hera resume` creates a new binding for the same role
- **THEN** the new binding MUST be in argus project `foo-frontend` (the role's stored `argus_project`), regardless of which worktree the user is currently in

### Requirement: Stricter rule on auto-adoption logged

The system SHALL log at INFO level every event-stream sequence that would have triggered auto-adoption but was rejected due to missing `meta:hera.role=worker`. The log entry MUST include the new task's id, the parent task's id, and the missing meta key.

#### Scenario: Skipped adoption logged

- **WHEN** a `link.created` event names a child task that has no `meta:hera.role`, with parent bound to a hera coordinator
- **THEN** hera MUST emit a log line at INFO level identifying the skipped task id and the reason (missing meta)

### Requirement: MCP tool registrations heartbeated and unregistered on shutdown

The system SHALL re-POST each of its five tool registrations to argus on a 5-minute cadence to stay within the substrate's 10-minute idle sweep. On graceful shutdown (SIGINT/SIGTERM), hera MUST DELETE each registered tool via `DELETE /api/mcp/tools/{name}` before exiting.

#### Scenario: Heartbeat keeps tools registered

- **WHEN** the hera daemon has been running for >5 minutes
- **THEN** hera MUST have re-POSTed each tool registration at least once since startup

#### Scenario: Graceful shutdown unregisters tools

- **WHEN** the hera daemon receives SIGTERM and shuts down cleanly
- **THEN** hera MUST issue DELETE requests for all five tool names before the process exits

### Requirement: Event stream cursor persisted and replayed

The system SHALL persist the last-seen event id in the `event_cursor` table and pass it as the `since=<id>` query param when reconnecting to `/api/events/stream`. On receipt of a `resync` event (cursor older than argus's retained ring), hera MUST snapshot the current task list via `GET /api/tasks` and reconcile its bindings before resuming the stream.

#### Scenario: Restart resumes from cursor

- **WHEN** the daemon is restarted with `last_seen_event_id = 1234` in the cursor table
- **THEN** the SSE subscription URL MUST include `since=1234` on reconnect

#### Scenario: Resync triggers task snapshot

- **WHEN** hera receives a `resync` event from argus
- **THEN** hera MUST call `GET /api/tasks` and mark any bindings whose argus task no longer exists as ended, before resuming live event processing

### Requirement: Idle gate requires sustained `session.idle` state

The system SHALL treat a bound argus task as eligible for auto-submit only when its most recent session event is `session.idle` AND that event was emitted at least 2 seconds ago. Any `session.started` or `session.exited` event more recent than the latest `session.idle` MUST cause the task to fall out of the idle-eligible set.

#### Scenario: Idle for less than debounce window

- **WHEN** `session.idle` for task `T1` fired 1 second ago
- **THEN** `T1` MUST NOT be treated as idle for injection purposes; messages addressed to `T1`'s role MUST be delivered in busy_buffer mode

#### Scenario: Idle for at least debounce window

- **WHEN** `session.idle` for task `T1` fired 3 seconds ago AND no `session.started` / `session.exited` has fired since
- **THEN** `T1` MUST be treated as idle; messages addressed to `T1`'s role MUST be delivered in idle_submit mode

#### Scenario: Session started after idle

- **WHEN** `session.idle` for `T1` fired at time X, then `session.started` for `T1` fired at time X+1
- **THEN** `T1` MUST NOT be treated as idle until a new `session.idle` event fires AND ≥2 seconds elapse without intervening session events

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

The system's five MCP tool registrations SHALL each include a non-empty `description` field and a JSON Schema `input_schema` describing every parameter (with types, required-flags, and a brief description per field).

#### Scenario: Tool registration carries description and schema

- **WHEN** hera POSTs a tool registration to argus's `/api/mcp/tools`
- **THEN** the registration body MUST include a `description` field of length ≥10 characters AND an `input_schema` object whose `properties` covers every documented parameter of that tool

### Requirement: Role metadata mirrored to argus task_meta

The system SHALL write `meta:hera.role=<kind>` to the bound argus task's metadata whenever a binding is created. When a worker reports a new thread status via `hera_status`, the system SHALL also write `meta:hera.thread_status=<status>` to the bound argus task's metadata. These writes MUST use hera's scope token; the substrate's auto-namespacing MUST derive the `hera` namespace from the scope.

#### Scenario: Role meta written on binding

- **WHEN** a new binding is created for a worker role on argus task `T2`
- **THEN** hera MUST PUT `{key: "role", value: "worker"}` to `/api/tasks/T2/meta` using its scope token

#### Scenario: Thread status meta updated on `hera_status`

- **WHEN** `hera_status(cwd=$PWD, status="blocked")` is called and resolves to worker role `f2-impl`
- **THEN** hera MUST update `role_status` for `f2-impl` AND PUT `{key: "thread_status", value: "blocked"}` to the bound task's `/api/tasks/{id}/meta` endpoint

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
