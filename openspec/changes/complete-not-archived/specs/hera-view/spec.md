## ADDED Requirements

### Requirement: Task status never buckets a rail row

Task STATUS SHALL NOT affect rail bucketing. A row buckets into an Archive expando (or the top-level Archive section) ONLY when at least one of the following holds: the hera role is archived (`archived_at` set), the bound argus task is archived (argus `archived` flag), or the argus task RECORD no longer exists (404 / pruned — a state-cache miss once the cache is warm). The `Dead` classification SHALL mean record-nonexistence ONLY; a task in any terminal STATUS (`complete`, `failed`, `stopped`, …) whose argus record still exists is NOT dead.

A completed task whose argus record still exists MUST render in the ACTIVE tree — among its coordinator's active children, in its freelance repo group, or as a live coordinator header — with its status glyph (green `✓` for `complete`, per the status-glyph table), selectable and bindable like any active row.

A coordinator binding whose task is completed-but-existing MUST still feed the orchestrator header's coord task binding (`CoordTaskID`) and status (`CoordStatus`), so the header renders the `✓` glyph and its pane shows the coordinator's last output; its children's bucketing is unaffected. Only an archived or record-gone coord binding is skipped as a tombstone.

Deadness MUST be derived from the argus state cache snapshot (the same source that drives row icons), not from per-row status-classifying calls: the rail rebuild path MUST NOT issue a synchronous argus HTTP request per row. A cold cache MUST NOT transiently classify rows dead. Status-based aliveness MAY still inform initial pane selection (preferring a live task at session start), but never bucketing.

#### Scenario: Stepping a task to complete keeps it in the active children

- **WHEN** a worker row's bound argus task transitions to status `complete` while its argus record still exists with `archived=0` and the hera role's `archived_at` is NULL
- **THEN** the row MUST remain among its coordinator's active children rendering the green `✓` glyph, MUST NOT move into the coordinator's `Archive (N)` expando, and MUST NOT be classified `Dead`

#### Scenario: Completed coordinator header stays bound and checked

- **WHEN** an orchestrator's coord binding points at an argus task with status `complete` whose record still exists and is not archived
- **THEN** the orchestrator header MUST carry that task as its coord binding (`CoordTaskID` set, `CoordStatus` complete, rendering `✓`) AND the header and its children MUST NOT be hidden or re-bucketed on account of the status

#### Scenario: Completed freelancer stays in its repo group

- **WHEN** an unmanaged argus task in the Freelance section has status `complete` and is not archived
- **THEN** it MUST remain visible in its repo group (counted in the group's `(N)`) rendering `✓`, without `l`

#### Scenario: Record-gone task still buckets as dead

- **WHEN** a role's bound argus task id is absent from the warm argus state cache (the record was pruned / returns 404)
- **THEN** the row MUST be classified `Dead` and bucket into its coordinator's Archive expando (hidden in the default view, dimmed under `l`)

#### Scenario: Rail rebuild issues no per-row argus HTTP

- **WHEN** the rail repopulates (any rebuild trigger) with N live-bound roles
- **THEN** deadness MUST be read from the state-cache snapshot; the rebuild MUST NOT perform a per-row `GET /api/tasks/{id}` aliveness probe on the event loop
