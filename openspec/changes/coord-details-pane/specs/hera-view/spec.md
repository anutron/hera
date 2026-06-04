## ADDED Requirements

### Requirement: Coordinator selection renders a right-side Details pane

When the rail selection is a coordinator — an orchestrator root header or a nested sub-coordinator — the body SHALL compose a right-side Details pane alongside the rail and the HERA pane (layout `rail | HERA | Details`). The HERA pane SHALL keep the majority of the width available between the rail and the right edge, so the Details pane never starves the HERA pane at narrow terminals. The Details pane SHALL NOT replace or displace the rail or the HERA pane.

The Details pane SHALL render, for the selected coordinator, fields derived from data hera already stores:

- **Name** of the coordinator.
- **Status** rendered with the same status glyph and state the rail shows for that coordinator (the shared status-icon vocabulary), so the Details status and the rail status never disagree.
- **Created** — the orchestrator's creation time.
- **Last agent activity** — the most recent activity timestamp across the coordinator's roles (role creation, their bindings' start/end, and their status updates).
- **Mission** and **Constraints** — the coordinator role's stored mission and constraints (empty when unset).
- **Repos in scope** — the distinct argus projects across the coordinator's roles.
- **Agent roster** — each of the coordinator's child roles by name and kind with its current rail-displayed status, including any nested sub-coordinators.

The Details pane SHALL include a clearly-marked placeholder for a future inferred description/goal/scope summary; that inference SHALL NOT be implemented by this change.

#### Scenario: Details pane appears on coordinator selection

- **WHEN** the operator selects a coordinator row (orchestrator root header or a nested sub-coordinator)
- **THEN** the body MUST compose the rail, the full-width HERA pane, AND a right-side Details pane (three columns), with the rail and HERA pane still present

#### Scenario: Details fields are rendered from available data

- **WHEN** the Details pane renders for a selected coordinator
- **THEN** it MUST show the coordinator's name, a status indicator matching the rail's status glyph for that coordinator, its created time, its last agent activity, its mission and constraints, the distinct repos across its roles, and a roster of its child roles (name, kind, current status) including sub-coordinators

#### Scenario: Agent and freelancer selections are unchanged

- **WHEN** the operator selects an agent (worker) row or a freelancer row
- **THEN** the body MUST compose exactly as before — `rail | HERA | AGENT` for an agent and `rail | AGENT` for a freelancer — and MUST NOT render the Details pane

#### Scenario: Sub-coordinator Details describe the sub-coordinator's own group

- **WHEN** the operator selects a nested sub-coordinator row
- **THEN** the Details pane MUST render the metadata of the sub-coordinator's own orchestration group (its name, mission, repos, and roster), not the parent coordinator's
