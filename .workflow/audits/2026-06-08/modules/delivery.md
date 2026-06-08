# Spec audit – delegate-delivery-to-argus delivery module

## Summary

- Requirements checked: 12
- Fully covered: 10
- Missing implementation: 0
- Missing test: 1
- Behavioral gaps (code without spec): 2

---

## Coverage by delta spec

### hera-coordination delta

#### MODIFIED: Message delivery modes

**Requirement text change:** The delta updates the `idle_submit` and `busy_buffer` definitions to reflect argus-delegated semantics. `idle_submit` now means argus submitted immediately; `busy_buffer` means argus is holding the delivery.

**Scenario: New messages start in pending**

- **Implemented:** Yes. `messages.Create` inserts with `delivery_mode = "pending"` (`internal/db/messages.go:37`). `SetDelivered` is called after notify resolves (`internal/mcp/handler_send.go:134`).
- **Test:** `TestSend_ArgusNotify_SubmittedState` verifies `delivered_at` is set after notify; `TestSend_ArgusNotify_PendingState` covers the `busy_buffer` path. The initial `pending` state is implicitly covered by `GetByID` checks post-send.
- **Correct:** Yes.

---

#### ADDED: Message delivery delegated to argus notify endpoint

**Requirement:** hera MUST call `POST /api/tasks/{id}/notify` with `text`, `submit`, `delivery_id`, `deadline_ms`. On `"submitted"` → `idle_submit`; on `"pending"` → `busy_buffer`; on 404 → error (no delivery_mode advance).

**Implemented:** Yes.

- `argus.NotifyTask` at `/Users/aaron/.argus/worktrees/Hera/hera-impl/internal/argus/tasks.go:425` POSTs to `/api/tasks/{id}/notify` with `NotifyInput{Text, Submit, DeliveryID, DeadlineMs}`.
- `handler_send.go:118-131` builds the `NotifyInput` from `formatPointer`, `h.autoInject.Load()`, message ID as decimal string, and `h.deadlineMs`.
- `resp.State == "submitted"` → `DeliveryIdleSubmit`; any other state → `DeliveryBusyBuffer` (lines 127–130).
- 404 from notify → `ErrNoTaskInput` → returned as handler error at line 124–125; `SetDelivered` is never called.

**Pointer format:** `formatPointer` at line 148 produces `[hera from <sender>] msg #<id> — <tldr>`. Matches spec exactly.

**Scenarios:**

| Scenario | Test | Status |
|---|---|---|
| Recipient idle, argus submits immediately (`state:"submitted"` → `idle_submit`) | `TestSend_ArgusNotify_SubmittedState` | PASS |
| Recipient busy, argus accepts (`state:"pending"` → `busy_buffer`) | `TestSend_ArgusNotify_PendingState` | PASS |
| Auto-inject disabled, `submit:false` sent | `TestSend_AutoInjectDisabled_SubmitFalse` | PASS |
| No active session, 404 → error, mode not advanced | `TestSend_NotifyNotFound_ReturnsError` | PASS |

**Correct:** Yes, all four scenarios covered.

---

#### REMOVED: Messages auto-submitted when recipient is idle

**Requirement:** hera's own idle-gate path (direct `POST /api/tasks/{id}/input` with CR) is removed.

**Code status:** No vestigial `Tracker`, `IsIdle`, or direct PTY-with-CR injection exists in the send path. The only remaining `PostTaskInput` call in production code lives in `handler_spawn_worker.go:181` (for the initial `\r` auto-submit on spawn) and in `view/` (for keyboard input forwarding) — neither is delivery-path code. `IdleDebounce` is referenced only in a comment in `daemon/run.go:79` (`"instantiating Tracker and Injector, so they see the saved values"`) — this comment is a stale artifact but there is no live `Tracker` or `Injector` in the delivery path. **Minor:** the comment refers to removed concepts and should be cleaned up, but it does not affect behavior.

**Correct:** Old behavior is gone.

---

#### REMOVED: Unread idle_submit messages re-nudged with non-duplicating doorbell

**Requirement:** `DeliveryWatcher`, `Config.NudgeAfter`, `Config.NudgeEvery`, `Config.MaxNudges` all removed. hera calls `CancelNotify` on read instead.

**Code status:** No `DeliveryWatcher`, `FormatDoorbell`, `[hera doorbell]`, `NudgeAfter`, `NudgeEvery`, or `MaxNudges` exist anywhere in production code. `Config` struct has `NotifyDeadlineMs` (added) but no nudge fields (removed). `LoadPersistedSettings` handles only `KeyAutoInjectEnabled`.

**Correct:** Old behavior is gone.

---

#### REMOVED: Messages buffered when recipient is not idle

**Requirement:** hera's own busy/idle decision (direct PTY write without CR) is removed. argus owns this decision.

**Code status:** The send handler never examines task idle state. It calls `NotifyTask` unconditionally (when a live binding exists) and derives `busy_buffer` vs `idle_submit` from argus's response.

