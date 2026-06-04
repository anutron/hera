## Why

Two operator-driven rail refinements, bundled because both touch rail rendering and key routing:

1. **No rail search.** As the rail fills with coordinators, agents, and freelancers, there is no way to narrow it to the rows the operator cares about. Argus's task list solves the same problem with a `/` filter; hera's rail should mirror it so the operator never has to scroll a long rail to find one agent.

2. **The `›` selection marker is unwanted.** The selection-marker change added a `›` glyph in a left gutter on the selected row to make the cursor identifiable in colorless captures. The operator does not want to see it in normal use — selection is already shown by the pink `theme.StyleSelected` text. But the live-probe harness relies on the glyph to locate the cursor in its text captures, so it cannot be deleted outright; it must be gated so the operator never sees it while the probe still does.

## What Changes

- Pressing `/` while RAIL is focused enters a **rail search/filter input mode**: typing narrows the rail to rows whose name matches the query (case-insensitive substring), across coordinators, agents, and freelancers. Whitespace-separated terms each match name or repo (mirroring argus's filter semantics).
- While a filter is active the rail stays **ancestry-preserving**: a matching agent keeps its parent coordinator header (and Freelance / Archive section headers) visible so the tree stays legible, and all sections auto-expand so matches are never hidden behind a fold.
- **`Esc`** exits search and restores the full rail (clears the query). **`Enter`** accepts the filter — it keeps the query applied but leaves input mode, returning to navigation so `j`/`k` move through the filtered set. (Chosen over Esc-only so the operator can hold a filter while navigating, matching argus.)
- The active query renders as an unobtrusive **`/ <query>` input line** at the bottom of the rail while in input mode, and the rail title shows the active filter (`Rail /query`) once accepted, so the operator always sees what is filtering the view.
- The `›` **selection marker is gated behind the probe env** (`HERA_LIVE_PROBE`, the same var that gates the live-probe harness): unset (normal operation) the selected-row gutter renders a blank cell — no glyph, no width shift — and selection is shown by `theme.StyleSelected` alone; set (probe runs) the `›` renders exactly as before so text captures can still locate the cursor.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the rail gains a `/` name filter (ancestry-preserving, Esc-restores, Enter-accepts) and the selection marker becomes probe-gated (rendered only when the probe env is set; normal rendering shows selection via style alone).

## Impact

- `internal/view/rail_list.go`: filter state (`filtering`, `filter`) + `matchesFilter` + ancestry-preserving `buildRows` filtering + auto-expand while filtering; the filter input line in `Draw`; the probe-gated selection marker.
- `internal/view/keys.go`: `/` enters filter input mode; while filtering the router yields keys to the rail's filter handler (so `n`/`r`/`a`/Esc/Enter are filter input, not mutation/release); a `RailFilter` gate mirrors the existing `ModalGate` pattern.
- `internal/view/app.go`: wires the rail's filter state to the router's `RailFilter` gate; rail title reflects the accepted query.
- `internal/view/rail_list_test.go`, `internal/view/keys_test.go`, `internal/view/app_test.go`: filter-narrowing + ancestry + Esc/Enter tests; marker-gated tests (absent without the env, present with it).
