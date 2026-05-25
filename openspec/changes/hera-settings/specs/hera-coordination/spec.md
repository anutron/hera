## ADDED Requirements

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

The system SHALL treat `Config.AutoInjectEnabled` as a master switch over the auto-submit branch of `Injector.Inject`. When `AutoInjectEnabled = false`, every message MUST be delivered in `busy_buffer` mode (formatted body, no trailing newline) regardless of the recipient task's idle state. When `AutoInjectEnabled = true` (the default), v1 behavior is preserved: idle tasks get `idle_submit`, non-idle tasks get `busy_buffer`.

#### Scenario: Auto-inject off, idle recipient still busy-buffers

- **WHEN** `AutoInjectEnabled = false` AND `hera_send` targets a worker whose bound task has been idle ≥ the configured debounce
- **THEN** the message MUST be persisted with `delivery_mode = "busy_buffer"` AND the bytes POSTed to argus's input endpoint MUST be the formatted body WITHOUT a trailing newline

#### Scenario: Auto-inject toggles back to true, behavior restored

- **WHEN** `AutoInjectEnabled` transitions from `false` to `true` via a successful settings_save AND a subsequent `hera_send` targets an idle recipient
- **THEN** the message MUST be persisted with `delivery_mode = "idle_submit"` AND the bytes POSTed MUST include the trailing newline

## MODIFIED Requirements

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

### Requirement: Messages auto-submitted when recipient is idle

The system SHALL inject `<formatted-body>\n` into the recipient's argus task PTY via `POST /api/tasks/{id}/input` when ALL of the following hold:

1. The recipient's bound task has been in `session.idle` state for at least `Config.IdleDebounce`.
2. `Config.AutoInjectEnabled` is `true`.

The trailing newline causes Claude Code's input handler to submit the buffer immediately.

#### Scenario: Recipient idle, auto-inject enabled, message auto-submits

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥ the configured debounce AND `AutoInjectEnabled = true`
- **THEN** hera MUST POST the formatted body terminated by `\n` to argus's input endpoint AND record `delivery_mode = "idle_submit"` on the message row

#### Scenario: Recipient idle, auto-inject disabled, message busy-buffers

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥ the configured debounce AND `AutoInjectEnabled = false`
- **THEN** hera MUST POST the formatted body WITHOUT a trailing newline AND record `delivery_mode = "busy_buffer"` on the message row

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
