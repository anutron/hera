# Design: delegate-delivery-to-argus

## Context

hera's current delivery stack:

```
hera_send
  → idle.Tracker.IsIdle(taskID)
  → if idle: POST /api/tasks/{id}/input  bytes+"\r"  → delivery_mode=idle_submit
  → if busy: POST /api/tasks/{id}/input  bytes       → delivery_mode=busy_buffer
DeliveryWatcher (goroutine)
  → scan stale unread idle_submit messages
  → POST /api/tasks/{id}/input  "[hera doorbell] N unread — call hera_inbox\r"
  → RecordNudge (increment nudge_count, set nudged_at)
```

Problems:

1. `idle.Tracker` consumes `session.idle` as a *transition* event. If hera
   misses the event (restart, stream gap), the tracker never marks the task idle
   and the message sits un-submitted indefinitely.
2. The doorbell loop is a compensating retry — complex, still racy, and
   intrinsically hera-owned. argus can own this reliably because it runs inside
   the same process as the PTY and the session state machine.

argus's new delivery primitive:

```
POST /api/tasks/{recipient_task_id}/notify
Body: {"text": "<pointer>", "submit": true, "delivery_id": "<msg_id>", "deadline_ms": 300000}
Response: 202 {"delivery_id": "...", "state": "submitted" | "pending"}
```

```
DELETE /api/tasks/{recipient_task_id}/notify/{delivery_id}
Response: 200 {"delivery_id": "...", "cancelled": true | false}
```

argus owns: idle gate, pre-clear (Ctrl+U), submit (CR), retry on missed idle,
deduplication (same delivery_id → no-op), single-writer exclusion, and the
deadline timer. hera does NONE of that anymore.

## Goals / Non-Goals

**Goals:**

- Replace hera's PTY injection + doorbell with a single argus notify call.
- Cancel argus's retries when the recipient confirms receipt via `hera_inbox` or
  `hera_mark_read`.
- Delete all hera-side idle tracking and doorbell machinery.
- Drop the vestigial `nudge_count`/`nudged_at` schema columns.
- Keep `delivery_mode` (still meaningful — see D6).
- Keep `auto_inject_enabled` (controls `submit:` in notify — see D5).
- Degrade gracefully during argus recovery gap (return structured error, not panic).

**Non-Goals:**

- Queued-no-binding drain (v1.1 follow-up, unchanged).
- Any argus-side changes (the notify/cancel endpoints are a stable argus contract
  delivered in the parallel argus PR).
- Any hera-view changes.

## Decisions

### D1: FormatPointer inlined into the send handler

`inject.FormatPointer` is a five-line pure function used in exactly one place.
Moving it to a separate `pointer` package or keeping the inject package alive
just for it adds a layer with no benefit. Inline it directly in
`handler_send.go`. Tests that cover the pointer format move to the send handler
test file.

### D2: inject and idle packages deleted entirely

