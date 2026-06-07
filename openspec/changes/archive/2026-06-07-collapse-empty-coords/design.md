## Context

`internal/view/rail_list.go` tracks coordinator folds in `collapsed map[int64]bool` keyed by orchestrator ID, with the map's ZERO VALUE meaning expanded. The new rule needs a per-coordinator default that depends on data (non-archived child count) while explicit operator toggles keep winning — so "never touched" must become distinguishable from "explicitly expanded".

## Goals / Non-Goals

- Goals: empty coordinators (zero non-archived children) fold away by default; operator toggles override and persist for the session; `l` keeps force-revealing archived content.
- Non-Goals: persistence across daemon restarts; changing Freelance or top-level Archive fold defaults; changing the active/archived partition (`roleArchived`).

## Decisions

- **Presence-checked map over tri-state enum.** Keep `collapsed map[int64]bool` but change its semantics: an entry present = the operator's explicit choice (true=collapsed, false=expanded); absent = use the default. A new `orchCollapsed(o *orchEntry) bool` helper resolves the effective state:

  1. explicit entry → that value;
  2. `showArchived` → false (force-expand the default so `l` reveals archived children);
  3. else → `visibleRoleCount(o) == 0` (the existing non-archived counter; hera-archived, argus-archived, and dead all count archived via `roleArchived`).

  This is the smallest delta: no schema change, no new field, and `restoreCursor`/selection code is untouched.

- **Toggle flips the EFFECTIVE state, not the raw map slot.** `ToggleCollapse` writes `collapsed[id] = !orchCollapsed(o)` (needs the `*orchEntry`, available at every toggle site: header row, sub-coord row via `childOrch`, worker row via parent lookup). The old `collapsed[id] = !collapsed[id]` would mis-toggle a default-collapsed empty coord (zero-value false → "collapse" an already-collapsed row).

- **All fold reads route through the helper**: `buildRows`' `appendOrch`, `appendOrchChildren`'s sub-coord recursion, and the chevron picks in `drawOrchRow`/`drawSubCoordRow`. No call site reads `collapsed[id]` directly anymore.

## Risks / Trade-offs

- A coordinator whose last active child gets archived silently folds up on the next rebuild (default flips). Accepted: that is exactly the operator-ruled behavior ("finished orchestrators fold away"), and one `space` pins it open.
- An explicit collapse now survives `l` (today it also does — appendOrch checks collapsed before children), so no behavior change there; only the DEFAULT branch consults showArchived.
