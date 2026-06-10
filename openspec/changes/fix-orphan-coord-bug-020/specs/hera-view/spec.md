## MODIFIED Requirements

### Requirement: `^d` deletes a role or cascade-deletes an orchestrator

The system SHALL, on `^d` confirmation against a role, end the role's live binding (if any) with `end_reason="user_deleted"`, set the role's `archived_at` to the current timestamp, and invoke `git worktree remove --force <worktree_path>` against the binding's worktree path. The role row MUST persist (archived). On `^d` confirmation against an orchestrator, the same operations MUST cascade to every role under the orchestrator, and the orchestrator row's `archived_at` MUST also be set. If a worktree path is empty or the directory does not exist, the `git worktree remove` step MUST be a soft no-op (logged and skipped, not an error).

The system SHALL ALSO make the argus-side task destruction resilient to a missing worktree (BUG-020): when the operator deletes a coordinator (or role) whose argus task worktree was removed out-of-band, argus's delete can fail with `worktree path missing`. Hera MUST treat THAT specific condition as a soft skip (logged) — mirroring the local `git worktree remove` guard — and continue, so the orchestrator + roles + bindings are removed from hera's DB regardless of worktree state and the orphan clears from the rail. Any OTHER argus delete failure MUST still abort the operation (hera MUST NOT silently orphan DB rows on a transient or unexpected error).

#### Scenario: Delete role ends binding and removes worktree

- **WHEN** the operator confirms the `^d` modal against role `foo/worker-1` whose live binding has worktree path `/Users/x/.argus/worktrees/foo/worker-1`
- **THEN** hera MUST update the binding's `ended_at` and `end_reason="user_deleted"`, set the role's `archived_at` to the current timestamp, AND execute `git worktree remove --force /Users/x/.argus/worktrees/foo/worker-1`

#### Scenario: Delete orchestrator cascades to all roles

- **WHEN** the operator confirms the `^d` modal against orchestrator `foo` which has roles `coord`, `w1`, `w2`
- **THEN** hera MUST end every live binding under `foo`, set `archived_at` on `foo`, `coord`, `w1`, and `w2`, AND invoke `git worktree remove --force` against each role's binding's worktree path

#### Scenario: Worktree missing is soft no-op

- **WHEN** the operator confirms `^d` against a role whose binding's `worktree_path` is empty OR the directory does not exist on disk
- **THEN** hera MUST skip the `git worktree remove` step, log the skip, AND still mark the role archived AND end the binding

#### Scenario: Orphaned coordinator deletes despite argus worktree-missing

- **WHEN** the operator confirms `^d` against an orchestrator whose coord task worktree was deleted out-of-band, so argus's delete of that task fails with `worktree path missing`
- **THEN** hera MUST treat the argus delete as a soft skip (logged), remove the orchestrator + its roles + their bindings from hera's DB, and clear the orphan from the rail

#### Scenario: A non-worktree-missing argus delete failure aborts

- **WHEN** the operator confirms `^d` against an orchestrator and argus's delete of a bound task fails with an error that is NOT the worktree-missing condition
- **THEN** hera MUST abort the operation and MUST NOT delete the orchestrator row, so the operator can retry rather than silently losing DB rows

### Requirement: Mixed-coord headers render a repair cue and `a` repairs first

The system SHALL detect the **mixed-coord state**: an orchestrator that DISPLAYS as active (hera `archived_at` NULL, header rendered in the active tree) while its coord-pane binding's argus task is ARCHIVED on the argus side (the argus state cache reports `archived` for the header's coord task). Only external argus-side archiving produces this state; it leaves the header's coordinator broken — the coord task is a tombstone argus hides — without any hera-side flag recording it.

The system SHALL render a DISTINCT visible cue on a mixed-coord header: the header's status icon MUST render `⊘` (U+2298, circled division slash) in the error style (red), replacing the normal status glyph, so the operator can SEE "this coord is broken/archived" at a glance. The cue MUST be distinct from the needs-input `?`, from the dimmed-archived treatment, and from the 󰹻 coordinator marker (which keeps rendering beside it). The cue applies ONLY to the mixed state: an orchestrator that is itself archived renders the normal dimmed-archived treatment regardless of its coord task's argus flag, and a header whose coord task is not argus-archived renders its normal status glyph.

The system SHALL make `a` REPAIR-FIRST on a mixed-coord header: pressing `a` (focus `RAIL`, mixed-coord header selected) MUST invoke argus's unarchive endpoint on the header's coord task — directly by task id, with no hera DB write — aligning argus reality to the displayed active orchestrator, and MUST NOT cascade-archive the orchestrator. An argus 404 (task pruned) is tolerated as a skip, per the existing archive-tolerance rule. Once the coord task is unarchived (or whenever the header is NOT in the mixed state), `a` MUST behave exactly as the standard orchestrator toggle (cascade-archive when displayed active, unarchive when displayed archived). The icon cue reverting to the normal status glyph on the post-repair rail refresh is the primary repair feedback.

The system SHALL make Enter on a mixed-coord header REPAIR-THEN-REATTACH (BUG-019): pressing Enter (focus `RAIL`, mixed-coord header selected) MUST FIRST invoke argus's unarchive endpoint on the header's coord task (the same task-direct unarchive `a` runs) and THEN restart that coord task via argus's restart endpoint, so the coord pane reattaches like an agent pane — REATTACHING splash, then the revived live coord. The unarchive MUST precede the restart, because argus refuses to restart an archived task. When the unarchive OR the restart fails (e.g. argus does not support restart, or the session is held by a background agent that cannot be double-attached), the system MUST surface a human-readable error and MUST NOT silently snap focus back to the rail with no explanation. This supersedes the prior "Enter keeps its current behavior (no new flow)" clause for the mixed-coord state; `a` remains the repair-only affordance (unarchive without restart).

The system SHALL recognize the UNRECOVERABLE-ORPHAN sub-case of that restart failure (BUG-020): when the restart fails specifically because the coord task's worktree is gone (argus reports `worktree path missing`), the coord can never be revived by reattach. Hera MUST NOT surface the raw argus 500 — instead it MUST offer to DELETE the orphan via a confirmation ("This coordinator's worktree is gone and can't be revived. Delete it? (y/N)"), and on confirm MUST route to the orchestrator-delete path (`DeleteOrchestrator`), clearing the orphan from the rail. The SAME recovery MUST apply when Enter reattaches a dead-session worker/freelance row whose worktree is gone: hera offers to delete that orphaned role (routing to the role-delete path). On decline, no deletion occurs. The delete MUST run off the event loop and MUST refresh the rail on success.

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

- **WHEN** the operator presses Enter against a mixed-coord header and either the unarchive or the restart fails for a reason OTHER than a missing worktree
- **THEN** hera MUST surface a human-readable error naming the coordinator AND MUST NOT silently return focus to the rail with no explanation; the restart MUST NOT run when the preceding unarchive failed

#### Scenario: Enter on an orphaned coord whose worktree is gone offers delete

- **WHEN** the operator presses Enter against a mixed-coord header whose coord task `T1` cannot be restarted because argus reports `worktree path missing`
- **THEN** hera MUST NOT show the raw argus 500; it MUST open a confirmation offering to delete the orphaned coordinator, and on confirm MUST call `DeleteOrchestrator` for that orchestrator and refresh the rail

#### Scenario: Enter on an orphaned worker whose worktree is gone offers delete

- **WHEN** the operator presses Enter against a dead-session worker row whose argus task cannot be restarted because argus reports `worktree path missing`
- **THEN** hera MUST offer to delete the orphaned role, and on confirm MUST route to the role-delete path rather than surfacing the raw argus error
