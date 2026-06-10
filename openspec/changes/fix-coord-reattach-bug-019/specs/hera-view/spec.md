## MODIFIED Requirements

### Requirement: Mixed-coord headers render a repair cue and `a` repairs first

The system SHALL detect the **mixed-coord state**: an orchestrator that DISPLAYS as active (hera `archived_at` NULL, header rendered in the active tree) while its coord-pane binding's argus task is ARCHIVED on the argus side (the argus state cache reports `archived` for the header's coord task). Only external argus-side archiving produces this state; it leaves the header's coordinator broken — the coord task is a tombstone argus hides — without any hera-side flag recording it.

The system SHALL render a DISTINCT visible cue on a mixed-coord header: the header's status icon MUST render `⊘` (U+2298, circled division slash) in the error style (red), replacing the normal status glyph, so the operator can SEE "this coord is broken/archived" at a glance. The cue MUST be distinct from the needs-input `?`, from the dimmed-archived treatment, and from the 󰹻 coordinator marker (which keeps rendering beside it). The cue applies ONLY to the mixed state: an orchestrator that is itself archived renders the normal dimmed-archived treatment regardless of its coord task's argus flag, and a header whose coord task is not argus-archived renders its normal status glyph.

The system SHALL make `a` REPAIR-FIRST on a mixed-coord header: pressing `a` (focus `RAIL`, mixed-coord header selected) MUST invoke argus's unarchive endpoint on the header's coord task — directly by task id, with no hera DB write — aligning argus reality to the displayed active orchestrator, and MUST NOT cascade-archive the orchestrator. An argus 404 (task pruned) is tolerated as a skip, per the existing archive-tolerance rule. Once the coord task is unarchived (or whenever the header is NOT in the mixed state), `a` MUST behave exactly as the standard orchestrator toggle (cascade-archive when displayed active, unarchive when displayed archived). The icon cue reverting to the normal status glyph on the post-repair rail refresh is the primary repair feedback.

The system SHALL make Enter on a mixed-coord header REPAIR-THEN-REATTACH (BUG-019): pressing Enter (focus `RAIL`, mixed-coord header selected) MUST FIRST invoke argus's unarchive endpoint on the header's coord task (the same task-direct unarchive `a` runs) and THEN restart that coord task via argus's restart endpoint, so the coord pane reattaches like an agent pane — REATTACHING splash, then the revived live coord. The unarchive MUST precede the restart, because argus refuses to restart an archived task. When the unarchive OR the restart fails (e.g. argus does not support restart, or the session is held by a background agent that cannot be double-attached), the system MUST surface a human-readable error and MUST NOT silently snap focus back to the rail with no explanation. This supersedes the prior "Enter keeps its current behavior (no new flow)" clause for the mixed-coord state; `a` remains the repair-only affordance (unarchive without restart).

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
