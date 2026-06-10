# Fix coord reattach for ⊘ mixed-coord headers (BUG-019)

## Why

Operators can't reattach to several coordinators. Hitting Enter on a `⊘` mixed-coord header (an orchestrator that DISPLAYS active hera-side while its argus coord task is ARCHIVED — created during bulk cleanup) shows the REATTACHING splash briefly, then focus bounces back to the rail. The coord never comes live.

Root cause: Enter on such a header falls through to normal pane-entry, which auto-reattaches the dead coord pane. But argus refuses to restart an ARCHIVED task, so the restart fails and the splash is cleared with a silent snap to the rail. The earlier mixed-coord design said "Enter keeps its current behavior — the repair-first `a` is the affordance," but in practice that current behavior is the silent bounce, leaving the operator with no in-pane way to revive the coord and no explanation.

## What Changes

- **Enter on a `⊘` mixed-coord header now repairs-then-reattaches:** Enter unarchives the coord's argus task (the same task-direct unarchive `a` runs) and THEN restarts it, so the coord pane reattaches the same way an agent pane does (splash → revive → live coord). This supersedes the prior "Enter keeps its current behavior" clause for the mixed-coord state; `a` remains the repair-only affordance.
- **No silent bounce:** when the repair or the restart fails (e.g. argus too old to support restart, or the session is held by a background agent that can't be double-attached), the failure is surfaced as an error modal instead of a silent snap to the rail. The operator always either sees the coord come live or learns why it can't.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the mixed-coord requirement — Enter on a `⊘` mixed-coord header now repairs (unarchives the coord task) then reattaches, and never silently bounces.

## Impact

- `internal/view/mutations.go` — `OnReattach` gains the mixed-coord orchestrator-header branch (unarchive then restart, error-on-failure).
- `internal/view/mutations_test.go` — coverage for the repair-then-reattach order, both failure modes, and the non-mixed / hera-archived no-op cases.
