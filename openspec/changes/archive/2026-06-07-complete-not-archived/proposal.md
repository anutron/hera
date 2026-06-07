# Completed is not archived

## Why

Operator QA: pressing `s` to step a task to `complete` made the row vanish from its coordinator's active children as if it had been archived — with NO archive flag set anywhere (argus `archived=0`, hera `archived_at` NULL). Pure display mis-bucketing: `populateRail` classifies a row `Dead` via `TaskAliveChecker.IsTaskAlive`, which maps STATUS strings (`complete`, `failed`, `stopped`, …) to dead — so a finished-but-existing task buckets into the Archive expando. Argus's own panel never does this: status drives the glyph, never the bucket. A completed coord task is likewise skipped from `CoordTaskID`, unbinding the header's pane and icon.

The per-row `IsTaskAlive` check is also a synchronous argus HTTP roundtrip (`GET /api/tasks/{id}`) per live-bound role executed inside `populateRail` — which runs ON the tview event loop (`RepopulateRail` → `QueueUpdateDraw`). Every rail rebuild serialized N network calls ahead of input processing.

## What Changes

- **Dead means record-gone, ONLY:** a rail row is `Dead` if and only if the argus task RECORD no longer exists (404 / pruned), as observed by the argus state cache (a cache miss once the cache is warm). Task STATUS never affects bucketing.
- **Status never buckets:** a completed (or failed/stopped) task whose argus record still exists renders in the ACTIVE tree with its status glyph (green `✓` for complete), selectable like any row. Only hera `archived_at`, argus `archived`, and record-nonexistence bucket a row into an Archive expando.
- **Coord headers:** a coord whose task is completed-but-existing still feeds `CoordTaskID`/`CoordStatus`, so the header keeps its pane binding and renders `✓`; only a record-gone (or archived) coord binding is skipped as a tombstone.
- **No per-row HTTP:** `populateRail` derives deadness from the state cache snapshot instead of calling `TaskAliveChecker` per row, removing N synchronous argus roundtrips from the event loop per rail rebuild. `IsTaskAlive` (status-based) remains for initial pane selection only, where preferring a live task is the point.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: new requirement — task status never buckets a rail row; Dead is record-nonexistence only.

## Impact

- `internal/view/app.go`: `populateRail` drops the `TaskAliveChecker` per-row calls; `dead` computed from the `TaskStateProvider` cache (miss + cache warm = record gone) via a shared `taskGone` helper; `applyArgusState` reuses the helper. `findInitialSelection` unchanged.
- `internal/view/rail_list.go`: `roleEntry.Dead` / `roleArchived` doc comments corrected (Dead = record gone, NOT completed).
- Tests: completed+existing rows (worker, coord header, freelancer) assert active rendering; record-gone rows still bucket; spinner `hasRunning` clears when the last running row leaves.
