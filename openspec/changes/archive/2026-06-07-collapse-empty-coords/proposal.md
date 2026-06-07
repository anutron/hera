## Why

Every coordinator in the hera-view rail renders EXPANDED (`▾`) by default, so dead or finished orchestrators with zero live agents each burn 2+ rows (header + `Archive (N)` expando) and the rail is mostly noise. The operator ruled (2026-06-02, with a target mock) that empty coordinators should fold away by default.

## What Changes

- A coordinator (root or nested sub-coordinator) with **zero non-archived child agents** renders **collapsed (`▸`) by default** — its children (including its `Archive (N)` expando) hidden until expanded.
- A coordinator with ≥1 non-archived child agent keeps the current default (expanded).
- Manual toggling still wins and persists for the session: the `collapsed` map becomes presence-checked (explicit toggle) instead of zero-value-defaulted, so the rule only sets the DEFAULT for coordinators the operator has not touched.
- `l`/showArchived force-reveals as today: while active it overrides the collapsed DEFAULT (so archived children are reachable), but an explicit manual collapse still wins.
- The Freelance section and the top-level Archive are unaffected.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the rail's coordinator fold default changes — a coordinator with zero non-archived children defaults collapsed; manual toggles override and persist; `l` overrides the collapsed default while active.

## Impact

- `internal/view/rail_list.go`: `buildRows`/`appendOrchChildren` fold checks, chevron rendering in `drawOrchRow`/`drawSubCoordRow`, and `ToggleCollapse` flip logic move from raw `collapsed[id]` reads to an effective-collapse helper (explicit entry wins, else default by non-archived child count and showArchived).
- `internal/view/rail_list_test.go`: new scenarios; existing tests asserting `▾` on header-only coordinators may need updating.
