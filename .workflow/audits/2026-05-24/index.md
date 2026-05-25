# Spec Audit – 2026-05-24

First audit of `hera-coordination` capability (the only capability in this project's base spec at present). Run in **full** mode on commit `5f9441c`.

## Summary

- Modules analyzed: 7
- Total behavioral branches: 596
- Covered by specs: 343 (58%)
- Uncovered-implementation (no spec needed): ~217 (CLI verbs, defensive checks, getters, lock acquisition)
- Behavioral gaps (verified): 21
- Contradictions (verified): 3
- Unimplemented spec promises: 2
- Test-alignment gaps: 30
- False positives suppressed: 0 (verification skipped — most findings reference symbols that point at the issue, not claims of missing-from-codebase)

The 58% raw coverage number understates correctness. The bulk of uncovered branches are CLI plumbing (cmd-hera) and implementation-detail control flow (lock acquisition, defensive nil guards, getter helpers). The high-signal numbers are the gap categories below.

## Coverage by Module

| Module | Branches | Covered | Behavioral gaps | Contradictions | Promises unimpl. | Test-alignment gaps |
|--------|----------|---------|-----------------|----------------|------------------|---------------------|
| cmd-hera | 64 | 1 (1.6%) | 6 | 0 | 1 | n/a |
| internal-db | 168 | 124 (74%) | 0 | 0 | 0 | 4 |
| internal-argus | 77 | 73 (95%) | 2 | 2 | 0 | 3 |
| internal-events | 72 | 28 (39%) | 2 | 1 | 0 | 4 |
| internal-mcp | 150 | 95 (63%) | 11 | 0 | 1 | 17 |
| internal-runtime (idle+inject+log) | 26 | 9 (35%) | 0 | 0 | 0 | 0 |
| internal-wiring (daemon+config) | 39 | 13 (33%) | 0 | 0 | 0 | 2 |

`internal-runtime` and `internal-wiring` have low raw coverage because most of their branches are init/teardown plumbing. Both modules have **zero** behavioral or test-alignment gaps – the spec-described behaviors are fully covered.

`cmd-hera`'s 1.6% reflects that the CLI verbs are mostly plumbing the spec doesn't describe (start/stop/status/list/resume's user-facing details). The one covered branch is the `--foreground`-required check; the rest are output formatting, PID-file handling, and signal wiring.

## Top Gaps (by impact)

### High severity – fix before dogfooding

1. **`hera_join` skips the meta:hera.role mirror.** When `hera_join` (freelance or coordinator-bootstrap path) creates a binding, it does NOT write `meta:hera.role` to the bound argus task. Auto-adopt does this correctly via `internal/events/adopt.go:128`; the freelance/bootstrap path in `internal/mcp/handler_join.go:171` doesn't have an `*argus.Client` reference and never makes the call. Spec § "Role metadata mirrored to argus task_meta" / scenario "Role meta written on binding" requires this on every binding creation. (internal-mcp-1, unimplemented promise)

2. **`hera_send` tests don't assert the persisted delivery_mode.** `TestSend_*` asserts `out.DeliveryMode` in the response JSON but never fetches the message row from the DB to confirm `db.Messages.SetDelivered` actually persisted the value. Spec § "Messages auto-submitted when recipient is idle" / "Messages buffered when recipient is not idle" MUSTs the row update. (internal-mcp-8, test-alignment high)

3. **No tests for `ResyncHandler`.** `internal/events/resync.go` is wired into `daemon/run.go:97` and is behaviorally correct, but the spec scenario "Resync triggers task snapshot" has no assertion at the events-handler layer. Every branch in resync.go is uncovered by tests. (events-bg-1 / events-ta-1)

4. **`adopt.go:86-88` skipped-adoption log omits parent task id.** Spec § "Stricter rule on auto-adoption logged" mandates the log entry MUST include "the new task's id, the parent task's id, and the missing meta key." The wrong-meta-value branch logs only `child` and `value` — no `parent`. The sibling branch at 81-83 does include `parent`, likely an oversight. (events-c-1, contradiction)

### Medium severity – fix soon, optional for v1

5. **No test for the mission/constraints-absent → empty-strings path.** Spec § "Auto-adopt copies mission and constraints from task meta" requires that if meta is absent, the role columns be empty strings (not NULL). Tested for the happy path; no test for the absent path. (events-bg-2)

6. **Skipped-adoption INFO log isn't verified by tests.** Spec requires the log; no test captures slog output to confirm the line is emitted. (events-ta-2)

7. **`hera_status` meta-mirror is best-effort.** Spec § "Role metadata mirrored to argus task_meta" / scenario "Thread status meta updated on `hera_status`" MUSTs the meta write. Code swallows PutTaskMeta failures and returns success with `meta_mirrored: false`. Operationally defensible (a transient argus outage shouldn't break status calls), but not strictly spec-compliant. (internal-mcp-2)

8. **`queued_no_binding` delivery mode in code, not in base spec.** The spec's enumerated modes are `idle_submit` and `busy_buffer`. The code introduces `queued_no_binding` for no-live-binding recipients. The archived design doc mentions it; the base spec doesn't. Either spec edit or code change. (internal-mcp-3)

9. **`kind="coordinator"` accepted by hera_join but not described in spec.** `hera_join` accepts `kind=coordinator` for the orchestrator-bootstrap path (also documented in the input_schema enum). The base spec describes freelance-attach but doesn't enumerate coordinator-bootstrap-via-join as a supported call shape. (internal-mcp-mentioned in surface gaps)

### Low severity – polish, defer if needed

10. Cwd → task lookup uses exact-string equality. No normalization (filepath.Clean, symlink resolve). Spec doesn't pin match semantics. Trailing slashes / symlinked worktrees produce ErrCwdUnknown. (internal-mcp-4)

11. `internal/argus/events.go:67-69`: when cursor is 0, the `since=` query param is omitted. Spec doesn't define cursor-0 as a sentinel for "no cursor." Edge case; cursor table starts at 0 by default and advances on first event. (internal-argus-1)

12. `StreamEvents` doc comment doesn't mention the resync requirement. A future caller could subscribe and ignore resync. Documentation gap, not a behavioral one. (internal-argus-3)

13. `cmd-hera` CLI verbs (status / list / stop / resume) have 6 minor uncovered-behavioral findings (output formatting, exit codes, --foreground enforcement). All reasonable; spec doesn't describe CLI ergonomics.

14. 17 test-alignment findings in `internal/mcp` – most are "test exercises the path but doesn't assert the exact spec wording." Low individual severity; collectively meaningful.

15. 4 test-alignment findings in `internal/db` – similar pattern.

16. 3 test-alignment findings in `internal/argus` – similar.

17. 2 test-alignment findings in `internal/wiring` – `TestDaemonStart_TokenMissing` doesn't assert the actionable `argus token mint --scope hera` wording; `TestLoadToken_FileEmpty` asymmetric with `TestLoadToken_FileMissing`.

## Verified False Positives

None. Verification was skipped (Phase 2.5) because most findings reference symbols as **pointers** (the symbol-at-this-location is the issue), not as **missing-from-codebase claims**. The few unimplemented-promise findings are legitimate (verified by inspection).

## Delta from Last Audit

First audit. No delta available.

## Where to look next

- Per-module reports: `.workflow/audits/2026-05-24/modules/*.md`
- Per-module findings JSON: `.workflow/audits/2026-05-24/modules/*.findings.json`
- Inventory: `.workflow/audits/2026-05-24/inventory.json`
- Symbols index: `.workflow/audits/2026-05-24/symbols.json`
- Config: `openspec/.audit-config.json`
