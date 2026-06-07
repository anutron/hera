# hera-delivery-receipt Specification

## Purpose

Delivery receipt and doorbell re-nudge system for `idle_submit` messages. Tracks whether injected messages were actually seen by the recipient and re-nudges via a non-duplicating doorbell PTY write until `read_at` is confirmed.

## Requirements

### Requirement: read_at is the delivery receipt for idle-submit messages

The system SHALL treat `messages.read_at` as the authoritative delivery confirmation for `idle_submit` messages. A message with `delivery_mode = 'idle_submit'` and `read_at IS NULL` MUST be considered unconfirmed — the body was injected but the agent may not have seen or processed it.

`read_at` is set by TWO mechanisms, both of which stop further doorbell nudges:

1. **`hera_inbox` fetch** — when a recipient calls `hera_inbox`, the daemon MUST stamp `read_at = now` on all messages returned in that response, in the same server-side call. This is the primary delivery receipt: the agent received and saw the message.
2. **`hera_mark_read` explicit ack** — the recipient may additionally call `hera_mark_read` as an explicit "handled" acknowledgement. This is idempotent if `read_at` is already set by an earlier `hera_inbox` fetch.

The delivery watcher MUST stop nudging a message as soon as `read_at` is set, regardless of which mechanism set it.

#### Scenario: hera_inbox fetch marks returned messages read

- **WHEN** a recipient calls `hera_inbox` and N messages are returned
- **THEN** all N returned message rows MUST have `read_at` set to a non-NULL RFC3339 timestamp immediately after the call
- **AND** a subsequent `hera_inbox` call by the same recipient MUST return 0 messages (they are no longer unread)
- **AND** the delivery watcher MUST NOT emit further nudges for those messages

#### Scenario: Message read confirms delivery

- **WHEN** a recipient calls `hera_mark_read` for a message
- **THEN** the message row MUST have `read_at` set to a non-NULL RFC3339 timestamp, and the delivery watcher MUST NOT emit further nudges for that message

#### Scenario: hera_inbox only marks read the caller's own messages

- **WHEN** `hera_inbox` is called by role A and messages for both role A and role B exist
- **THEN** only role A's messages MUST have `read_at` set; role B's messages MUST remain unread

#### Scenario: idle_submit with read_at NULL considered unconfirmed

- **WHEN** a message has `delivery_mode = 'idle_submit'` and `read_at IS NULL`
- **THEN** the system MUST treat it as potentially undelivered and eligible for doorbell re-nudge once the nudge threshold elapses

### Requirement: Schema tracks nudge delivery attempts

The `messages` table SHALL include two nullable columns for nudge state: `nudge_count INTEGER NOT NULL DEFAULT 0` and `nudged_at TEXT` (nullable RFC3339 timestamp). These persist durably across daemon restarts.

#### Scenario: New message has zero nudge state

- **WHEN** a new message is inserted via `messages.Create`
- **THEN** the row MUST have `nudge_count = 0` and `nudged_at IS NULL`

### Requirement: Doorbell re-nudge is non-duplicating

The system SHALL emit a short doorbell PTY write when re-nudging, formatted as `[hera doorbell] N unread message(s) — call hera_inbox\r` where N is the total count of unread messages for that recipient at scan time. The doorbell MUST NOT re-inject the original message body. Multiple doorbell fires for the same recipient MUST NOT cause the original message body to appear more than once in the recipient's PTY.

#### Scenario: Doorbell format is correct

- **WHEN** the delivery watcher fires a re-nudge for a recipient who has 2 unread messages
- **THEN** the bytes written to argus input MUST match `[hera doorbell] 2 unread message(s) — call hera_inbox\r` (terminated by CR 0x0D, not LF)

#### Scenario: Body is not re-injected on re-nudge

- **WHEN** the delivery watcher fires a re-nudge for message M
- **THEN** the bytes posted to argus input MUST NOT contain the original body of message M

### Requirement: Doorbell re-nudge loop with bounded retries

The system SHALL run a daemon-lifetime `DeliveryWatcher` goroutine that periodically scans for stale unread idle_submit messages and fires a doorbell nudge per recipient. The scan MUST only target messages with `delivery_mode = 'idle_submit'`, `read_at IS NULL`, and `nudge_count < MaxNudges`. It MUST respect `NudgeAfter` (initial wait after `delivered_at` before the first nudge) and `NudgeEvery` (minimum spacing between subsequent nudges). It MUST aggregate all stale messages for a given recipient into a single doorbell write. After firing, it MUST increment `nudge_count` and set `nudged_at = now` for each nudged message row.

Config defaults: `NudgeAfter = 30s`, `NudgeEvery = 30s`, `MaxNudges = 5`.

#### Scenario: First nudge fires after NudgeAfter elapses

- **WHEN** a message has `delivery_mode = 'idle_submit'`, `delivered_at` older than `NudgeAfter`, and `nudge_count = 0`
- **THEN** the next watcher scan MUST fire a doorbell for the recipient and increment `nudge_count` to 1, setting `nudged_at = now`

#### Scenario: Re-nudge respects NudgeEvery spacing

- **WHEN** a message has `nudge_count = 1` and `nudged_at` is within the last `NudgeEvery` window
- **THEN** the watcher scan MUST NOT fire a nudge for that message

#### Scenario: Cap stops nudging at MaxNudges

- **WHEN** a message has `nudge_count >= MaxNudges` and `read_at IS NULL`
- **THEN** the watcher MUST NOT emit any further doorbell for that message

#### Scenario: Nudge stops when read_at is set

- **WHEN** `read_at` is set on a message
- **THEN** the watcher MUST NOT emit any further nudge for that message; it MUST be excluded from all future scans

### Requirement: Agent contract for doorbell response

An agent receiving a `[hera doorbell]` PTY turn MUST call `hera_inbox(cwd=$PWD)` as its next action to retrieve and process its unread messages. The doorbell is a re-delivery signal, not a new message; the actual content is in the inbox.

#### Scenario: Agent receives doorbell and calls hera_inbox

- **WHEN** an agent's PTY receives a `[hera doorbell] N unread message(s) — call hera_inbox` message as a turn
- **THEN** the agent MUST call `hera_inbox(cwd=$PWD)` to retrieve the queued messages
