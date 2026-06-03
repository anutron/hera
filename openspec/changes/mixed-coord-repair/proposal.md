# Mixed-coord repair-first `a` + freelance dedupe

## Why

Operator hit the mixed-coord trap live: an orchestrator ACTIVE hera-side with live workers (renders on the rail) whose COORD's argus task is ARCHIVED — only external argus-side archiving produces this now. Today the header renders no hint that the coord is broken, and `a` on the header would cascade-archive the WHOLE orchestrator (display-active → archive direction), so the only repair path was leaving hera for argus to unarchive the coord task.

Separately, a known wart: a task whose hera bindings are all ended falls back into the Freelance section (the rail-truthfulness fallback) even when its ORCHESTRATOR header already renders on the rail — observed live as `archive-this-coord` rendering both as a collapsed orchestrator header and as a freelance row. The fallback exists for findability; when the header renders and carries the task as its coord-pane binding, findability is already preserved and the freelance row is a duplicate.

## What Changes

- **Visible mixed-coord cue:** when an active (non-archived) orchestrator's coord task is argus-archived, the header's status icon renders `⊘` (U+2298, circled division slash) in error red instead of the normal status glyph — the operator SEES "this coord is broken/archived" at a glance. Cue choice: `⊘` reads "void/blocked"; error red is distinct from the orange needs-input `?`, from the dimmed-archived treatment, and from the cyan 󰹻 coord marker beside it.
- **Repair-first `a`:** pressing `a` on a header in this mixed state UNARCHIVES the coord's argus task (aligning argus reality to the displayed active orchestrator) via the existing task-direct unarchive verb (`ToggleArchiveTask`, 404 = skip per existing tolerance), instead of cascade-archiving. Once repaired — or whenever the header is not in the mixed state — `a` behaves exactly as today. Enter on such a header keeps current behavior; the repair-first `a` is the affordance.
- **Freelance fallback dedupe:** the freelance fallback excludes tasks reachable via a rendered orchestrator — a task that is some rendered orchestrator header's coord-pane binding (`CoordTaskID`) no longer ALSO renders as a freelance row. Truly orphaned tasks (no rendered header carries them — e.g. the coord role is hera-archived so the header binds nothing, or the orchestrator itself is hidden) still fall back, preserving rail-truthfulness.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: new requirement — mixed-coord headers render a repair cue and `a` repairs first; modified requirements — `a` toggle direction on orchestrator headers (repair-first carve-out) and the Freelance fallback rule (rendered-header dedupe).

## Impact

- `internal/view/rail_list.go`: `orchEntry.CoordArgusArchived` field; `orchIcon` renders the `⊘` repair cue for the mixed state.
- `internal/view/app.go`: `populateRail` captures the coord task's argus `Archived` bit into the entry; rendered-set now also collects each entry's `CoordTaskID` so `buildFreelance` skips header-reachable tasks; `CurrentRailSelection` carries `CoordTaskID` + `CoordArgusArchived` on orchestrator rows.
- `internal/view/mutations.go`: `railSelection` gains `CoordTaskID`/`CoordArgusArchived`; `OnArchive`'s orchestrator branch dispatches the task-direct unarchive (repair) before the cascade-archive path.
- Tests: mixed-state cue rendering; repair-first `a` (unarchive call, no cascade); repaired/non-mixed headers cascade as before; freelance build excludes header-reachable tasks while keeping truly-orphaned ones.
