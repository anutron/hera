# Live coordinator must never render ✓/complete

## Why

Argus auto-completes a coordinator's underlying argus task when the session goes
idle (the agent loop is waiting for the human operator). Hera's rail and Details
pane inherit that argus task status directly, causing a live coordinator
(e.g. `hera-1.0-release`) to render `✓ complete` even though it is still
actively coordinating work. This contradicts operator expectations: a coordinator
is a long-lived session, not a one-shot task; "complete" implies it is done and
safe to prune.

Additionally, `^r` (prune completed) calls `ops.ListCompletedAgents`, which
reads every live binding's argus task status. If argus marks the coordinator
task complete, the coordinator role would appear in the prune list — a live
coordinator must never be prunable from the `^r` flow.

## What Changes

- **Display:** a live (non-archived, non-mixed-coord) coordinator whose argus
  task status is `complete` MUST render as idle (☾) in both the rail glyph and
  the Details pane "Status:" field. The underlying `CoordStatus` field is kept
  accurate (still `complete`) so pane bindings and the coord pane's task target
  are unaffected; only the display is adjusted.

- **Prune-guard:** `ops.ListCompletedAgents` MUST skip coordinator roles so a
  live coordinator can never surface in the `^r` prune confirmation list,
  regardless of argus task status.

- **Archived coords:** this change applies only to LIVE (non-archived) coords.
  Archived coordinators may render `✓` (their session is genuinely finished).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: new requirement — live coordinators never render ✓/complete in
  the rail or Details pane; coordinator roles excluded from `^r` prune targets.
