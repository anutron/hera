## Design

### Delivery receipt model

`read_at` on the `messages` row is the delivery receipt. A message is considered "consumed" when `read_at` is set — either by `hera_inbox` returning the message (which triggers implicit mark-read) or by an explicit `hera_mark_read` call. When `read_at` is NULL and `delivery_mode = 'idle_submit'`, the message may have been injected but not submitted.

Note: `hera_inbox` currently does NOT automatically mark messages as read — it only returns them. `hera_mark_read` sets `read_at`. The watcher treats "unread" as `read_at IS NULL`.

### The doorbell

A non-duplicating nudge: injects `[hera doorbell] N unread message(s) — call hera_inbox` with a trailing CR. This:

- **Does not re-inject the body** (no duplication risk).
- **Informs the agent** of the count so it has urgency context.
- **Terminates with CR** so it auto-submits if the recipient is idle (matching idle_submit semantics); if busy, it's buffered and the human sees it.
- **Is idempotent** from the coordinator's perspective: receiving multiple doorbells is annoying but not harmful — the agent just calls hera_inbox and reads the messages once.

### Nudge tracking schema (migration 0006)

Two new nullable columns on `messages`:

```sql
ALTER TABLE messages ADD COLUMN nudge_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN nudged_at TEXT;
```

`nudge_count`: incremented atomically with each doorbell fire. Cap at `Config.MaxNudges` (default 5).
`nudged_at`: RFC3339 timestamp of the most recent nudge. Used to enforce `NudgeEvery` spacing.

### DeliveryWatcher

`internal/inject.DeliveryWatcher` owns the re-nudge loop:

```
watcher.Run(ctx):
  ticker := time.NewTicker(interval)  // interval ≈ NudgeEvery / 2, default 15s
  for each tick:
    scan(ctx)

scan(ctx):
  msgs := db.Messages.UnreadIdleSubmitStale(ctx, nudgeAfter, nudgeEvery, maxNudges)
  for each msg:
    bnd, ok := db.Bindings.GetLiveByRole(ctx, msg.ToRoleID)
    if !ok: skip (recipient unbound — they'll see it when they re-bind)
    count := msg.NudgeCount + 1 (for display in doorbell text)
    doorbell := fmt.Sprintf("[hera doorbell] %d unread message(s) — call hera_inbox", countUnread)
    pty.PostTaskInput(ctx, bnd.ArgusTaskID, []byte(doorbell+"\r"))
    db.Messages.RecordNudge(ctx, msg.ID)   // updates nudge_count++ and nudged_at=now
```

The watcher scans all stale messages and fires one doorbell per **recipient** (aggregating: if role R has 3 stale idle_submit messages, one doorbell saying "3 unread messages" covers all of them, but the nudge_count is incremented on each stale message row individually so the cap is per-message, not per-recipient).

Wait — aggregating by recipient is cleaner UX (one doorbell for N messages) but incrementing per-message correctly. The query should group by recipient and the watcher should fire one doorbell per recipient. `RecordNudge` takes a list of message IDs to update in one shot.

### UnreadIdleSubmitStale query

```sql
SELECT id, to_role_id, nudge_count
FROM messages
WHERE delivery_mode = 'idle_submit'
  AND read_at IS NULL
  AND nudge_count < :maxNudges
  AND (
    (nudged_at IS NULL AND delivered_at <= datetime('now', '-' || :nudgeAfterSecs || ' seconds'))
    OR
    (nudged_at IS NOT NULL AND nudged_at <= datetime('now', '-' || :nudgeEverySecs || ' seconds'))
  )
```

This avoids string interpolation of user input by parameterizing seconds; the seconds values come from config (trusted internal values). Since SQLite doesn't have a `datetime('now', '-X seconds')` via parameterized interval, we use Go-side time arithmetic instead: pass absolute cutoff timestamps as bound parameters.

### Watcher scan interval

The watcher ticks at `NudgeEvery / 2` (minimum 5s) so nudges fire within one tick of the threshold. Default: 15s (half of 30s NudgeEvery). This is not user-configurable — derived from NudgeEvery.

### Config additions

```go
NudgeAfter  time.Duration  // initial wait after idle_submit; default 30s
NudgeEvery  time.Duration  // spacing between subsequent nudges; default 30s
MaxNudges   int            // cap; default 5
```

### Wiring in daemon

In `Start()`, after creating the injector:

```go
watcher := inject.NewDeliveryWatcher(database, client, cfg.NudgeAfter, cfg.NudgeEvery, cfg.MaxNudges, log)
watcherCtx, watcherCancel := context.WithCancel(context.Background())
go func() { watcher.Run(watcherCtx) }()
```

Stop in `Daemon.Stop()` before DB close.

### Backward compatibility

The original idle_submit path is unchanged: body+CR injected on first delivery. The watcher only fires on messages that are already persisted with `delivery_mode=idle_submit`. No changes to the MCP handler flow.

### Agent contract (skill update)

The hera skill must document: **when an agent receives a `[hera doorbell]` PTY message, it MUST call `hera_inbox`** to retrieve and read its unread messages, then process them. The doorbell is a re-delivery nudge, not a new message — calling `hera_inbox` reads the original body.