**Correct:** Old behavior is gone.

---

### hera-delivery-receipt delta

#### MODIFIED: read_at is the delivery receipt for idle-submit messages

**Requirement change:** `read_at` now serves as delivery confirmation for both `idle_submit` and `busy_buffer` messages (expanded from idle_submit only). After setting `read_at`, handler MUST call `DELETE /api/tasks/{recipient_task_id}/notify/{message_id}` best-effort; cancel error MUST NOT fail the handler.

**Scenarios:**

**Scenario: hera_inbox fetch marks messages read and cancels delivery**

- **Implemented:** `handler_inbox.go:83-87` calls `db.Messages.MarkRead` for all returned messages, then `cancelDeliveries` (line 87).
- `cancelDeliveries` (lines 112-122) looks up the live binding and calls `CancelNotify` for each message. Errors are logged at debug level only.
- Second-call emptiness: guaranteed by `UnreadForRole` filtering `read_at IS NULL`.
- **Test:** `TestInbox_FetchCancelsArgusDelivery` verifies cancel called with correct `taskID` and `deliveryID`. `TestInbox_MarksReturnedMessagesRead` verifies `read_at` is set and second call returns 0.
- **Correct:** Yes.

**Scenario: hera_mark_read marks messages read and cancels delivery**

- **Implemented:** `handler_mark_read.go:59-65`. `MarkRead` stamps `read_at`, then `cancelDeliveries` calls `CancelNotify` for each ID.
- **Test:** `TestMarkRead_CancelsArgusDelivery` verifies cancel called with correct `taskID` and `deliveryID`.
- **Correct:** Yes.

**Scenario: Cancel error does not fail the handler**

- **Implemented:** `cancelDeliveries` in both handlers uses `slog.Debug` on error and never returns an error to the handler response.
- **Test:** `TestInbox_CancelErrorDoesNotFailHandler` and `TestMarkRead_CancelErrorDoesNotFailHandler`.
- **Correct:** Yes.

**Scenario: hera_inbox only marks read the caller's own messages**

- **Implemented:** `MarkRead` SQL (`messages.go:120`) uses `WHERE ... AND to_role_id = ? AND id IN (...)` so messages belonging to other roles are skipped.
- **Test:** `TestInbox_MarkReadPreservesOtherRoleMessages` and `TestMarkRead_OnlyOwnMessagesAffected`.
- **Correct:** Yes.

**Cancel during recovery gap (no live binding):**

- **Implemented:** Both `cancelDeliveries` implementations return early if `GetLiveByRole` returns an error (line 113 in `handler_inbox.go`, equivalent in `handler_mark_read.go:76`). A missing binding silently skips the cancel — matching the delta's "cancel is skipped silently" requirement.
- **Test:** No dedicated test for "no live binding → silent skip". The existing cancel-error tests cover the `cancelFail=true` 500 path; there is no test where `GetLiveByRole` returns `ErrNotFound`. **Missing test for this specific scenario.**

---

#### REMOVED: Schema tracks nudge delivery attempts

**Requirement:** `nudge_count` and `nudged_at` dropped via migration 0009. Index `messages_nudge_scan` dropped.

**Implemented:** `schema.go:200-209` (migration `0009_drop_nudge_columns`): drops the index and both columns. `types.go` `Message` struct has no `NudgeCount` or `NudgedAt` fields. `messages.go` scan function does not reference these columns.

**Test:** `TestMigration0009_NudgeColumnsDropped` verifies columns absent after `Open()`.

**Correct:** Yes.

---

#### REMOVED: Doorbell re-nudge is non-duplicating / Doorbell re-nudge loop with bounded retries / Agent contract for doorbell response

All three removed requirements:

**Code status:** No `DeliveryWatcher`, `FormatDoorbell`, `[hera doorbell]`, `nudge_count`, `nudged_at`, or doorbell PTY write exists anywhere in production code. Confirmed by grep.

**Correct:** All removed.

---

### hera-substrate-link delta

#### ADDED: Message delivery delegated to argus notify endpoint (substrate perspective)

**Requirement:** `NotifyTask` and `CancelNotify` calls go through the recovering `argus.Client`. During recovery gap, `LinkGate` preamble returns structured error before notify is attempted. Cancel during recovery silently skipped.

**Scenarios:**

**Scenario: Notify call goes through the recovering client**

- **Implemented:** `handler_send.go:118` calls `h.argus.NotifyTask(ctx, bnd.ArgusTaskID, ...)`. The `argus` field is the shared `*argus.Client` (wired in `daemon/run.go:124`). `NotifyTask` uses `c.doJSON` which uses `c.BaseURL()` — atomically read from the recovering client.
- **Test:** `TestSend_ArgusNotify_SubmittedState` and `TestNotifyTask_Submitted` verify the POST goes to the correct path.
- **Correct:** Yes.

