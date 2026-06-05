## Why

Idle-submit delivery (the primary hera_send path) fires a single PTY write and considers the message delivered — but if the recipient isn't cleanly submit-ready (e.g. mid argus view-transition), the trailing CR doesn't trigger a submit. The body sits unsubmitted in the PTY input buffer, the agent stays dormant, and the coordinator sees `delivery_mode=idle_submit` and believes delivery succeeded. Messages silently rot.

## What Changes

- Add `nudge_count` (INT NOT NULL DEFAULT 0) and `nudged_at` (TEXT nullable) columns to the `messages` table via migration 0006.
- New `db.MessagesDAO` methods: `UnreadIdleSubmitStale` (query for idle_submit messages with `read_at IS NULL` past a threshold and below max nudge cap) and `RecordNudge` (update `nudge_count + 1` and `nudged_at = now`).
- New `internal/inject.DeliveryWatcher` — a daemon-lifetime goroutine that periodically scans for stale unread idle_submit messages and re-nudges their recipients with a **non-duplicating doorbell**: a short `[hera doorbell] N unread message(s) — call hera_inbox` PTY write (terminated by CR so it auto-submits when the recipient is idle). Re-nudges loop until `read_at` is set or `nudge_count` reaches the configured cap.
- Three new fields in `config.Config`: `NudgeAfter` (initial wait before first nudge, default 30s), `NudgeEvery` (interval between subsequent nudges, default 30s), `MaxNudges` (cap, default 5).
- Updated hera skill (`claude/skills/hera/SKILL.md`) to document the doorbell contract: agents receiving a `[hera doorbell]` message MUST call `hera_inbox` to read their unread messages.

## Capabilities

### New Capabilities

- `hera-delivery-receipt`: Observable, reliable idle-submit delivery — read_at as the delivery receipt, doorbell re-nudge loop, nudge tracking schema, agent contract.

### Modified Capabilities

- `hera-coordination`: Delivery-mode requirement updated to document that idle_submit delivery is confirmed only when `read_at` is set; adds the doorbell re-nudge requirement; adds agent contract for doorbell response.

## Impact

- `internal/db`: new migration (0006), two new MessagesDAO methods.
- `internal/inject`: new `doorbell.go` file, new exported `DeliveryWatcher` struct.
- `internal/config`: three new Config fields with defaults.
- `internal/daemon/run.go`: wire DeliveryWatcher into Start/Stop.
- `claude/skills/hera/SKILL.md`: doorbell contract section added.
- No changes to any MCP handler interfaces; the watcher runs independently of the send/inbox/mark_read handlers.
