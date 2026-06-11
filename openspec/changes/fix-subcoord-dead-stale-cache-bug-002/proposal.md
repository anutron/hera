# Rail does not classify a live task dead from a stale state-cache snapshot (BUG-002)

## Why

On orchestrator `sherlock-3b`, the parent-link rows for sub-coordinators `1c-team` and `1d-team` were classified `Dead` and bucketed, stranding their subtrees (the operator saw the rail collapse from ~30 roles to ~1 visible row). Argus's own state disagreed: MCP `task_list` (project Sherlock) reported both coord tasks LIVE and NON-archived (`1c-add-memory-svc` = complete, `1d-add-playbook-svc` = in_progress, neither archived). The operator archived and pruned nothing. So hera classified as `Dead` rows that argus considers alive.

This is the RESIDUAL ROOT CAUSE behind BUG-001: BUG-001 (PR #151) kept a bucketed sub-coord's subtree REACHABLE inside the Archive expando, but did not explain WHY the parent-link rows bucketed in the first place.

Investigation ruled out the filed hypotheses except staleness:

- **NOT pagination / a fetch cap.** Argus's `GET /api/tasks` handler (`internal/api/handlers.go`) returns ALL matching tasks with no `limit`/`offset`. The MCP `task_list` and the HTTP list read the IDENTICAL `db.Tasks()` query (`SELECT … FROM tasks ORDER BY created_at ASC`, no cap). Argus is a singleton daemon with one DB, so both surfaces see the same rows.
- **NOT an archived filter.** Hera's cache fetches `GET /api/tasks?archived=all`; the missing tasks were non-archived anyway.
- **CONFIRMED: a staleness / refresh gap (hypothesis 3).** Deadness is read from hera's argus state cache (`internal/view/taskstate.go`), which polls `/api/tasks?archived=all` every 2s. The cache's `Ready()` latch flips true after the FIRST successful poll and NEVER resets. The poll error path (argus bounced / hung / slow — the HTTP client has a 30s timeout) returns early and RETAINS the frozen snapshot. `taskGone` (`internal/view/app.go`) classified ANY cache miss `Dead` the instant `Ready()` was true — with no check that the snapshot was still FRESH. So once polling stopped succeeding, every task that was created or changed after the freeze was absent from the frozen snapshot and read `Dead`, even though argus reported it live. A long orchestrator session that outgrew an early freeze collapses to the one still-attached row — exactly the reported symptom.

## What Changes

- **The state cache tracks snapshot freshness.** `ArgusStateCache` records the wall-clock time of the most recent SUCCESSFUL poll and exposes `Fresh()` — ready AND a successful poll within a staleness window (8× the poll interval, floored at 15s). A frozen snapshot (polls erroring while the snapshot is retained) goes stale; `Fresh()` returns false while `Ready()` stays latched true. A brief blip within the window stays fresh.
- **Deadness gates on freshness, not just readiness.** `taskGone` returns `Dead` only when the cache is BOTH ready AND fresh. A STALE cache reports a miss as "unknown", not "gone": the row stays in the active tree, driven by its live / most-recent binding, until a fresh poll can confirm the task's true state. The genuine prune case is unaffected — while polls keep succeeding (fresh), a task truly absent from the snapshot still buckets `Dead`.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: tightens the deadness-classification invariant so a row is classified `Dead` only from a FRESH state-cache snapshot — a stale snapshot (polling stopped succeeding) MUST NOT bucket a task argus still reports live.

## Impact

- `internal/view/taskstate.go` — `ArgusStateCache` gains `lastSuccess`/`staleAfter`/`now` and a `Fresh()` method; `poll` stamps `lastSuccess` on success.
- `internal/view/session.go` — `managerPaneSource.StatesFresh()` delegates to `ArgusStateCache.Fresh()`.
- `internal/view/app.go` — `taskGone` adds the `StatesFresh` gate.
- `internal/view/taskstate_test.go` — unit tests for `Fresh()` (fresh after poll, false cold, stale after sustained failure, tolerant of a brief blip).
- `internal/view/bug002_stale_cache_dead_test.go` — regression tests: a stale-but-ready cache does NOT classify a live-bound task `Dead`; a fresh cache still buckets a genuinely-gone task.

## Notes

- Classification: CODE-DRIFT. The base `hera-view` requirement already states "A cold cache MUST NOT transiently classify rows dead." The fix extends that intent to a STALE cache (the gap the cold-cache latch left open). The added requirement locks the stale-cache scenario against regression; no existing requirement wording is loosened.
- The argus-side observation that `db.Tasks()` silently skips scan-error rows (a partial-but-200 response could drop tasks while the cache stays fresh) is a SEPARATE, argus-side concern outside hera's control; reported to the coordinator, not fixed here.
