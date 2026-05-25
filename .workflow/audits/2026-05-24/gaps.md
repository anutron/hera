# Spec Audit Gaps – 2026-05-24

Sorted by severity. Each entry lists the requirement, the location, and a one-line fix.

## High severity

### G1. `hera_join` skips the `meta:hera.role` argus mirror
- **Spec:** § "Role metadata mirrored to argus task_meta" / scenario "Role meta written on binding" (MUSTs the PUT on every binding creation)
- **Location:** `internal/mcp/handler_join.go:171` (freelance path) and `:154` (coordinator path); `JoinHandler` does not hold an `*argus.Client` reference
- **Fix:** Wire `*argus.Client` into `JoinHandler` (constructor + struct field), then call `h.client.PutTaskMeta(ctx, argusTaskID, "role", string(kind))` after `Bindings.Create` succeeds. Mirror auto-adopt's behavior at `internal/events/adopt.go:128`. Add a test asserting the meta write happens on freelance attach.

### G2. `TestSend_*` doesn't assert persisted `delivery_mode`
- **Spec:** § "Messages auto-submitted when recipient is idle" / "Messages buffered when recipient is not idle" (MUST record on the row)
- **Location:** `internal/mcp/handler_send_test.go` (all TestSend_* tests assert response JSON only)
- **Fix:** After each send call, fetch the message via `e.db.Messages.GetByID(out.MessageID)` and assert `msg.DeliveryMode == expected`. Close the loop on a MUST requirement.

### G3. No tests for `ResyncHandler`
- **Spec:** § "Event stream cursor persisted and replayed" / scenario "Resync triggers task snapshot"
- **Location:** `internal/events/resync.go` (entire file – 18 branches uncovered)
- **Fix:** Create `internal/events/resync_test.go` covering: (a) ResyncHandler ignores non-resync events; (b) on resync, ListTasks is called and bindings whose ArgusTaskID is absent from the live list are ended with `end_reason=resync_missing`; (c) bindings whose tasks are still present are left alone.

### G4. `adopt.go:86-88` skipped-adoption log omits parent task id
- **Spec:** § "Stricter rule on auto-adoption logged" / scenario "Skipped adoption logged" (log MUST include new task id, parent task id, missing meta key)
- **Location:** `internal/events/adopt.go:86-88` (wrong-meta-value branch — only logs `child` and `value`; sibling at 81-83 includes `parent`)
- **Fix:** Add `parent=link.Parent` and `missing_key=MetaKeyRole` to the log attributes for symmetry. Then add a test that captures slog output and verifies both branches emit the required fields.

## Medium severity

### G5. No test for mission/constraints-absent → empty-strings
- **Spec:** § "Auto-adopt copies mission and constraints from task meta" / scenario "Mission and constraints meta absent" (MUST be empty strings, not NULL)
- **Location:** `internal/events/adopt_test.go` (happy-path covered; absent-path not)
- **Fix:** Add `TestAdopt_MissionConstraintsAbsent_EmptyStrings` setting only `meta:hera.role=worker` and asserting role.Mission == "" && role.Constraints == "" after adoption.

### G6. Skipped-adoption INFO log isn't asserted by any test
- **Spec:** § "Stricter rule on auto-adoption logged" / scenario "Skipped adoption logged"
- **Location:** `internal/events/adopt_test.go` (no log capture in any test)
- **Fix:** In the existing skipped-adoption tests, inject a `*slog.Logger` whose handler captures records, then assert the captured records include the required keys (task id, parent id, missing key).

### G7. `hera_status` meta-mirror is best-effort, spec says MUST
- **Spec:** § "Role metadata mirrored to argus task_meta" / scenario "Thread status meta updated on `hera_status`" (MUSTs the PUT)
- **Location:** `internal/mcp/handler_status.go:74-79` (PutTaskMeta failure swallowed; `meta_mirrored: false` returned)
- **Fix options:** (A) make the meta write blocking — failure returns IsError. (B) keep best-effort, amend spec to clarify "best-effort: handler returns success with meta_mirrored:false if the argus write fails." **Aaron's call.**

### G8. `queued_no_binding` delivery mode in code but not in base spec
- **Spec:** Enumerated modes are `idle_submit` and `busy_buffer`. Archived design doc mentions queued; base spec doesn't.
- **Location:** `internal/db/types.go` (defines `DeliveryQueuedNoBinding`); `internal/mcp/handler_send.go:88` (uses it)
- **Fix options:** (A) Amend the base spec to enumerate `queued_no_binding`. (B) Remove the mode and just reject sends with no recipient binding. **Aaron's call.** Recommended: (A), this is the existing behavior and is useful.

### G9. `kind="coordinator"` accepted by `hera_join` but not enumerated by spec
- **Spec:** § "Freelance join from an existing task" describes freelance and accepts kind=worker/freelance/coordinator implicitly via validation. The coordinator-bootstrap call shape isn't a separate scenario.
- **Location:** `internal/mcp/handler_join.go:118` (kind=coordinator validates), `internal/daemon/run.go:174` (input_schema enum lists coordinator)
- **Fix options:** (A) Add a spec scenario "Coordinator bootstrap via hera_join" describing the call shape + that `Orchestrators.Create` is idempotent. (B) Remove the call shape; require operators to bootstrap orchestrators via a future `hera_new_orchestrator` tool (planned for next change). **Aaron's call.** Recommended: (A) for v1 + plan the move per the morning-review note.

## Low severity

### G10. Cwd → task lookup is byte-exact, not normalized
- **Spec:** Doesn't define match semantics
- **Location:** `internal/mcp/resolve.go:35`
- **Fix:** Either pin spec to byte-exact, or normalize via `filepath.Clean` + resolve symlinks before comparison.

### G11. `internal/argus/events.go:67-69` treats cursor 0 as "no cursor"
- **Spec:** Doesn't define cursor-0 sentinel
- **Location:** `internal/argus/events.go:67-69`
- **Fix:** Either spec a cursor-0 sentinel or always emit the `since=` query param.

### G12. `StreamEvents` doc comment doesn't mention resync requirement
- **Location:** `internal/argus/events.go` (StreamEvents)
- **Fix:** Add a sentence: "Callers MUST handle resync events by snapshotting state and reconciling. See `internal/events.ResyncHandler` for the reference implementation."

### G13. 13–16 (low-priority test-alignment gaps)
- 17 in `internal/mcp` – mostly "asserts the call path; doesn't quote the spec wording verbatim."
- 4 in `internal/db` – similar.
- 3 in `internal/argus` – similar.
- 2 in `internal/wiring` – `TestDaemonStart_TokenMissing` and `TestLoadToken_FileEmpty` don't assert actionable error wording.

### G17. `cmd-hera` uncovered behaviors
- 6 minor findings: output formatting, exit codes, --foreground enforcement, PID-file lifecycle. Spec doesn't describe CLI ergonomics; these are reasonable.

## Verified False Positives

None.
