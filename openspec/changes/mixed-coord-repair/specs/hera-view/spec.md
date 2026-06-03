## ADDED Requirements

### Requirement: Mixed-coord headers render a repair cue and `a` repairs first

The system SHALL detect the **mixed-coord state**: an orchestrator that DISPLAYS as active (hera `archived_at` NULL, header rendered in the active tree) while its coord-pane binding's argus task is ARCHIVED on the argus side (the argus state cache reports `archived` for the header's coord task). Only external argus-side archiving produces this state; it leaves the header's coordinator broken — the coord task is a tombstone argus hides — without any hera-side flag recording it.

The system SHALL render a DISTINCT visible cue on a mixed-coord header: the header's status icon MUST render `⊘` (U+2298, circled division slash) in the error style (red), replacing the normal status glyph, so the operator can SEE "this coord is broken/archived" at a glance. The cue MUST be distinct from the needs-input `?`, from the dimmed-archived treatment, and from the 󰹻 coordinator marker (which keeps rendering beside it). The cue applies ONLY to the mixed state: an orchestrator that is itself archived renders the normal dimmed-archived treatment regardless of its coord task's argus flag, and a header whose coord task is not argus-archived renders its normal status glyph.

The system SHALL make `a` REPAIR-FIRST on a mixed-coord header: pressing `a` (focus `RAIL`, mixed-coord header selected) MUST invoke argus's unarchive endpoint on the header's coord task — directly by task id, with no hera DB write — aligning argus reality to the displayed active orchestrator, and MUST NOT cascade-archive the orchestrator. An argus 404 (task pruned) is tolerated as a skip, per the existing archive-tolerance rule. Once the coord task is unarchived (or whenever the header is NOT in the mixed state), `a` MUST behave exactly as the standard orchestrator toggle (cascade-archive when displayed active, unarchive when displayed archived). The icon cue reverting to the normal status glyph on the post-repair rail refresh is the primary repair feedback. Enter on a mixed-coord header keeps its current behavior (no new flow) — the repair-first `a` is the affordance.

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

## MODIFIED Requirements

### Requirement: `a` toggles archived state on an orchestrator or role

The system SHALL, on `a` against a non-archived role, set the role's `archived_at` to the current timestamp AND invoke argus's archive endpoint (`POST /api/tasks/{id}/archive`) on the binding's `argus_task_id`. The worktree MUST be preserved. On `a` against a non-archived orchestrator, the same MUST cascade to every role under that orchestrator — EXCEPT when the header is in the mixed-coord state (displayed-active orchestrator whose coord task is argus-archived), where `a` MUST instead repair by unarchiving the coord's argus task, per the mixed-coord repair requirement. The toggle is SYMMETRIC: on `a` against an already-archived role, `archived_at` MUST be cleared AND hera MUST invoke argus's unarchive endpoint on the role's bound argus task — the rail buckets a row into the Archive expando when EITHER side is archived, so clearing only the hera side would produce no visible change.

