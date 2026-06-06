# hera-coordination Delta: reliable-bus-delivery

## MODIFIED Requirements

### Requirement: Messages auto-submitted when recipient is idle

The system SHALL inject `<formatted-body>\r` into the recipient's argus task PTY via `POST /api/tasks/{id}/input` when ALL of the following hold:

1. The recipient's bound task has been in `session.idle` state for at least `Config.IdleDebounce`.
2. `Config.AutoInjectEnabled` is `true`.

The trailing byte MUST be CR (`\r`, byte 0x0D), not LF. After injection, the message row MUST have `delivery_mode = 'idle_submit'` and `read_at IS NULL` — the idle-submit record indicates an injection attempt only; the system MUST NOT set `read_at` on injection. Delivery MUST be confirmed only when `read_at` is set (by a subsequent `hera_mark_read` call from the recipient).

#### Scenario: Recipient idle, auto-inject enabled, message auto-submits

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥ the configured debounce AND `AutoInjectEnabled = true`
- **THEN** hera MUST POST the formatted body terminated by `\r` to argus's input endpoint AND record `delivery_mode = "idle_submit"` on the message row, with `read_at` remaining NULL

#### Scenario: idle_submit injection does not set read_at

- **WHEN** hera injects a message via the idle-submit path
- **THEN** the message row MUST have `read_at IS NULL` immediately after injection; the daemon MUST NOT set `read_at` itself

### Requirement: Unread idle_submit messages are re-nudged with a non-duplicating doorbell

The system SHALL re-nudge unread idle_submit messages via the `DeliveryWatcher` doorbell mechanism when `read_at` remains NULL past `Config.NudgeAfter`. Re-nudges MUST continue at `Config.NudgeEvery` intervals until `read_at` is set or `nudge_count` reaches `Config.MaxNudges`. The doorbell MUST NOT re-inject the original message body.

See the `hera-delivery-receipt` spec for the full nudge loop, doorbell format, schema, and agent contract.

#### Scenario: Unread idle_submit message nudged after NudgeAfter

- **WHEN** a message with `delivery_mode = 'idle_submit'` has `delivered_at` older than `NudgeAfter`, `read_at IS NULL`, and `nudge_count < MaxNudges`
- **THEN** the delivery watcher MUST inject a doorbell for the recipient and MUST NOT re-inject the original message body

#### Scenario: Nudging stops when recipient reads the message

- **WHEN** `read_at` is set on a message — either by the recipient calling `hera_inbox` (which stamps `read_at` on all returned messages) or by an explicit `hera_mark_read` call
- **THEN** the delivery watcher MUST NOT emit any further nudge for that message
