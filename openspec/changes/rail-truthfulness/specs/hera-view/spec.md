## ADDED Requirements

### Requirement: Rail status icons reflect the bound task's actual argus state

The status icon on every rail row (coordinator headers, agent/worker rows, sub-coordinator rows, and freelance rows) SHALL be derived from the bound argus task's actual argus-reported state whenever that state is known to the argus state cache, REGARDLESS of the row's archive state (hera `archived_at` set, argus-archived) or its hera binding's liveness (live, ended, or dead). Archive state and binding liveness SHALL modulate only the row's STYLE (dimmed) and PLACEMENT (active list vs Archive expando) — never the status GLYPH: archiving a row, unarchiving it, or its binding ending MUST NOT change which glyph renders while the task's argus status is unchanged.

To keep the state resolvable across those transitions, the row's argus-state lookup MUST resolve through the role's most recent binding's task id when no live binding exists (archiving ends the binding with `end_reason='argus_archived'` while preserving the task id; reconciliation ends bindings with `end_reason='resync_missing'`), and the argus state cache MUST include archived tasks (argus's default task list excludes them).

The idle circle `○` SHALL render as a status glyph only when the task's argus state actually warrants it (in-progress idle); for an archived or dead row whose argus state is UNKNOWN (cache miss / task gone from argus), `○` dimmed remains the fallback.

#### Scenario: Archive round-trip never mutates the status glyph

- **WHEN** an agent row whose argus task is `complete` is archived via `a` and later unarchived via `a`, with the task's argus status unchanged throughout
- **THEN** the row's status icon MUST render `✓` at every step — dimmed while the row displays as archived — and MUST NOT fall to `○` at any point in the round trip

#### Scenario: Archived row with known state renders its true status dimmed

- **WHEN** the rail renders a row bucketed as archived (hera-archived, argus-archived, or dead) whose task state is known to the cache
- **THEN** the row MUST render the glyph for its actual argus status (`?` needs-input, `✓` complete/in_review, `☾` working, `○` idle-in-progress) in a dimmed style

#### Scenario: Ended binding does not lose the state lookup

- **WHEN** a role's only binding has ended (`end_reason='argus_archived'` or `'resync_missing'`) and the task's state is present in the argus state cache
- **THEN** the row's status icon MUST reflect that state exactly as if the binding were live

#### Scenario: Dead-classified row with known complete state shows the check

- **WHEN** a row's task is classified dead by the aliveness checker because its status is `complete`, while the state cache still holds that status
- **THEN** the row MUST render `✓` dimmed, not `○`

#### Scenario: Unknown-state archived row falls back to the dimmed circle

- **WHEN** an archived or dead row's task has no entry in the argus state cache (and the cache is warm)
- **THEN** the row renders `○` dimmed as the unknown-state fallback

## MODIFIED Requirements

### Requirement: Freelance (unmanaged argus) agents render in a Freelance rail section grouped by repo

A **freelancer** is a live argus task that hera does not currently manage. The system SHALL surface freelancers in the rail so the operator never has to leave hera to notice that an unmanaged agent needs attention — and so that EVERY non-archived argus task is reachable in the rail.

The system SHALL determine the freelancer set from argus's live task list (the argus state cache): every non-archived argus task is a freelancer UNLESS (a) at least one hera binding for it is LIVE (`ended_at` null) — it renders under its orchestrator — or (b) it already renders as a role row in the orchestrator tree (workers and sub-coordinators render via the latest-binding fallback even after their bindings end). A hera binding is hera's claim on a task: a live task whose bindings have ALL ENDED (a coordinator binding reconciled away by resync, an archive round-trip that ended the binding) and that no rendered role row carries MUST fall back to the Freelance section — hera's claim has lapsed, and a live argus task MUST NOT become unreachable through hera-side binding bookkeeping. (A task hera has never bound remains the common freelancer case; a coordinator task surfaced this way is ADDITIONALLY still reachable through its orchestrator header's coord-pane binding.) Freelancers SHALL be rendered in a "Freelance" section below all project (orchestrator) rows and above the Archive separator, introduced by a "Freelance" separator that is shown ONLY when at least one freelancer exists (so the operator never lands on an empty section).

Within the Freelance section, freelancers SHALL be grouped by argus project (repo) — "the same way Argus shows them" — under per-repo headers sorted by project name. Each repo header MUST render a collapse chevron (`▾` expanded / `▸` collapsed), the project name, and the count of its live freelance tasks, and MUST toggle expand/collapse when the operator presses Space while that header is selected. Repo groups default to expanded so freelancers are visible by default. Each freelance row MUST render its argus-reported state (status / idle / needs-input) via the same icon rules as managed rows, and its elapsed column MUST show argus's own age string.

Archived argus tasks MUST NOT appear in the Freelance section by default; they MUST appear only when the Archive view is revealed via `l`.

#### Scenario: Unmanaged argus tasks surface as freelancers grouped by repo

- **WHEN** argus reports live tasks whose ids no hera binding references
- **THEN** a "Freelance" section MUST appear below all project rows, with those tasks grouped under per-repo headers sorted by project name

#### Scenario: Tasks with a live hera binding are excluded from Freelance

- **WHEN** an argus task has at least one live hera binding
- **THEN** that task MUST NOT appear in the Freelance section (it renders under its orchestrator instead)

#### Scenario: A live task whose hera bindings have all ended falls back to Freelance

- **WHEN** a non-archived argus task's hera bindings have ALL ended (e.g. a coordinator role's binding ended by `resync_missing`) and no role row in the orchestrator tree renders that task id
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