The toggle DIRECTION MUST follow the row's EFFECTIVE rendered archived state — the same predicate the rail uses to bucket the row into an Archive expando (hera `archived_at` set, OR argus-archived, OR dead) — never a single backing flag alone: `a` MUST unarchive any row that DISPLAYS as archived and archive any row that displays as active. The view layer SHALL compute the effective state from the selected row and dispatch an EXPLICIT archive or unarchive verb (not a flag-inspecting toggle), so on a mixed-flag row (e.g. hera-active + argus-archived — a state historical asymmetric toggles could produce) `a` clears BOTH sides instead of re-archiving the side that was already clear. Unarchive MUST clear both the hera and argus sides; archive MUST set both. In BOTH directions the bound task MUST be resolved from the role's live binding when one exists, FALLING BACK to the role's most recent binding (by `started_at`) regardless of `ended_at` — archiving a task ENDS its binding (`end_reason='argus_archived'`) while preserving its `argus_task_id`, so for exactly the rows that need unarchiving no live binding exists and a live-only lookup would silently skip the argus side, defeating the symmetric toggle; likewise, archiving a role whose binding was ended by a previous archive (the hera-active + argus-active row that a partial unarchive or historical asymmetric toggle leaves behind) MUST still archive its bound argus task via the same fallback — a live-only lookup there would stamp hera's `archived_at` while silently leaving the argus task active, recreating the mixed state. The ended-binding fallback MUST carry a **shared-task guard** in the ARCHIVE direction only: when the task id was resolved via an ENDED binding AND any OTHER role holds a LIVE binding (`ended_at` NULL) to that same argus task, the argus-side archive MUST be skipped — the live binding is that other role's ownership claim, and archiving through the stale ended binding would yank a task out from under an ACTIVE role or orchestrator (the cascade-collateral hazard: archiving an old orchestrator archived the operator's live coord's task). The hera-side role archive still proceeds, and the skip MUST be logged at info naming both roles (the role being archived and the live-bound role). A task resolved via the role's OWN live binding archives unconditionally — that live binding IS the ownership — and the unarchive direction stays permissive (no guard). Only when the role has no binding at all (or the resolved binding carries no argus task id) is the argus step skipped entirely (nothing to archive or unarchive); the hera-side write still proceeds. On `a` against an already-archived orchestrator, the orchestrator's `archived_at` MUST be cleared AND hera MUST likewise invoke argus's unarchive endpoint on the coord role's live binding's argus task; unarchiving an orchestrator MUST NOT cascade to its worker roles (workers unarchive individually). On `a` against a freelance row (an unmanaged argus task with no hera role or binding), hera MUST address the argus task directly — issuing `POST /api/tasks/{id}/archive` (or the unarchive endpoint when the task is already archived per argus state) against the row's argus task id — with no hera DB write (there is no role row to stamp).

#### Scenario: Archive role calls argus and preserves worktree

- **WHEN** the operator presses `a` against non-archived role `foo/w1` bound to argus task `T1`
- **THEN** hera MUST set the role's `archived_at` to the current timestamp, issue `POST /api/tasks/T1/archive` to argus, AND MUST NOT touch the worktree directory

#### Scenario: Unarchive role unarchives the argus task too

- **WHEN** the operator presses `a` against archived role `foo/w1` whose live binding's argus task `T1` is archived on the argus side
- **THEN** hera MUST clear the role's `archived_at` AND issue the argus unarchive endpoint for `T1`, so the row visibly returns to the coordinator's active children

#### Scenario: Mixed-flag role (hera-active + argus-archived) unarchives both sides

- **WHEN** the operator presses `a` against a role row that DISPLAYS as archived (it sits in an Archive expando) because its bound argus task is archived, while its hera `archived_at` is NULL
- **THEN** hera MUST treat the press as UNARCHIVE — leaving `archived_at` clear (no fresh archive timestamp may be written) AND issuing the argus unarchive endpoint for the bound task — so the row visibly leaves the Archive expando rather than being re-archived

#### Scenario: Dead row treated as displayed-archived

- **WHEN** the operator presses `a` against a role row that displays as archived only because its binding is dead (the argus task is gone)
- **THEN** hera MUST treat the press as UNARCHIVE (clear `archived_at`, attempt the argus unarchive), never as a fresh archive

#### Scenario: Archive role with an ended binding falls back to the latest binding

- **WHEN** the operator presses `a` against a role that DISPLAYS as active whose only binding was ended by a previous archive (`end_reason='argus_archived'`) and records argus task `T1`, and no other role holds a live binding to `T1`
- **THEN** hera MUST set the role's `archived_at` AND issue `POST /api/tasks/T1/archive` resolved via the most recent binding — the ended binding MUST NOT cause the argus side to be silently skipped, leaving the argus task active under a hera-archived role

#### Scenario: Archive via an ended binding skips argus when the task is live-bound to another role

