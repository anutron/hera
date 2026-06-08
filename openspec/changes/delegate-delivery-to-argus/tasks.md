# Tasks: delegate-delivery-to-argus

**Design doc:** openspec/changes/delegate-delivery-to-argus/design.md

## 1. New argus client methods — failing tests first

- [ ] 1.1 Add `NotifyInput`, `NotifyResponse`, `CancelNotifyResponse` types to
       `internal/argus/tasks.go`; add `NotifyTask` and `CancelNotify` method stubs
       that return `errors.New("not implemented")`.
- [ ] 1.2 Write table tests in `internal/argus/tasks_notify_test.go` against
       `httptest.Server`: 202 submitted → NotifyResponse{State:"submitted"}, 202
       pending → NotifyResponse{State:"pending"}, 404 → ErrNoTaskInput, 500 →
       HTTPError; CancelNotify: 200 ok, 404 treated as success, 500 → error.
       Confirm tests fail (stubs return "not implemented").
- [ ] 1.3 Implement `NotifyTask` and `CancelNotify`; confirm tests pass.

## 2. DB migration — failing tests first

**Depends on:** nothing

- [ ] 2.1 Add migration `0009_drop_nudge_columns` to `internal/db/schema.go`:
       DROP INDEX `messages_nudge_scan`, then DROP COLUMN `nudge_count` and
       `nudged_at`. Remove `NudgeCount`/`NudgedAt` from `db.Message` struct in
       `internal/db/types.go`. Remove nudge column scanning from `scanMessageRow`
       in `internal/db/messages.go`. Remove `UnreadIdleSubmitStale` and
       `RecordNudge` methods.
- [ ] 2.2 Write a migration-level test (or extend existing schema tests) that opens
       a fresh DB and verifies `nudge_count` / `nudged_at` columns do NOT exist on
       the `messages` table. Confirm it fails (columns still exist).
- [ ] 2.3 Apply migration code; confirm migration test passes and all other DB tests
       still pass.

## 3. SendHandler — failing tests first

**Depends on:** Stage 1

- [ ] 3.1 Write failing tests for the new send handler behavior in
       `internal/mcp/handlers_test.go` or a dedicated test file:
       - Verify argus notify is called with correct `text`, `submit`, `delivery_id`,
         `deadline_ms` when recipient has a live binding.
       - Verify `delivery_mode = "idle_submit"` when argus responds `state:submitted`.
       - Verify `delivery_mode = "busy_buffer"` when argus responds `state:pending`.
       - Verify `delivery_mode = "queued_no_binding"` when no binding (unchanged path).
       - Verify `submit: false` when `AutoInjectEnabled = false`.
       Tests should fail because `SendHandler` still calls `injector.Inject`.
- [ ] 3.2 Rewrite `internal/mcp/handler_send.go`:
       - Remove `MessageInjector` interface and `injector` field.
       - Add `argus` client field and `cfg` pointer (or inline fields for
         `autoInjectEnabled` atomic.Bool and `notifyDeadlineMs`).
       - Inline `FormatPointer` logic (was `inject.FormatPointer`).
       - Replace injector call with `argus.NotifyTask`; map response state to
         delivery_mode.
- [ ] 3.3 Confirm send handler tests pass.

## 4. InboxHandler and MarkReadHandler — cancel on read

**Depends on:** Stage 1

- [ ] 4.1 Write failing tests verifying that `hera_inbox` calls `CancelNotify`
       for each returned message's delivery_id, and that a cancel error does NOT
       fail the response. Also verify `hera_mark_read` does the same.
- [ ] 4.2 Add argus client to `InboxHandler` and `MarkReadHandler`; call
       `CancelNotify` (best-effort) after `MarkRead` in both handlers.
- [ ] 4.3 Confirm cancel-on-read tests pass.

## 5. SettingsSaveHandler cleanup

**Depends on:** Stage 3 (need send handler to be wired before we know what
implements autoInjectSwitch)

- [ ] 5.1 Remove `debounceSetter` interface and its `tracker` field from
       `internal/mcp/handler_settings_save.go`. Remove `IdleDebounceSeconds`
       field from `settingsSaveInput` and `settingsSaveOutput`. Update
       `NewSettingsSaveHandler` signature (drop tracker param). Update tests.
- [ ] 5.2 Update `internal/settings/registrar.go` `HeraSection` function: remove
       the `idle_debounce_seconds` field. Update `auto_inject_enabled` description
       to reflect `submit:` semantics.

## 6. Config and daemon cleanup

**Depends on:** Stage 3, Stage 5

- [ ] 6.1 Remove `IdleDebounce`, `NudgeAfter`, `NudgeEvery`, `MaxNudges` from
       `internal/config/config.go`. Add `NotifyDeadlineMs int64` (default 300000).
       Remove any references to these fields in `internal/config/config_test.go`.
       Remove key constants from `internal/config/settings_keys.go`.
- [ ] 6.2 Remove `idle.Tracker`, `inject.Injector`, `inject.DeliveryWatcher`,
       doorbell goroutine, and all related wiring from `internal/daemon/run.go`.
       Wire the argus client and config directly to `NewSendHandler`,
       `NewInboxHandler`, `NewMarkReadHandler`. Remove `tracker` from
       `subscriber.Register(...)`. Remove doorbell lifecycle from `Daemon` struct
       and `Stop()`.
- [ ] 6.3 Delete `internal/inject/inject.go`, `internal/inject/inject_test.go`,
       `internal/inject/doorbell.go`, `internal/inject/doorbell_test.go`,
       `internal/inject/doc.go`.
       Delete `internal/idle/tracker.go`, `internal/idle/tracker_test.go`,
       `internal/idle/doc.go`, `internal/idle/audit_followup_test.go`.
- [ ] 6.4 Remove `LoadPersistedSettings` calls for the deleted config keys
       (`KeyIdleDebounceSeconds`, `KeyNudgeAfter`, `KeyNudgeEvery`, `KeyMaxNudges`).
       Remove the corresponding key constants. Old config table rows with these
       keys are harmless and do not require a migration.

## 7. Build, test, and ship

**Depends on:** Stages 1–6

- [ ] 7.1 `make build` green.
- [ ] 7.2 `make test` green (all existing tests pass, new tests pass).
- [ ] 7.3 `make vet && make lint` green.
- [ ] 7.4 `openspec validate delegate-delivery-to-argus --strict` passes.
- [ ] 7.5 hera_send coord: tldr "delegate-delivery-to-argus: build+tests green, ready for PR"
- [ ] 7.6 Open PR with `mcp__argus__iris_gh_pr_create`.
- [ ] 7.7 hera_send coord the PR URL; hera_status done.