**Scenario: Notify during recovery gap returns structured error**

- **Implemented:** `handler_send.go:67-69`: `LinkGate()` is the first call; if link is `recovering` or `down`, it returns the structured error before any notify call.
- **Test:** No dedicated test in the new test files for `LinkGate` + notify interaction. The `LinkGate` behavior is tested elsewhere in the MCP server infrastructure (not a delta-new test), but there is no new test scenario specifically for "send during recovery returns recovering error". **Missing test for this scenario as it applies to the notify path.** (Note: existing `LinkGate` tests may cover this generically.)
- **Correct:** Implementation correct per pattern.

**Scenario: Cancel during recovery gap is silently skipped**

- **Implemented:** Both `cancelDeliveries` implementations skip the cancel when `GetLiveByRole` fails and log errors at debug level. If `CancelNotify` itself fails (e.g., connection error during recovery), the error is logged at debug and the handler response is not affected.
- **Test:** `TestInbox_CancelErrorDoesNotFailHandler` and `TestMarkRead_CancelErrorDoesNotFailHandler` cover the failure path. No test specifically for "recovery gap" link state on cancel.
- **Correct:** Yes.

**Scenario: Client base URL updated on recovery before next notify**

- **Implemented:** `argus.Client.SetBaseURL` is called by `RecoverFunc` before `ForceReregister` (`daemon/run.go:272`). `NotifyTask` uses `c.doJSON` which calls `c.BaseURL()` — atomic read. Next `NotifyTask` call uses the new URL automatically.
- **Test:** No new test for this specific scenario in the delta test files; covered by the broader watcher/recovery integration tests.
- **Correct:** Yes per design.

---

## Behavioral gaps

### Gap 1: `idle_debounce_seconds` removed from settings section without delta spec

The base `hera-coordination` spec (`Settings section registered with argus on startup`) requires **exactly two fields**: `idle_debounce_seconds` (integer) and `auto_inject_enabled` (boolean). The implementation (registrar.go `HeraSection`) only registers **one field** (`auto_inject_enabled`). The `idle_debounce_seconds` field was removed per design doc D10 and tasks.md task 3.4, and the `registrar_test.go` `TestRegistrar_PayloadShapeMatchesSpec` explicitly expects 1 field.

However, **no delta spec in `openspec/changes/delegate-delivery-to-argus/specs/hera-coordination/spec.md`** covers the modification to the "Settings section registered with argus on startup" requirement. The delta spec only covers delivery-mode and notify/cancel requirements. The base spec will be out of sync with the implementation at archive time.

**Should a delta describe it:** Yes. The `hera-coordination` delta spec should include a MODIFIED section for "Settings section registered with argus on startup" describing the reduction from two fields to one, removal of `idle_debounce_seconds`, and the update to `auto_inject_enabled`'s description.

### Gap 2: `LoadPersistedSettings` comment references removed concepts

`daemon/run.go:79` has the comment: `"Override Config defaults with persisted settings (if any) before instantiating Tracker and Injector, so they see the saved values."` Neither `Tracker` nor `Injector` exist as concepts in the post-change code (the IdleTracker and delivery Injector were removed along with the idle-gate path). The comment is stale.

**Should a delta describe it:** No — this is a comment, not a behavioral spec. It should be cleaned up but does not constitute a spec gap. Flagged as a cosmetic cleanup item.

### Gap 3: `PostTaskInput` retained alongside `NotifyTask`

`argus.Client.PostTaskInput` still exists in `internal/argus/tasks.go:458`. It is used legitimately by `handler_spawn_worker.go` (to auto-submit the `\r` CR after worker spawn) and by the `view/` forwarding layer (for keyboard input). It is NOT used for message delivery. The spec only prohibits using `PostTaskInput` for **message delivery**; retaining it for other purposes is correct.

**Should a delta describe it:** No — the retained usage is out-of-scope for the delivery delegation change and is correctly unspecified by the delivery delta.

---

## Summary verdict

**FAIL** – the implementation is behaviorally correct and complete for all specified delivery scenarios, but there is one spec gap that blocks archive:

- The **`hera-coordination` delta spec does not cover the removal of `idle_debounce_seconds` from the settings section** (and the corresponding update to the "Settings section registered with argus on startup" requirement in the base spec). The base spec says "exactly two fields"; the implementation has one. This divergence will leave the base spec out of sync after archive.

Additionally, two tests are absent but not blocking:

1. No test for "cancel silently skipped when no live binding at cancel time" (the `GetLiveByRole` early-return path in `cancelDeliveries`).
2. No dedicated test for "hera_send during recovery gap returns structured error specifically before attempting notify" (covered generically by `LinkGate` but not per-scenario for the new notify path).

**Action required before archive:** Add a MODIFIED entry to `openspec/changes/delegate-delivery-to-argus/specs/hera-coordination/spec.md` for the "Settings section registered with argus on startup" requirement, reflecting the reduction to one field and updated description semantics.