- **WHEN** role `old/w1` is archived (directly or via an orchestrator cascade) and its task id `T1` resolves via an ENDED binding, while role `new/coord` holds a LIVE binding to `T1`
- **THEN** hera MUST set `old/w1`'s `archived_at`, MUST NOT issue the argus archive endpoint for `T1`, AND MUST log the skip at info naming both `old/w1` and `new/coord`

#### Scenario: Archive via the role's own live binding archives even when the task is shared

- **WHEN** the operator presses `a` against an active role whose LIVE binding records argus task `T1`, while another role also holds a live binding to `T1` (multi-binding)
- **THEN** hera MUST issue `POST /api/tasks/T1/archive` as today — the role's own live binding is its ownership claim and the shared-task guard MUST NOT apply

#### Scenario: Orchestrator cascade does not archive a sibling orchestrator's live task

- **WHEN** the operator cascade-archives an OLD orchestrator containing a role whose ended binding records task `T1`, and `T1` is live-bound under a DIFFERENT active orchestrator
- **THEN** the cascade MUST archive the old orchestrator and its roles hera-side, MUST NOT issue the argus archive endpoint for `T1`, and MUST succeed (the skip is not a failure)

#### Scenario: Unarchive role with an ended binding falls back to the latest binding

- **WHEN** the operator presses `a` against an archived role whose only binding was ended by the archive (`end_reason='argus_archived'`) and records argus task `T1`
- **THEN** hera MUST clear the role's `archived_at` AND issue the argus unarchive endpoint for `T1` resolved via the most recent binding — the ended binding MUST NOT cause the argus side to be silently skipped

#### Scenario: Unarchive role with no binding at all skips argus

- **WHEN** the operator presses `a` against an archived role that has never had a binding
- **THEN** hera MUST clear the role's `archived_at` AND MUST NOT call argus

#### Scenario: Archive orchestrator cascades to roles

- **WHEN** the operator presses `a` against non-archived orchestrator `foo` with roles `coord`, `w1`, `w2`, and `foo` is NOT in the mixed-coord state
- **THEN** hera MUST set `archived_at` on `foo`, `coord`, `w1`, and `w2` AND issue an archive call to argus for each role's resolved binding's argus_task_id (live preferred, latest fallback — the cascade calls the role-level archive and inherits its resolution)

#### Scenario: Mixed-coord header repairs instead of cascading

- **WHEN** the operator presses `a` against a displayed-active orchestrator header whose coord task is argus-archived
- **THEN** hera MUST issue the argus unarchive endpoint for the coord task directly (no hera DB write, no cascade), per the mixed-coord repair requirement

#### Scenario: Unarchive orchestrator unarchives its coord task but does not cascade to workers

- **WHEN** the operator presses `a` against an archived orchestrator `foo` whose roles `coord`, `w1` are also archived
- **THEN** hera MUST clear `archived_at` on `foo`, issue the argus unarchive endpoint for the coord role's live binding's argus task, AND MUST leave `archived_at` on `coord` and `w1` set

#### Scenario: Archive freelancer addresses the argus task directly

- **WHEN** the operator presses `a` against a non-archived freelance row whose argus task is `T9`
- **THEN** hera MUST issue `POST /api/tasks/T9/archive` to argus AND MUST NOT write any hera DB row

#### Scenario: Unarchive freelancer addresses the argus task directly

- **WHEN** the operator presses `a` against a freelance row whose argus task `T9` is archived per argus state
- **THEN** hera MUST issue the argus unarchive endpoint for `T9` AND MUST NOT write any hera DB row

### Requirement: Freelance (unmanaged argus) agents render in a Freelance rail section grouped by repo

A **freelancer** is a live argus task that hera does not currently manage. The system SHALL surface freelancers in the rail so the operator never has to leave hera to notice that an unmanaged agent needs attention — and so that EVERY non-archived argus task is reachable in the rail.

