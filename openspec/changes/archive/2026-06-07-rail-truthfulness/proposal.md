# Rail truthfulness

## Why

Live QA (keyset acceptance R1/R2) surfaced two ways the rail lies about argus reality:

1. **Status icons mutate on archive toggles.** Archiving + unarchiving `archive-this-agent` flipped its icon `✓` → `○` even though the task's argus status stayed `complete` throughout (verified in the argus DB). Mechanism: `statusIcon` early-returns the idle circle `○` for ANY row whose effective archived flag is set (hera-archived, argus-archived, or dead), discarding the known true status. An icon that mutates on archive reads as "archiving killed/reset my agent". The same override also lies for `Dead`-marked rows (`IsTaskAlive` classifies `complete` tasks as dead, so live-binding complete tasks render `○` instead of `✓`).

2. **A live argus task whose hera bindings have ALL ENDED renders nowhere findable.** The operator's own session (`hera-1.0-ux-qa`, argus id 1780338447976760000, `in_review`, non-archived) had its coord binding reconciled away (`end_reason='resync_missing'`). Coordinator roles never render as their own rail row (the orchestrator header subsumes them anonymously), and the Freelance partition excludes any task referenced by ANY binding — live **or ended** (`AllArgusTaskIDs`). Result: no rail row anywhere carries the task, and the operator had to go back to argus to find their own live session. `archive-this-coord`'s task (binding ended `argus_archived`, task since unarchived) is in the same hole.

## What Changes

- **Icon truth:** the status glyph on every rail row is derived from the task's actual argus state whenever the state cache knows it, regardless of archive state or binding liveness. Archive/dead state modulates only STYLE (dimmed) and PLACEMENT (Archive expando), never the glyph. `○` remains only the true-idle status and the unknown-state archived fallback.
- **Reachability:** the Freelance partition's predicate changes from "no binding rows at all" to "no LIVE binding and not already rendered as a role row in the orchestrator tree". A live (non-archived) argus task whose hera bindings have all ended — and that no rendered role row carries via the latest-binding fallback — falls back to the Freelance section, named, grouped by repo. Every non-archived argus task is thereby reachable in the rail.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: status icons mirror real argus state across archive toggles and binding liveness (new requirement); the Freelance partition treats live-binding-less, unrendered tasks as freelancers (modified requirement).

## Impact

- `internal/view/rail_list.go`: `statusIcon` — glyph from true state when known; archived/dead dims the style instead of overriding the glyph.
- `internal/view/app.go`: `populateRail` collects the set of rendered role-row task ids; `buildFreelance` keys exclusion on live bindings (`Bindings.ListLive`) plus that rendered set instead of `AllArgusTaskIDs`.
- `internal/db/bindings.go`: `AllArgusTaskIDs` loses its only caller (removed).
- Tests: `rail_list_test.go` icon assertions for archived rows flip from `○` to true-status-dimmed; `app_test.go` freelance partition tests gain ended-binding fallback + no-duplication cases.
