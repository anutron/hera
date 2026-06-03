# Design

## Bug 1 — where the icon actually lies

The suspected state-cache miss does NOT exist: `populateRail` resolves an ended binding's task id via the `ListByRole` latest-binding fallback, and the `ArgusStateCache` polls `ListTasksAll` (includes archived tasks), so `applyArgusState` finds archived tasks' state fine. The lie is downstream: `statusIcon(archived, ...)` early-returns `'○'` whenever the row's effective archived flag (`Archived || Dead || ArgusArchived`) is set, throwing away the resolved state. Fix at that single chokepoint — shared by worker rows, sub-coord rows, coordinator headers, and freelance rows — so all four row kinds inherit the truth at once:

- state known → glyph from status (`?`/`✓`/`☾`/`○`), archived/dead → render that glyph in `theme.StyleDimmed`;
- state unknown + archived/dead → `'○'` dimmed (unchanged fallback);
- state unknown + active → existing binding-presence fallbacks (unchanged).

`Dead` rows benefit automatically: `IsTaskAlive` classifies `complete` as dead, so live-binding complete tasks (sketch-release fixtures) currently render `○`; with the fix they render `✓` dimmed. The active/archived PARTITION (`roleArchived`) is deliberately untouched — placement, expandos, collapse defaults, and counts keep their semantics; only the glyph stops lying.

## Bug 2 — which fallback is correct

Two candidate shapes were considered for "every non-archived argus task must be reachable":

1. **Header-binding counts as reachable.** The orchestrator header already binds its coord pane to the coord role's latest binding's task (live or ended), so `hera-1.0-ux-qa` is technically enterable today (verified by live probe). Rejected as the sole answer: the header carries the ORCHESTRATOR's name; nothing in the rail carries the task's name, and the operator demonstrably could not find their session. Reachable-but-anonymous failed QA.

2. **Freelance fallback on lapsed claims (chosen).** A hera binding is hera's claim on an argus task; when every claim has ENDED, hera no longer manages the task, and the argus-mirror principle demands it surface like any other unmanaged live task — named, in the Freelance section. Exclusion from Freelance therefore keys on (a) a LIVE binding existing, or (b) the task already rendering as a role ROW in the tree (workers/sub-coords render via the latest-binding fallback even after their binding ends — without (b) every finished agent would duplicate into Freelance).

The orchestrator header KEEPS its latest-binding coord-pane fallback (shape 1) — it is correct coordinator-context behavior — so a lapsed coordinator task is reachable both via its orchestrator header and, named, via Freelance. With today's live data exactly two tasks gain Freelance rows: `hera-1.0-ux-qa` and `archive-this-coord` — precisely the two live sessions QA could not find.

## Mechanics

`populateRail` already walks every role; it now accumulates `rendered` — the set of argus task ids carried by role rows appended to the tree (coordinator roles are NOT in the set: they render as anonymous headers, which is exactly the findability gap). `buildFreelance` takes that set, swaps `AllArgusTaskIDs` for a live-binding set built from the existing `Bindings.ListLive`, and skips a task only when it is live-bound, rendered, or archived-and-hidden. Query-failure behavior is preserved: on error, fail safe to "everything looks managed" (no Freelance section) rather than mislabeling managed tasks.

`AllArgusTaskIDs` loses its only caller and is removed with its tests.