The system SHALL determine the freelancer set from argus's live task list (the argus state cache): every non-archived argus task is a freelancer UNLESS (a) at least one hera binding for it is LIVE (`ended_at` null) — it renders under its orchestrator — or (b) it already renders as a role row in the orchestrator tree (workers and sub-coordinators render via the latest-binding fallback even after their bindings end) — or (c) it is the coord-pane binding (`CoordTaskID`) of an orchestrator header rendered in the current rail: the header makes the task reachable (selecting the header binds its pane), so a freelance row would be a duplicate. A hera binding is hera's claim on a task: a live task whose bindings have ALL ENDED and that NO rendered row or header carries MUST still fall back to the Freelance section — hera's claim has lapsed, and a live argus task MUST NOT become unreachable through hera-side binding bookkeeping. Truly orphaned shapes that keep the fallback include a coord task whose hera coord role is archived (the rendered header then binds no coord task) and a coord task whose orchestrator is not rendered at all. Freelancers SHALL be rendered in a "Freelance" section below all project (orchestrator) rows and above the Archive separator, introduced by a "Freelance" separator that is shown ONLY when at least one freelancer exists (so the operator never lands on an empty section).

Within the Freelance section, freelancers SHALL be grouped by argus project (repo) — "the same way Argus shows them" — under per-repo headers sorted by project name. Each repo header MUST render a collapse chevron (`▾` expanded / `▸` collapsed), the project name, and the count of its live freelance tasks, and MUST toggle expand/collapse when the operator presses Space while that header is selected. Repo groups default to expanded so freelancers are visible by default. Each freelance row MUST render its argus-reported state (status / idle / needs-input) via the same icon rules as managed rows, and its elapsed column MUST show argus's own age string.

Archived argus tasks MUST NOT appear in the Freelance section by default; they MUST appear only when the Archive view is revealed via `l`.

#### Scenario: Unmanaged argus tasks surface as freelancers grouped by repo

- **WHEN** argus reports live tasks whose ids no hera binding references
- **THEN** a "Freelance" section MUST appear below all project rows, with those tasks grouped under per-repo headers sorted by project name

#### Scenario: Tasks with a live hera binding are excluded from Freelance

- **WHEN** an argus task has at least one live hera binding
- **THEN** that task MUST NOT appear in the Freelance section (it renders under its orchestrator instead)

#### Scenario: Tasks reachable via a rendered orchestrator header do not duplicate into Freelance

- **WHEN** an argus task is the coord-pane binding (`CoordTaskID`) of an orchestrator header rendered in the current rail — even though all its hera bindings have ended
- **THEN** that task MUST NOT appear in the Freelance section (the header preserves findability; selecting it binds the coord pane to the task)

#### Scenario: A truly orphaned task still falls back to Freelance

- **WHEN** a non-archived argus task's hera bindings have ALL ended and no rendered row or header carries it (e.g. its hera coord role is archived, so the rendered header binds no coord task)
- **THEN** the task MUST appear as a named row in the Freelance section under its repo group, selectable like any freelancer

#### Scenario: Role rows rendered via the latest-binding fallback do not duplicate into Freelance

- **WHEN** a worker role's only binding has ended but the role still renders as a row in the orchestrator tree carrying that task id via the latest-binding fallback
- **THEN** that task MUST NOT additionally appear in the Freelance section

#### Scenario: Space toggles a Freelance repo group

- **WHEN** focus is `RAIL`, a Freelance repo header is selected, and the operator presses Space
- **THEN** that repo group MUST expand (or collapse if already expanded), revealing (or hiding) its freelance rows, independently of other repo groups

#### Scenario: No freelancers hides the section

- **WHEN** every live argus task is excluded from the freelancer set
- **THEN** the "Freelance" separator and all repo headers MUST NOT be rendered

#### Scenario: Archived freelancers appear only in Archive

- **WHEN** an argus task in the freelancer set is archived
- **THEN** it MUST NOT appear in the live Freelance section AND MUST appear only when the Archive view is revealed via `l`
