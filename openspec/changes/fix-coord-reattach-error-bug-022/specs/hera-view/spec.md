## MODIFIED Requirements

### Requirement: Mixed-coord headers render a repair cue and `a` repairs first

The system SHALL detect the **mixed-coord state**: an orchestrator that DISPLAYS as active (hera `archived_at` NULL, header rendered in the active tree) while its coord-pane binding's argus task is ARCHIVED on the argus side (the argus state cache reports `archived` for the header's coord task). Only external argus-side archiving produces this state; it leaves the header's coordinator broken — the coord task is a tombstone argus hides — without any hera-side flag recording it.

The system SHALL render a DISTINCT visible cue on a mixed-coord header: the header's status icon MUST render `⊘` (U+2298, circled division slash) in the error style (red), replacing the normal status glyph, so the operator can SEE "this coord is broken/archived" at a glance. The cue MUST be distinct from the needs-input `?`, from the dimmed-archived treatment, and from the 󰹻 coordinator marker (which keeps rendering beside it). The cue applies ONLY to the mixed state: an orchestrator that is itself archived renders the normal dimmed-archived treatment regardless of its coord task's argus flag, and a header whose coord task is not argus-archived renders its normal status glyph.

The system SHALL make `a` REPAIR-FIRST on a mixed-coord header: pressing `a` (focus `RAIL`, mixed-coord header selected) MUST invoke argus's unarchive endpoint on the header's coord task — directly by task id, with no hera DB write — aligning argus reality to the displayed active orchestrator, and MUST NOT cascade-archive the orchestrator. An argus 404 (task pruned) is tolerated as a skip, per the existing archive-tolerance rule. Once the coord task is unarchived (or whenever the header is NOT in the mixed state), `a` MUST behave exactly as the standard orchestrator toggle (cascade-archive when displayed active, unarchive when displayed archived). The icon cue reverting to the normal status glyph on the post-repair rail refresh is the primary repair feedback.

The system SHALL make Enter on a mixed-coord header REPAIR-THEN-REATTACH (BUG-019): pressing Enter (focus `RAIL`, mixed-coord header selected) MUST FIRST invoke argus's unarchive endpoint on the header's coord task (the same task-direct unarchive `a` runs) and THEN restart that coord task via argus's restart endpoint, so the coord pane reattaches like an agent pane — REATTACHING splash, then the revived live coord. The unarchive MUST precede the restart, because argus refuses to restart an archived task. When the unarchive OR the restart fails (e.g. argus does not support restart, or the session is held by a background agent that cannot be double-attached), the system MUST surface a human-readable error and MUST NOT silently snap focus back to the rail with no explanation. This supersedes the prior "Enter keeps its current behavior (no new flow)" clause for the mixed-coord state; `a` remains the repair-only affordance (unarchive without restart).

The system SHALL treat a reattach that finds the session ALREADY LIVE as SUCCESS, not a terminal failure (BUG-022). When argus's restart endpoint reports a live session — `409 "task already running"` (the agent is live; no restart was needed) or the Start-race `500 "session already exists for task <id>"` (a concurrent restart already spawned the session) — the reattach MUST complete WITHOUT surfacing a red error modal, because a live session the proxy subscription can pick up is exactly the reattach goal. This applies on the `⊘` mixed-coord success path (archiving an argus task does NOT stop its PTY, so the coord's session is frequently still alive when the repair unarchives it) and whenever a manual reattach races the focus-driven auto-reattach (the loser hits the live session). The already-live tolerance MUST NOT mask genuine failures: worktree-missing (a 500 carrying the worktree-missing marker) MUST still route to the offer-delete recovery, restart-not-supported MUST still surface the update-argus message, and any other start-failure 500 MUST still surface as an error.

#### Scenario: Mixed-coord header renders the ⊘ repair cue

- **WHEN** an active orchestrator's coord-pane binding task is reported archived by the argus state cache
- **THEN** the header's status icon MUST render `⊘` in the error style instead of the coord task's status glyph, while the 󰹻 coordinator marker and the header's name render as usual

#### Scenario: Archived orchestrator does not render the cue

- **WHEN** an orchestrator is itself archived (hera `archived_at` set) and its coord task is also argus-archived
- **THEN** the header MUST render the normal dimmed-archived treatment, NOT the `⊘` cue (the state is not mixed — both sides agree)

#### Scenario: `a` on a mixed-coord header unarchives the coord task instead of cascade-archiving

- **WHEN** the operator presses `a` against a mixed-coord header whose coord task is `T1`
- **THEN** hera MUST issue the argus unarchive endpoint for `T1` directly (no hera DB write) AND MUST NOT archive the orchestrator or any of its roles

#### Scenario: Repaired header cascades as before

- **WHEN** the operator presses `a` against an active orchestrator header whose coord task is NOT argus-archived
- **THEN** hera MUST run the standard cascade-archive (orchestrator + roles), exactly as the `a`-toggle requirement specifies

#### Scenario: Enter on a mixed-coord header repairs then reattaches

- **WHEN** the operator presses Enter against a mixed-coord header whose coord task is `T1`
- **THEN** hera MUST FIRST issue the argus unarchive endpoint for `T1` and THEN issue the argus restart endpoint for `T1`, showing the REATTACHING splash on the coord pane, so the revived coord comes live

#### Scenario: Enter reattach surfaces failure instead of bouncing

- **WHEN** the operator presses Enter against a mixed-coord header and either the unarchive or the restart fails
- **THEN** hera MUST surface a human-readable error naming the coordinator AND MUST NOT silently return focus to the rail with no explanation; the restart MUST NOT run when the preceding unarchive failed

#### Scenario: Reattach to an already-live coord session shows no error

- **WHEN** the operator presses Enter against a `⊘` mixed-coord header and the restart endpoint reports `409 "task already running"` (the coord's PTY was never stopped by the archive)
- **THEN** hera MUST treat the reattach as successful — no red error modal — and let the coord pane come live from the existing session

#### Scenario: Reattach race loser does not modal

- **WHEN** a manual reattach and the focus-driven auto-reattach both restart the same task and the loser's restart returns the Start-race `500 "session already exists for task <id>"`
- **THEN** hera MUST treat the loser's result as success (the winner's session is live) and MUST NOT surface an error modal

#### Scenario: Genuine restart failure still surfaces

- **WHEN** a reattach restart returns a worktree-missing 500, a restart-not-supported response, or any other start-failure 500
- **THEN** hera MUST still route worktree-missing to the offer-delete recovery, surface the update-argus message for restart-not-supported, and surface an error for any other start-failure — the already-live tolerance MUST NOT swallow these
