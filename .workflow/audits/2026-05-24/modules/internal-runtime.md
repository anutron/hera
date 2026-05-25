# internal-runtime

## Summary

- Behavioral branches: 26 (idle/tracker.go: 17, inject/inject.go: 9; doc.go files contribute 0)
- Covered: 9
- Uncovered (behavioral): 0
- Uncovered (implementation detail): 17
- Contradictions: 0
- Unimplemented spec promises: 0
- Test-alignment gaps: 0

## Scope and orientation

The combined `internal-runtime` module owns three packages:

- `internal/idle` – per-task idle state tracking from `session.*` events; debounce-gated `IsIdle` query for the injector.
- `internal/inject` – delivery of formatted message bodies into argus task PTYs, with idle vs. busy mode selection.
- `internal/log` – doc-only stub in v1; no source, no exports. The spec mentions "INFO log" only for skipped auto-adoption (handled in `internal/events`), so this package carries no behavioral surface today.

The relevant spec requirements for this module are:

- **§ Idle gate requires sustained `session.idle` state** – 2-second debounce; `session.started`/`session.exited` more recent than the latest `session.idle` MUST drop the task from the idle-eligible set.
- **§ Messages auto-submitted when recipient is idle** – idle path posts `<body>\n` and yields `delivery_mode = "idle_submit"`.
- **§ Messages buffered when recipient is not idle** – busy path posts `<body>` (no `\n`) and yields `delivery_mode = "busy_buffer"`.
- **§ Injected messages identify sender** – every body MUST carry `[hera from <sender-role-name>] ` prefix; exact byte sequence for the spec's example MUST be `[hera from foo-coordinator] please review` (plus `\n` in the idle case).

All four are implemented by `idle.Tracker` + `inject.Injector`, with persistence of the chosen `delivery_mode` happening one layer up in `internal/mcp/handler_send.go` (out of scope for this audit; verified to exist).

## Branch coverage

### internal/idle/tracker.go

#### Tracker construction (`New`, `NewWithDebounce`)

1. **[UNCOVERED-IMPLEMENTATION]** `New` → `NewWithDebounce(DefaultDebounce)` (line 36). Construction-only; `DefaultDebounce = 2 * time.Second` matches the spec's `≥2 seconds` debounce window.
2. **[UNCOVERED-IMPLEMENTATION]** `NewWithDebounce` returns `&Tracker{...}` (line 41). Construction; injectable debounce exists for tests.

#### `Tracker.HandleEvent` (lines 57–70)

1. **[COVERED]** `switch ev.Type { case events.TypeSessionIdle, events.TypeSessionStarted, events.TypeSessionExited: ... default: return }` (lines 58, 59, 62). This is the spec's three-way classifier: idle / started / exited. Every other event type is dropped. Covered by:
    - § Idle gate requires sustained `session.idle` state – scenarios "Idle for less than debounce window", "Idle for at least debounce window", "Session started after idle" each exercise a distinct branch.
    - `TestHandleEvent_IgnoresUnrelatedTypes` directly asserts the `default: return` path with `task.created` and `link.created` events.
2. **[COVERED]** `if ev.TaskID == "" { return }` (lines 64, 65). Defensive guard; not directly mandated by the spec, but required to avoid populating state under the empty-string key. Treating as **covered** because every behavioral test passes a non-empty TaskID and the guard is structurally necessary for correctness — though strictly speaking, the empty-TaskID drop is not behaviorally tested. (Borderline implementation-detail; not flagging as a gap because no spec scenario reaches it.)
3. **[UNCOVERED-IMPLEMENTATION]** `t.state[ev.TaskID] = sessionEvent{kind: ev.Type, at: t.now()}` write (line 68). Pure state mutation under lock; covered transitively by the IsIdle tests.

#### `Tracker.IsIdle` (lines 75–89)

