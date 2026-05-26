**Design doc:** `openspec/changes/fix-idle-submit-cr/design.md`

**Acceptance criteria:** `openspec/changes/fix-idle-submit-cr/specs/hera-coordination/spec.md` (each `#### Scenario:` is one acceptance criterion).

## 1. Spec delta

**Depends on:** nothing.

- [x] 1.1 Write the modified-requirements delta at `openspec/changes/fix-idle-submit-cr/specs/hera-coordination/spec.md` amending "Messages auto-submitted when recipient is idle" and "Auto-inject master switch gates idle-submit path" to specify `\r` instead of `\n`, with a one-line WHY (raw-mode PTY; CR is the Enter key byte).
- [x] 1.2 `openspec validate fix-idle-submit-cr --strict` passes.

## 2. Tests (TDD: assert the new byte first, watch them fail)

**Depends on:** Stage 1.

- [x] 2.1 Update `internal/inject/inject_test.go` idle-path assertions: `TestInject_IdleSubmits` `want` body changes from `"[hera from foo-coord] ping\n"` to `"[hera from foo-coord] ping\r"`. `TestInject_DefaultAutoInjectEnabledIsTrue` `got` check changes from `"[hera from foo] ping\n"` to `"[hera from foo] ping\r"`. `TestInject_AutoInjectReEnabledRestoresIdleSubmit` `got` check changes from `"[hera from foo-coord] second\n"` to `"[hera from foo-coord] second\r"`.
- [x] 2.2 Confirm busy-path assertions in `TestInject_BusyBuffersWithoutNewline`, `TestInject_AutoInjectDisabledForcesBusyBufferEvenWhenIdle`, `TestInject_AutoInjectOnButBusyStillBuffers` are unchanged (no trailing byte).
- [x] 2.3 Run `go test ./internal/inject/...` — idle-path tests fail with `"...\n"` vs `"...\r"` diff (proves the test now drives the fix).

## 3. Implementation

**Depends on:** Stage 2.

- [x] 3.1 In `internal/inject/inject.go`, change the idle branch's `[]byte(formatted+"\n")` to `[]byte(formatted+"\r")`. The doc comment above `Inject` mentions `"\n"` in the idle-submit description — update that comment to `"\r"` as well.
- [x] 3.2 Run `go test ./internal/inject/...` — green.
- [x] 3.3 Run `go test ./... -race -count=1` — green.

## 4. Archive

**Depends on:** Stages 1–3.

- [x] 4.1 `openspec validate fix-idle-submit-cr --strict` still passes after all checkboxes flip.
- [ ] 4.2 Aaron rebuilds the daemon and confirms live: a hera_send to an idle recipient now actually submits (no manual Enter required).
- [ ] 4.3 `openspec archive fix-idle-submit-cr` merges the delta into `openspec/specs/hera-coordination/spec.md`.
