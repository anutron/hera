# Icon alignment

## Why

Operator QA (R3) surfaced the "blue check" confusion: hera renders an `in_review` task as a BLUE CHECKMARK. Argus never renders a blue check — `in_review` is the blue moon-with-stars (`󰖔` U+F0594). Because hera mapped both `in_review` and `complete` to `✓` (differing only by color), stepping a task through statuses looked like a subtle color shuffle, breaking the operator's argus muscle memory. Other mappings drifted too: `pending` rendered the moon outline instead of argus's `○`, `in_progress`+idle rendered dimmed instead of argus's blue, and `in_progress`+running rendered a static moon-stars instead of argus's animated spinner.

## What Changes

- **Glyph table:** hera's status glyph mapping mirrors argus's task-panel table EXACTLY (argus `internal/tui/theme/theme.go:29-34` + `internal/tui/taskview/tasklist.go:1095-1132`): `pending` → `○` gray; `complete` → `✓` green; `in_review` → `󰖔` (U+F0594) blue; `in_progress`+needs-input → `` (U+F059) #faa378; `in_progress`+idle → `` (U+F186) blue; `in_progress`+running → animated spinner frames (U+EE06..U+EE0B) orange.
- **Visual distinctness:** `in_review` is visually distinct from `complete`; no checkmark renders for any non-complete state.
- **needs-input scoping:** the needs-input override applies within `in_progress` only, mirroring argus's switch nesting (the API only serves `needs_input` for `in_progress` anyway).
- **Spinner animation:** running rows animate by wall clock (argus's cadence, 150 ms/frame) via a spinner driver that schedules the existing redraw coalescer only while a running row is visible.
- **Idle variant:** argus's TUI-only `idleUnvisited` (moon-stars for unvisited-idle) is NOT in the API; hera renders plain moon `` for all `in_progress`+idle.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: status glyph table mirrors argus exactly (new requirement); the rail-tree requirement's status-icon vocabulary references the corrected table (modified requirement).

## Impact

- `internal/view/rail_list.go`: `stateGlyph` rewritten to the argus table; spinner frames + wall-clock frame derivation; `statusIcon`/`roleIcon`/`orchIcon` thread the animation frame; `hasRunning` flag for the spinner driver.
- `internal/view/app.go`: spinner driver goroutine (150 ms ticker → `redraw.Schedule()` while the rail has a running row), stopped in `Close`.
- Tests: `rail_list_test.go` mapping expectations updated to the argus table; new table-driven glyph test; spinner-frame and spinner-driver tests.