Both packages become empty shells once Injector and Tracker are removed. There
is no residual logic worth keeping:
- `inject.FormatPointer` → inlined in send handler.
- `inject.FormatDoorbell` → deleted (argus owns doorbell format).
- `idle.Tracker` → deleted.
- `idle.DefaultDebounce` → deleted (no longer configurable from hera's side).

The packages are removed from the repo. `daemon/run.go` imports both; those
imports are dropped.

### D3: New argus client methods

Add to `internal/argus/tasks.go`:

```go
// NotifyInput is the body of POST /api/tasks/{id}/notify.
type NotifyInput struct {
    Text       string `json:"text"`
    Submit     bool   `json:"submit"`
    DeliveryID string `json:"delivery_id"`
    DeadlineMs int64  `json:"deadline_ms"`
}

// NotifyResponse is the 202 response from POST /api/tasks/{id}/notify.
type NotifyResponse struct {
    DeliveryID string `json:"delivery_id"`
    State      string `json:"state"` // "submitted" | "pending"
}

// NotifyTask calls POST /api/tasks/{id}/notify to delegate PTY delivery to argus.
// Returns ErrNoTaskInput on 404 (task has no active session), or the structured
// recovery error during a gap. An argus restart returns whatever the recovering
// client returns — callers surface the existing LinkGate error.
func (c *Client) NotifyTask(ctx context.Context, taskID string, in NotifyInput) (*NotifyResponse, error)

// CancelNotifyResponse is the 200 response from DELETE /api/tasks/{id}/notify/{id}.
type CancelNotifyResponse struct {
    DeliveryID string `json:"delivery_id"`
    Cancelled  bool   `json:"cancelled"`
}

// CancelNotify asks argus to cancel a pending delivery.
// 404 is silently treated as success (already delivered or cancelled).
func (c *Client) CancelNotify(ctx context.Context, taskID, deliveryID string) error
```

### D4: SendHandler calls argus notify directly — no MessageInjector interface

The `MessageInjector` interface exists solely to allow `inject.Injector` to be
swapped in tests. With injection gone, the send handler calls the argus client
directly. Tests mock the HTTP client via `httptest.Server` (the same pattern
used by `argus.Client` throughout the test suite). The `MessageInjector`
interface, `SendHandler.injector` field, and `NewSendHandler` injector param are
all removed.

The send handler receives the argus `Client` directly and reads
`cfg.AutoInjectEnabled` and `cfg.NotifyDeadlineMs` from the config.

### D5: AutoInjectEnabled maps to submit: in notify call

`Config.AutoInjectEnabled` was the master switch over auto-submit. With
delegation, it maps directly to the `submit:` field in the argus notify body:

- `AutoInjectEnabled = true` (default) → `submit: true` → argus auto-submits
  when recipient is idle.
- `AutoInjectEnabled = false` → `submit: false` → argus injects the text but
  does NOT submit; the recipient submits manually.

This preserves the setting's operational purpose: "turn off when you want to QA
every cross-agent message before it lands."

The `SetAutoInjectEnabled(b bool)` method moves from `inject.Injector` to a
thin wrapper in `daemon.Daemon` (or stays on a new `deliveryConfig` struct the
settings handler can hold). The settings handler's `autoInjectSwitch` interface
stays and is satisfied by whatever holds the `atomic.Bool`.

### D6: delivery_mode values preserved with updated semantics

`delivery_mode` stays in the messages schema because it still records what
happened:
- `pending` – initial state, before notify POST.
- `idle_submit` – argus responded `{"state":"submitted"}` (submitted immediately
  because recipient was already idle).
- `busy_buffer` – argus responded `{"state":"pending"}` (argus is holding the
  delivery, will submit when recipient becomes idle or deadline elapses).
- `queued_no_binding` – no live argus task to call notify on (unchanged).

The names `idle_submit` and `busy_buffer` are slightly imprecise under the new
model but remain correct enough that renaming would only add migration churn
with no practical benefit. The spec updates their descriptions.

### D7: nudge columns dropped via migration 0009

`nudge_count` and `nudged_at` are only ever written by `DeliveryWatcher`, which
is deleted. Leaving dead columns silently violates the /doitright rule. The
migration:

```sql
DROP INDEX IF EXISTS messages_nudge_scan;
ALTER TABLE messages DROP COLUMN nudge_count;
ALTER TABLE messages DROP COLUMN nudged_at;
```

SQLite 3.35.0+ supports `ALTER TABLE ... DROP COLUMN`. The daemon's runtime
environment (macOS 12+, bundled SQLite ≥ 3.39) is well above this floor.

The `Message` struct in `db/types.go` and the `scanMessageRow` helper both
have `NudgeCount`/`NudgedAt` removed. `UnreadIdleSubmitStale` and
`RecordNudge` DAO methods are deleted.

### D8: Cancel on read – best-effort, lookup binding once per handler call

`hera_inbox` already resolves the caller's role and binding. After
`MarkRead`, it calls `CancelNotify` for each read message using the same
task binding — one binding lookup, N cancel calls. `hera_mark_read` follows
the same pattern.

Cancel errors are logged at debug level and do NOT fail the handler response.
The worst case is argus delivering an already-read message again — an annoying
duplicate, not a data-loss event. The recipient's `hera_inbox` will return 0
messages on the second delivery because they are already read.

### D9: Config cleanup

Removed fields: `IdleDebounce`, `NudgeAfter`, `NudgeEvery`, `MaxNudges`.
Added field: `NotifyDeadlineMs int64` (default: 300000).

The `LoadPersistedSettings` path in `daemon/run.go` loaded `IdleDebounceSeconds`
and `MaxNudges` etc. from the DB config table. Those keys are no longer written
by new code, but old rows in the config table with those keys are harmless
(they're just ignored). No migration needed for the config table.

### D10: settings section update

The `idle_debounce_seconds` field is removed from the settings section registered
with argus (it is no longer meaningful — argus manages idle detection). The
`auto_inject_enabled` field stays with an updated description reflecting the new
`submit:` semantics.

The `debounceSetter` parameter of `NewSettingsSaveHandler` is removed. The
`debounceSetter` interface is deleted. The `autoInjectSwitch` interface stays.

### D11: Graceful degradation during recovery gap

`NotifyTask` and `CancelNotify` go through `c.doJSON` like all other argus client
methods. During a recovery gap the client's base URL may be stale; calls may
fail with a connection error wrapped in the usual argus error types. The send
handler already checks `LinkGate()` before attempting any work — if the link is
recovering or down, it returns the structured "argus link recovering" error before
reaching the notify call. This is the existing pattern; no new code is needed.
`CancelNotify` in the inbox/mark-read handlers skips the `LinkGate` check
because it is best-effort — a cancel failure during recovery is benign.

## Testing

Each stage's TDD step writes failing tests first:

1. `argus.NotifyTask` / `argus.CancelNotify` – table tests against an
   `httptest.Server` that records calls and returns various status codes.
2. `handler_send` – mock argus server; verify notify is called with correct body
   and delivery_mode is set from the response state. Verify `queued_no_binding`
   path unchanged.
3. `handler_inbox` / `handler_mark_read` – verify cancel is called for each read
   message; verify cancel error does not fail the handler.
4. DB migration – verify `nudge_count`/`nudged_at` are absent after migrate.
5. No test touches the live argus daemon.
