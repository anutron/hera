## ADDED Requirements

### Requirement: A bucketed sub-coordinator keeps its child subtree reachable

When a sub-coordinator's parent-link row BUCKETS (its hera role is archived, its bound argus task is argus-archived, or the bound task RECORD no longer exists — `Dead`), the system SHALL STILL nest the child orchestrator's subtree beneath the parent-link row wherever that row renders (inside the coordinator's `Archive (N)` expando), nested one level deeper, so the child subtree MUST NOT drop out of the rail entirely.

A sub-coordinator is modeled as a multi-binding: the SAME argus task is both a worker role under a parent orchestrator (the parent-link row) AND the coord of a separate child orchestrator. When this multi-binding is resolved, the child orchestrator is removed from the top level and renders ONLY nested beneath the parent-link row — so its subtree's reachability depends ENTIRELY on the parent-link row continuing to nest it.

This is the transitive form of the invariant that archiving a row never makes it unreachable: bucketing a parent-link row MUST NOT make its child orchestrator's roles unreachable. Binding LIVENESS (live, ended, or dead) and archive state modulate only a row's STYLE (dimming) and its own PLACEMENT (active list vs Archive expando) — they never silently drop a consumed child orchestrator's subtree. The nested subtree is reachable by folding the owning `Archive (N)` expando open (and is force-expanded by `l` listall or an active filter), exactly like any other archived child.

#### Scenario: Sub-coordinator subtree survives a bucketed parent-link row

- **WHEN** a worker role under a parent orchestrator is the coordinator of a child orchestrator (a multi-binding, so the child is removed from the top level and nests beneath this parent-link row), AND the parent-link row buckets (the role is archived, the bound task is argus-archived, or its record is gone / `Dead`)
- **THEN** the child orchestrator's roles MUST remain reachable nested beneath the parent-link row inside the coordinator's `Archive (N)` expando (one level deeper than the parent-link row), and MUST NOT drop out of the rail entirely — neither at the top level (the child was consumed) nor missing from every row

#### Scenario: Disconnected worker whose record exists stays in the active tree

- **WHEN** a worker role's hera binding has ENDED (its session disconnected) but its most-recent binding's argus task RECORD still exists in the warm state cache and is not archived
- **THEN** the row MUST render among its coordinator's ACTIVE children (selectable and bindable, carrying the most-recent binding's task id), at most DIMMED by binding liveness, and MUST NOT be dropped from the rail or bucketed into Archive on account of the ended binding