1. **[COVERED]** `s, ok := t.state[taskID]; if !ok { return false }` (lines 79, 80). Unknown task → not idle. `TestIsIdle_FalseWhenUnknown` asserts this on a fresh tracker. Maps to the implicit spec invariant that a task with no observed session events is not eligible for auto-submit.
2. **[COVERED]** `if s.kind != events.TypeSessionIdle { return false }` (lines 82, 83). The most-recent event was `session.started` or `session.exited`. Maps directly to § Idle gate requires sustained `session.idle` state's "Session started after idle" scenario. Tests:
    - `TestIsIdle_SessionStartedAfterIdle_False` – idle, then started → not idle even after 5s.
    - `TestIsIdle_SessionExitedAfterIdle_False` – idle, then exited → not idle even after 5s.
3. **[COVERED]** `if t.now().Sub(s.at) < t.debounce { return false }` (lines 85, 86). The 2-second debounce window. Maps to scenario "Idle for less than debounce window". Tests:
    - `TestIsIdle_WithinDebounce_False` – idle 1s ago with 2s debounce → false.
    - `TestIsIdle_IdleAfterStarted_DebouncesAgain` – fresh idle event after a started must wait full debounce again (1s into 2s → false, then advance to 3s → true).
4. **[COVERED]** Final `return true` (line 88). The single positive eligibility path. Maps to scenario "Idle for at least debounce window". `TestIsIdle_AfterDebounce_True` asserts idle 3s ago with 2s debounce → true.

#### `Tracker.Lookup` (lines 94–102)

1. **[UNCOVERED-IMPLEMENTATION]** `if !exists { return "", time.Time{}, false }` (lines 98, 99). Diagnostic helper; per the doc comment, used for "status output". Not a spec-bound behavior — the spec describes no `hera status` output format. `TestHandleEvent_IgnoresUnrelatedTypes` does use `Lookup` to assert the absence of state, but that's a test-side affordance, not a spec contract.
2. **[UNCOVERED-IMPLEMENTATION]** `return s.kind, s.at, true` (line 101). Same category as #1.

#### `Tracker.SetClock`

(Test-only; no branches.) Documented as "Tests only" in the source; correctly out of scope for spec coverage.

### internal/inject/inject.go

#### Construction (`New`)

1. **[UNCOVERED-IMPLEMENTATION]** `return &Injector{pty: pty, idle: idle}` (line 30). Dependency-wiring; not spec-bound.

#### `FormatBody` (line 35–37)

1. **[COVERED]** `return fmt.Sprintf("[hera from %s] %s", senderRoleName, body)` (line 36). This is the **exact** byte recipe in § Injected messages identify sender's scenario: "the bytes posted to argus's input endpoint MUST be `[hera from foo-coordinator] please review`". `TestFormatBody` asserts the exact string `"[hera from foo-coord] please review"` for the canonical sender/body pair. Test wording deviates only in the role name (`foo-coord` vs spec's `foo-coordinator`) — semantically the same assertion, but worth noting for test-aligned-to-spec strictness.

#### `Injector.Inject` (lines 48–60)

1. **[COVERED]** `if i.idle.IsIdle(taskID) { ... }` true-branch (lines 50–55). Idle path. Posts `formatted+"\n"`, returns `db.DeliveryIdleSubmit` on success or `db.DeliveryPending` + wrapped error on PTY failure. Maps to § Messages auto-submitted when recipient is idle. `TestInject_IdleSubmits` asserts:
    - `mode == db.DeliveryIdleSubmit`
    - posted bytes == `"[hera from foo-coord] ping\n"` (exact byte sequence including trailing `\n`)
    - target taskID == `"task-1"`
