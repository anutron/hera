# hera-coordination Delta: delegate-delivery-to-argus

## MODIFIED Requirements

### Requirement: Message delivery modes

The system SHALL persist `delivery_mode` on every `messages` row to record how the
message was (or was not) delivered to the recipient's PTY. The enumerated delivery
modes are `pending` (initial state before delivery is attempted), `idle_submit`,
`busy_buffer`, and `queued_no_binding`. Each mode corresponds to a specific
delivery path described in the requirements below.

Under the argus-delegated delivery model, `idle_submit` means argus submitted the
message immediately (the recipient was already idle at the time of the notify call);
`busy_buffer` means argus accepted the delivery and is holding it, submitting when
the recipient next becomes idle or the deadline elapses.

#### Scenario: New messages start in pending

- **WHEN** `hera_send` inserts a message row before calling argus notify
- **THEN** the row MUST have `delivery_mode = "pending"`; the final mode is written via `Messages.SetDelivered` once delivery is resolved

### Requirement: Message delivery delegated to argus notify endpoint

The system SHALL deliver every message pointer to the recipient's PTY by calling
argus's `POST /api/tasks/{recipient_task_id}/notify` endpoint — hera MUST NOT
write to `/api/tasks/{id}/input` directly for message delivery. The notify body
MUST include:

- `text`: the formatted pointer (`[hera from <sender>] msg #<id> — <tldr>`).
- `submit`: `true` when `Config.AutoInjectEnabled` is `true`, `false` otherwise.
- `delivery_id`: the hera message ID as a decimal string.
- `deadline_ms`: `Config.NotifyDeadlineMs` (default 300 000).

argus owns the idle gate, pre-clear, submit CR, retry, and single-writer
exclusion. hera MUST NOT implement any of these behaviors.

When argus responds with `{"state": "submitted"}`, hera MUST record
`delivery_mode = "idle_submit"`. When argus responds with `{"state": "pending"}`,
hera MUST record `delivery_mode = "busy_buffer"`. On a 404 (no active session),
hera MUST return an error without setting a delivery_mode.

#### Scenario: Recipient idle, argus submits immediately

- **WHEN** `hera_send` calls argus notify and argus returns `{"state": "submitted"}`
- **THEN** hera MUST record `delivery_mode = "idle_submit"` on the message row

#### Scenario: Recipient busy, argus accepts for deferred delivery

- **WHEN** `hera_send` calls argus notify and argus returns `{"state": "pending"}`
- **THEN** hera MUST record `delivery_mode = "busy_buffer"` on the message row

#### Scenario: Auto-inject disabled, submit: false sent to argus

- **WHEN** `hera_send` is called AND `Config.AutoInjectEnabled = false`
- **THEN** hera MUST call argus notify with `submit: false`; argus MAY inject the pointer text without submitting it

#### Scenario: No active session, delivery fails

- **WHEN** argus notify returns 404 (task has no active session)
- **THEN** hera MUST return an error to the caller; the message row MUST NOT have its delivery_mode advanced past `pending`

### Requirement: Settings section registered with argus on startup

The system SHALL register exactly one settings-section with argus when the daemon
starts, via `POST /api/plugins/settings/sections`. The section MUST have `type =
"form"` and contain exactly **one** field:

- `auto_inject_enabled`: boolean, default `true`. Description MUST describe BOTH
  what "on" does (argus auto-submits the pointer when the recipient is idle) AND
  what "off" does (argus injects the text without submitting; recipient submits
  manually) AND name a concrete use case for "off". The description MUST reference
  the `submit:` parameter in argus notify rather than hera-side idle detection.

The `idle_debounce_seconds` field is removed from this change onward. hera no
longer has an idle-debounce setting because argus owns idle detection.

The `settings_save` callback MUST accept `auto_inject_enabled` as before and MUST
NOT accept `idle_debounce_seconds`. Supplying `idle_debounce_seconds` in the
callback body is silently ignored (the `any`-typed input struct discards unknown
keys via JSON unmarshalling). The `config` table MAY retain stale
`idle_debounce_seconds` rows from prior daemon runs; they are harmless and will not
be written by new code.

#### Scenario: Settings section has exactly one field post-change

- **WHEN** the hera daemon starts after this change
- **THEN** the registered settings section MUST have exactly one field with key
  `auto_inject_enabled` AND MUST NOT contain a field with key `idle_debounce_seconds`

#### Scenario: auto_inject_enabled description updated

- **WHEN** the settings section is registered
- **THEN** the `auto_inject_enabled` field's `description` MUST describe BOTH what
  "on" does (argus auto-submits on idle) AND what "off" does (text injected without
  submit; user submits manually) AND name a concrete use case for "off"

## REMOVED Requirements

### Requirement: Messages auto-submitted when recipient is idle

**Reason:** hera no longer owns the idle gate or the CR injection. argus's notify
endpoint subsumes this requirement entirely — the `submit: true` parameter and
argus's internal idle detection replace hera's `session.idle` debounce + PTY
write.

**Migration:** hera calls `POST /api/tasks/{id}/notify` with `submit: true`
instead of directly writing `<body>\r` to `/api/tasks/{id}/input` when idle.

### Requirement: Unread idle_submit messages are re-nudged with a non-duplicating doorbell

**Reason:** argus owns retry. The `DeliveryWatcher` goroutine, `Config.NudgeAfter`,
`Config.NudgeEvery`, and `Config.MaxNudges` are all removed. hera cancels argus's
delivery when `read_at` is confirmed.

**Migration:** No hera-side retry loop. argus retries internally until submitted or
the `deadline_ms` expires. hera calls `CancelNotify` on read to stop retries early.

### Requirement: Messages buffered when recipient is not idle

**Reason:** argus owns the idle/busy decision. When the recipient is not idle at
notify time, argus holds the delivery and submits when idle — from hera's
perspective this is simply `delivery_mode = "busy_buffer"` (argus responded
`state: "pending"`). hera no longer decides or injects the no-terminator path.

**Migration:** hera always calls `POST .../notify`; argus determines whether to
submit immediately or defer.
