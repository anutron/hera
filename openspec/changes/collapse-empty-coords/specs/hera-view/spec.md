## ADDED Requirements

### Requirement: Coordinators with zero non-archived children default collapsed

The rail SHALL derive each coordinator's DEFAULT fold state from its non-archived child count: a coordinator (orchestrator root or nested sub-coordinator) with zero non-archived child agents defaults COLLAPSED (`▸` chevron, children hidden — including its `Archive (N)` expando, per the existing fold semantics under which a collapsed coordinator renders no child rows); a coordinator with one or more non-archived child agents defaults EXPANDED (`▾`). "Non-archived" is the existing active/archived partition: hera-archived, argus-archived, and dead children all count as archived.

The default applies ONLY to coordinators the operator has not explicitly toggled this session. An explicit fold toggle (`space`, or the Enter-on-header fold) SHALL override the default in either direction and persist for the session: toggling a default-collapsed empty coordinator expands it (revealing its `Archive (N)` expando as today), toggling it again re-collapses it, and a manually-collapsed coordinator with active children stays collapsed across rail rebuilds.

While `l`/showArchived is active, the collapsed DEFAULT SHALL be overridden (an untouched empty coordinator renders expanded so its archived children are reachable through its force-expanded `Archive (N)` expando); an explicit manual collapse still wins over `l`. The Freelance section and the top-level `Archive` expando keep their existing fold defaults, unaffected by this rule.

#### Scenario: Empty coordinator defaults collapsed

- **WHEN** the rail renders a coordinator with zero non-archived child agents (no children at all, or only archived/dead children) that the operator has not toggled
- **THEN** the coordinator row MUST render with the collapsed chevron (`▸`) AND none of its child rows — including its `Archive (N)` expando — may render

#### Scenario: Coordinator with an active child defaults expanded

- **WHEN** the rail renders a coordinator with at least one non-archived child agent that the operator has not toggled
- **THEN** the coordinator row MUST render with the expanded chevron (`▾`) AND its active children MUST render as today

#### Scenario: Manual toggle overrides the default and persists

- **WHEN** the operator toggles the fold of a default-collapsed empty coordinator (`space` or Enter-on-header)
- **THEN** the coordinator MUST expand (revealing its `Archive (N)` expando when it has archived children) AND that explicit state MUST persist across rail rebuilds until toggled again; symmetrically a manually-collapsed coordinator with active children MUST stay collapsed across rebuilds

#### Scenario: `l` force-reveal overrides the collapsed default while active

- **WHEN** showArchived (`l`) is active and the rail renders an untouched coordinator with zero non-archived children
- **THEN** the coordinator MUST render expanded with its `Archive (N)` expando force-expanded so its archived children are visible, AND a coordinator the operator explicitly collapsed MUST remain collapsed