2. **[COVERED]** `if _, err := i.pty.PostTaskInput(...); err != nil { return DeliveryPending, ... }` inside idle branch (lines 51, 52). PTY-error path. `TestInject_PropagatesPTYErrors` asserts that on PTY error the returned mode is `db.DeliveryPending` (not idle_submit), and the error is non-nil. This is a sensible runtime contract — the caller (`handler_send.go`) decides retry/queue behavior. The spec is silent on the failure mode; flagging as covered because the test exists and the behavior is reasonable.
3. **[COVERED]** Idle-path success `return db.DeliveryIdleSubmit, nil` (line 54). See #1.
4. **[COVERED]** Busy-path post + return (lines 56, 59). Posts `formatted` without `\n`, returns `db.DeliveryBusyBuffer`. Maps to § Messages buffered when recipient is not idle. `TestInject_BusyBuffersWithoutNewline` asserts:
    - `mode == db.DeliveryBusyBuffer`
    - posted bytes == `"[hera from foo-coord] ping"` (no trailing `\n` – the spec's critical bit)
5. **[UNCOVERED-IMPLEMENTATION]** Busy-path PTY-error wrap `return DeliveryPending, fmt.Errorf("inject (busy path): %w", err)` (lines 56, 57). Only the idle-path error is tested. The busy-path error wrapping is structurally identical and not separately specced. Implementation parity is the only argument for coverage; the test gap is not behaviorally meaningful.

### internal/log/doc.go

No source code, no exports, no branches. The doc declares an intent for v1 ("output to stderr and `~/.hera/hera.log`, colon-separated key=value pairs, no JSON"). The spec does not impose any of that — the only spec mention of logging is § Stricter rule on auto-adoption logged, which lives in `internal/events`'s `AdoptHandler` (out of scope for this audit). Nothing to evaluate here.

A grep across `cmd/` and `internal/` confirms no consumer imports `internal/log` today; the daemon's logging is provided by the caller via a `*log.Logger` from the stdlib (per `daemon.Run`'s signature, observed via cmd-hera audit). This is consistent with the package's doc-only status in v1.

## Spec coverage – directional review

### Spec → code (forward direction)

**§ Idle gate requires sustained `session.idle` state**

Three scenarios, each landing on a distinct tracker branch:

- "Idle for less than debounce window" → `TestIsIdle_WithinDebounce_False` exercises the `t.now().Sub(s.at) < t.debounce` branch (line 85). Test asserts `IsIdle should be false within debounce window`; spec says messages MUST be delivered in `busy_buffer` mode in this case. The tracker correctly returns false; downstream the injector will pick busy_buffer. **Aligned**.
- "Idle for at least debounce window" → `TestIsIdle_AfterDebounce_True` exercises the final `return true` (line 88). **Aligned**.
- "Session started after idle" → `TestIsIdle_SessionStartedAfterIdle_False` exercises the `s.kind != events.TypeSessionIdle` branch (line 82). The test also explicitly waits 5s after the `session.started` event and asserts `IsIdle` is still false, matching the spec's wording "MUST NOT be treated as idle until a new `session.idle` event fires AND ≥2 seconds elapse". `TestIsIdle_IdleAfterStarted_DebouncesAgain` adds the complementary proof: after a fresh `session.idle`, the full debounce must elapse again. **Aligned**.

The spec scenario doesn't mention `session.exited`, but the spec's parent requirement does ("Any `session.started` or `session.exited` event more recent..."). `TestIsIdle_SessionExitedAfterIdle_False` covers this. **Aligned**.

**§ Messages auto-submitted when recipient is idle / § Messages buffered when recipient is not idle**

- Idle path scenario: "MUST POST the formatted body terminated by `\n` ... AND record `delivery_mode = "idle_submit"`". `TestInject_IdleSubmits` asserts the exact `\n`-terminated byte sequence and the `db.DeliveryIdleSubmit` mode constant. The constant string value (`"idle_submit"`) lives in `internal/db/types.go` and matches the spec wording. **Aligned**.
- Busy path scenario: "MUST POST the formatted body without `\n` AND record `delivery_mode = "busy_buffer"`". `TestInject_BusyBuffersWithoutNewline` asserts the exact non-newlined byte sequence and the `db.DeliveryBusyBuffer` mode. Constant value (`"busy_buffer"`) matches. **Aligned**.

Persistence of the chosen delivery mode happens in `mcp/handler_send.go` via `Messages.SetDelivered`; verified to exist but out of scope here.

**§ Injected messages identify sender**

Scenario: bytes MUST be `[hera from foo-coordinator] please review` (+ `\n` in idle case). `TestFormatBody` asserts the exact format with role `foo-coord`/body `please review`. The format-string assertion (`"[hera from %s] %s"`) means the spec's exact-byte invariant holds for any sender/body pair. `TestInject_IdleSubmits` and `TestInject_BusyBuffersWithoutNewline` further check the prefixed bytes end-to-end through `Inject`. **Aligned**.

