# hera-delivery-receipt Delta: delegate-delivery-to-argus

## MODIFIED Requirements

### Requirement: read_at is the delivery receipt for idle-submit messages

The system SHALL treat `messages.read_at` as the authoritative delivery
confirmation for `idle_submit` messages. A message with `delivery_mode =
'idle_submit'` or `delivery_mode = 'busy_buffer'` and `read_at IS NULL` MUST be
considered in-flight — the pointer was delegated to argus but the recipient has
not yet called `hera_inbox` or `hera_mark_read`.

`read_at` is set by TWO mechanisms:

1. **`hera_inbox` fetch** — when a recipient calls `hera_inbox`, the daemon MUST
   stamp `read_at = now` on all messages returned in that response, in the same
   server-side call. This is the primary delivery receipt.
2. **`hera_mark_read` explicit ack** — the recipient may call `hera_mark_read`
   as an explicit "handled" acknowledgement. Idempotent if `read_at` is already
   set.

After setting `read_at`, the handler MUST call argus
`DELETE /api/tasks/{recipient_task_id}/notify/{message_id}` (best-effort) to
cancel any pending delivery retry. A cancel error MUST NOT fail the handler
response — it is logged at debug level. If no live binding exists for the role
at cancel time the cancel is skipped silently.

#### Scenario: hera_inbox fetch marks messages read and cancels delivery

- **WHEN** a recipient calls `hera_inbox` and N messages are returned
- **THEN** all N returned message rows MUST have `read_at` set to a non-NULL RFC3339 timestamp immediately after the call
- **AND** a subsequent `hera_inbox` call by the same recipient MUST return 0 messages
- **AND** hera MUST call `DELETE /api/tasks/{task_id}/notify/{msg_id}` for each of the N messages (best-effort)

#### Scenario: hera_mark_read marks messages read and cancels delivery

- **WHEN** a recipient calls `hera_mark_read` for a message
- **THEN** the message row MUST have `read_at` set to a non-NULL RFC3339 timestamp
- **AND** hera MUST call `DELETE /api/tasks/{task_id}/notify/{msg_id}` for each marked-read message (best-effort)

#### Scenario: Cancel error does not fail the handler

- **WHEN** the argus cancel call returns an error (e.g., argus link recovering)
- **THEN** hera MUST still return a successful `hera_inbox` / `hera_mark_read` response; the cancel error MUST be logged at debug level and not surface to the caller

#### Scenario: hera_inbox only marks read the caller's own messages

- **WHEN** `hera_inbox` is called by role A and messages for both role A and role B exist
- **THEN** only role A's messages MUST have `read_at` set; role B's messages MUST remain unread

## REMOVED Requirements

### Requirement: Schema tracks nudge delivery attempts

**Reason:** `nudge_count` and `nudged_at` were only written by the deleted
`DeliveryWatcher`. With argus owning retry, hera has no nudge state to track.

**Migration:** Migration 0009 drops `nudge_count` and `nudged_at` from the
`messages` table and drops the `messages_nudge_scan` index.

### Requirement: Doorbell re-nudge is non-duplicating

**Reason:** argus owns the doorbell. hera's `DeliveryWatcher`, `FormatDoorbell`,
and the `[hera doorbell]` PTY format are all deleted. argus's internal retry uses
its own format.

**Migration:** Existing `DeliveryWatcher` goroutine and all related infrastructure
removed from `daemon/run.go`. No replacement in hera.

### Requirement: Doorbell re-nudge loop with bounded retries

**Reason:** argus's notify endpoint owns retry, spacing, cap, and deadline.
`Config.NudgeAfter`, `Config.NudgeEvery`, `Config.MaxNudges` are all removed.

**Migration:** No hera-side retry loop. The `deadline_ms` parameter in the notify
call (controlled by `Config.NotifyDeadlineMs`) is the deadline argus enforces.

### Requirement: Agent contract for doorbell response

**Reason:** Agents no longer receive `[hera doorbell]` PTY turns from hera.
argus may emit its own delivery signal; agents do not need hera-specific handling
for it.

**Migration:** Remove the `[hera doorbell]` handling instruction from agent skills.
Agents continue to call `hera_inbox` proactively on each turn as always.
