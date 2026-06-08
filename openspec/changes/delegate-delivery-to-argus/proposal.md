# Proposal: delegate-delivery-to-argus

## Why

hera currently owns every step of PTY message delivery: it checks whether the
recipient's session is idle (via an internal `idle.Tracker` that consumes
`session.*` events), decides whether to append a CR, writes the bytes directly
to the recipient's PTY via `POST /api/tasks/{id}/input`, and runs a
`DeliveryWatcher` goroutine that periodically re-nudges unread messages with a
doorbell write.

The idle gate is unreliable: it is a *transition-only* event consumer that can
miss the `session.idle` signal, leaving a recipient stuck with un-submitted text
in their input buffer forever. The doorbell nudge loop is a compensating
mechanism for exactly this failure, but it adds complexity and still doesn't
guarantee delivery.

argus is adding a first-class reliable delivery primitive:
`POST /api/tasks/{id}/notify` – which owns the idle+focus gate, pre-clears the
input line (Ctrl+U), retries, deduplication, and single-writer exclusion. hera
should delegate all PTY message delivery to this primitive and delete its own
idle-gating + doorbell machinery.

## What Changes

- **Remove `internal/inject`** – `Injector.Inject` (idle-gate + PTY write) is
  replaced by a call to argus `POST .../notify`. The `DeliveryWatcher` doorbell
  loop is deleted entirely. `FormatPointer` moves inline to the send handler.
- **Remove `internal/idle`** – the `Tracker` was used exclusively to feed the
  injector's idle gate. With argus owning idle detection, it is no longer needed
  and is deleted.
- **New argus client methods** – `NotifyTask` and `CancelNotify` added to
  `internal/argus/tasks.go`.
- **Cancel on read** – `hera_inbox` and `hera_mark_read` call argus
  `DELETE .../notify/{delivery_id}` (best-effort) when they set `read_at`, so
  argus stops retrying messages the recipient has already read.
- **Schema cleanup** – `nudge_count` and `nudged_at` columns on `messages` are
  dropped via a new migration; they were only written by the deleted
  `DeliveryWatcher`. `delivery_mode` stays (still meaningful: `idle_submit`
  means argus immediately submitted; `busy_buffer` means argus accepted and is
  holding for idle).
- **Config cleanup** – `IdleDebounce`, `NudgeAfter`, `NudgeEvery`, `MaxNudges`
  removed (all vestigial). `NotifyDeadlineMs` added (controls the `deadline_ms`
  parameter in the notify call; default 300 000 = 5 min). `AutoInjectEnabled`
  stays – it now controls whether hera passes `submit: true` or `submit: false`
  to argus notify.
- **Settings section** – `idle_debounce_seconds` field removed (argus handles
  idle). `auto_inject_enabled` stays with updated description.

## Capabilities

### Modified Capabilities

- `hera-coordination`: delivery-mode semantics updated; auto-submit and
  busy-buffer requirements rewritten to describe delegation; doorbell requirement
  removed.
- `hera-delivery-receipt`: nudge-schema and doorbell-loop requirements removed;
  read_at receipt requirement gains the argus cancel-on-read obligation.
- `hera-substrate-link`: new requirement covering delivery delegation, cancel on
  read, and graceful degradation during a recovery gap.

## Impact

- **Code:** `internal/argus/tasks.go` (new Notify/Cancel methods),
  `internal/inject/` (deleted), `internal/idle/` (deleted),
  `internal/mcp/handler_send.go` (call argus notify directly),
  `internal/mcp/handler_inbox.go` (cancel on read),
  `internal/mcp/handler_mark_read.go` (cancel on read),
  `internal/mcp/handler_settings_save.go` (drop debounceSetter param),
  `internal/settings/registrar.go` (drop idle_debounce_seconds field),
  `internal/config/config.go` (remove vestigial fields, add NotifyDeadlineMs),
  `internal/db/schema.go` (migration 0009 drops nudge columns),
  `internal/db/types.go` (remove NudgeCount/NudgedAt from Message struct),
  `internal/db/messages.go` (remove UnreadIdleSubmitStale, RecordNudge, and
  nudge column scanning), `internal/daemon/run.go` (remove tracker,
  DeliveryWatcher, doorbell goroutine).
- **Dependencies:** argus must have `POST /api/tasks/{id}/notify` and
  `DELETE /api/tasks/{id}/notify/{id}` endpoints. hera degrades gracefully
  during an argus recovery gap (returns the existing recovering/down error).