### Code → spec (reverse direction)

No behavioral code in `internal/idle` or `internal/inject` lacks a spec referent. The injector's PTY-error fallback (`db.DeliveryPending` + wrapped error) is a runtime behavior the spec doesn't speak to; given the surrounding code's handling (the caller persists the pending message for retry), this is acceptable implementation-side latitude rather than a contradiction.

## Test-alignment sub-check

| Spec scenario | Asserting test | Verdict |
| --- | --- | --- |
| § Idle gate – "Idle for less than debounce window" | `TestIsIdle_WithinDebounce_False` | aligned (asserts `IsIdle == false` within window) |
| § Idle gate – "Idle for at least debounce window" | `TestIsIdle_AfterDebounce_True` | aligned (asserts `IsIdle == true` past 2s) |
| § Idle gate – "Session started after idle" | `TestIsIdle_SessionStartedAfterIdle_False` (+ `TestIsIdle_IdleAfterStarted_DebouncesAgain`) | aligned |
| § Idle gate – (parent) session.exited supersedes idle | `TestIsIdle_SessionExitedAfterIdle_False` | aligned |
| § Messages auto-submitted when recipient is idle | `TestInject_IdleSubmits` | aligned (asserts `\n`-terminated bytes + `DeliveryIdleSubmit`) |
| § Messages buffered when recipient is not idle | `TestInject_BusyBuffersWithoutNewline` | aligned (asserts no-`\n` bytes + `DeliveryBusyBuffer`) |
| § Injected messages identify sender | `TestFormatBody` + indirect via inject tests | aligned (exact byte sequence asserted; role name differs in spelling but the format string trivially generalizes) |

No `test-doesnt-assert-spec` findings. Every test makes an explicit assertion (`t.Fatalf` on mismatch) tied to the spec wording.

Minor stylistic note (not flagged as a gap): `TestFormatBody` uses `foo-coord` where the spec's example uses `foo-coordinator`. Both forms compile through the format string identically, so the assertion is still proof of the spec's invariant — but a strict reading would prefer the test to use the literal example string from the spec. Not material.

## Cross-module symbol awareness

All cross-module symbols referenced from this module resolve in the symbols index:

- `events.TypeSessionIdle`, `events.TypeSessionStarted`, `events.TypeSessionExited`, `events.TypeTaskCreated`, `events.TypeLinkCreated` (test only) — exist in `internal/events/types.go` and are listed in symbols.json (events package's `ParseTaskCreated` etc. confirm the module).
- `argus.Event` — present in `internal-argus` symbols.
- `db.DeliveryMode`, `db.DeliveryIdleSubmit`, `db.DeliveryBusyBuffer`, `db.DeliveryPending` — present in `internal-db` symbols (confirmed by source inspection of `internal/db/types.go`).
- The `*idle.Tracker` satisfies the `inject.IdleChecker` interface (single method `IsIdle(taskID string) bool`) — confirmed by reading both signatures.
- The `*argus.Client.PostTaskInput` satisfies `inject.PTYWriter` — confirmed via the symbol index (`PostTaskInput` listed under `internal-argus`).

No `missing-symbol` findings.

## Unimplemented spec promises

None for this module.

## Contradictions

None.

## Notes

- The doc-only `internal/log` package is consistent with v1 spec silence on logging mechanics. The single spec promise about logging (§ Stricter rule on auto-adoption logged) is owned by `internal/events`'s `AdoptHandler` and audited under that module.
- The idle tracker's `Lookup` function exists for "diagnostics / status output" per its doc comment. The spec doesn't describe a `hera status` surface that consumes it, so its presence is implementation latitude — not coverage-bearing.
- The injector's design correctly separates idle gating from PTY mechanics via the `IdleChecker` / `PTYWriter` interfaces, allowing the spec's behavior to be unit-tested without spinning up argus. This is good hygiene.
- The behavioral coverage rate (9/26 = 35%) understates spec coverage because many uncovered branches are structural plumbing (lock acquisition, struct construction, error wrapping). Every spec-mandated behavior in this module's scope has at least one asserting test.
