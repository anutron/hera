## Context

The rail (`railList`) already owns row-building, cursor movement, collapse/fold state, and drawing. Key events reach the rail through two layers: the `KeyRouter` (`keys.go`, installed as tview's `SetInputCapture`) intercepts most keys in `handleRail` (Enter, j/k→Down/Up, the mutation runes `n`/`r`/`a`/`l`/`s`/`S`/`P`/`?`, Ctrl-D/R/P, Esc-release), and anything it does not consume propagates to the rail widget's `InputHandler` (KeyDown/KeyUp/space).

Argus's `TaskListView` already implements exactly the filter UX the operator asked for (`internal/tui/taskview/tasklist.go`): `/` enters input mode, runes append, Backspace deletes, `Esc` clears + exits, `Enter` keeps the filter but exits input mode, and while a filter is active every project/archive section auto-expands so matches are visible. We mirror it.

## Goals / Non-Goals

- **Goal:** `/` filters the rail by name (case-insensitive substring, whitespace terms), ancestry-preserving, Esc restores, Enter accepts.
- **Goal:** the `›` marker renders only under the probe env; normal operation shows no marker and no width shift.
- **Non-Goal:** fuzzy matching, regex, or filtering by status/state. Substring-on-name (and repo, for parity with argus) only.
- **Non-Goal:** persisting the filter across sessions or DAO refreshes beyond what `buildRows` naturally re-applies.

## Decisions

### Filter state lives on `railList`

`railList` gains `filtering bool` (input mode active) and `filter string` (the query). This matches argus and keeps `buildRows` — the single place that flattens orchestrators/freelance/archive into `rows` — as the one place the filter is applied. `SetFilter`/`BeginFilter`/`AcceptFilter`/`ClearFilter`/`HandleFilterKey`/`Filtering` are the rail's filter API.

### `matchesFilter` + ancestry preservation

`matchesFilter(name string)` lowercases the query, splits on whitespace, and requires every term to be a substring of the candidate (the row name; for freelance rows the repo also counts, mirroring argus). `buildRows`, when `filter != ""`:

- A **coordinator** renders if it matches OR any of its (recursively walked) roles match — so a matching agent always keeps its parent header (ancestry). When it renders, ALL its children that match render under it; if the coordinator itself matches but no child does, its children still render (so selecting a matched coordinator shows its team).
- **Auto-expand**: `orchCollapsed` returns false while a filter is active (like argus's `filterActive` override), so matches are never hidden behind a fold. Freelance groups and the Archive expando likewise force-open while filtering.
- A **freelance repo group** renders the tasks that match (or all, if the repo name matches); the group header renders when it has at least one visible task.
- Section separators (Pinned/Freelance) and the Archive expando render only when they have visible content, so the operator never lands on an empty section.

The cursor is preserved across the rebuild by the existing `restoreCursor`; if the previously-selected row filtered out, it falls to the first selectable row.

### Router yields to the rail while filtering (mirrors the modal gate)

The `KeyRouter` already yields every key to the focused widget when a modal is active (`Modal ModalGate`). We add the same shape for filtering: a `RailFilter` interface with `IsFiltering() bool`. In `HandleKey`, after the modal check, if RAIL is focused and the rail is filtering, the event is passed straight through to the rail's `InputHandler` — so `n`/`r`/`a`/Esc/Enter/runes are filter input, NOT mutation triggers or the Esc-release frame.

Entering filter mode: `/` is currently unbound and already propagates to the rail. The rail's `InputHandler` handles `/` (when not already filtering) by calling `BeginFilter`, and routes all keys to `handleFilterKey` while filtering. Because `/` propagates today, no router change is needed to ENTER; only the yield-while-filtering gate is added so the router stops stealing keys once input mode is on.

`j`/`k` semantics: while in INPUT mode they are filter text (the router yields, so they reach the rail as runes and append) — matching argus, where arrows navigate during input. After `Enter` (input mode off, filter still applied) the router resumes translating `j`/`k`→Down/Up so navigation moves through the filtered set. Arrow keys (Up/Down) navigate during input mode too.

### Marker gate: reuse the probe env `HERA_LIVE_PROBE`

The live-probe harness (`internal/daemon/live_probe_test.go`) is gated on `HERA_LIVE_PROBE=1`; that is the existing probe env. The marker is rendered server-side by the daemon, so for a probe capture to show it the daemon must be launched with `HERA_LIVE_PROBE=1` in its environment (the `hera-view-probe` skill's redeploy sets it). `railList` reads the env once at construction into a `probeMarker bool` field (tests set it directly). `Draw` renders the `›` only when `probeMarker && cursor`; the `markerGutter` width is reserved unconditionally so nothing shifts. Normal operation (env unset) → blank gutter, selection shown by `theme.StyleSelected`.

## Risks / Trade-offs

- **Router yield correctness:** if the filter gate is wrong, keys could be double-handled (mutation AND filter). Mitigated by gating strictly on `IsFiltering()` and RAIL focus, and by tests asserting that a mutation rune typed while filtering appends to the query instead of firing.
- **Probe visibility depends on daemon env:** the marker only reappears for the probe if the daemon process has `HERA_LIVE_PROBE=1`. Documented in the skill; the gate itself is correct regardless.
