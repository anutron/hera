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

## MODIFIED Requirements

### Requirement: Body layout adapts to the selected row's kind

The system SHALL render the body inside top and bottom chrome bars whenever the view application is active, in one of THREE modes determined by the rail's current selection (mirroring the canonical prototype's SOLO/PAIR behavior):

- **Coordinator mode** (a coordinator row is selected — root or sub): rail + a full-width **Coord pane** (the coordinator's own PTY) + a right-side Details pane. No AGENT pane is composed.
- **Agent mode** (a worker/agent row is selected): rail + **Coord pane** (the agent's coordinator's PTY) + **AGENT pane** (the agent's PTY) — a split body.
- **Freelance mode** (a freelance row is selected): rail + a single full-width **AGENT pane** (the freelancer's PTY). No Coord pane is composed.

The center coordinator pane is titled **Coord** (parallel to the **Agent** pane title), NOT "HERA": the argus chrome already identifies the view, so a hera-stamped "HERA" label is redundant. The top chrome bar MUST NOT stamp any hera branding (no literal `HERA`/`Hera` text); it is kept as an empty 1-row bar so the body's vertical geometry is unchanged. The rail keeps its "Rail" title. Bottom-bar hints are advertised to argus per the key-surrender contract (argus draws the plugin-mode bar); when rendered standalone the bottom bar is focus-aware.

A pane that has no bound task — the **empty/placeholder state** ("(no coord selected)" / "(no agent selected)") — MUST fill its layout-allocated rect exactly as a bound pane does: the placeholder pane's emulator surface MUST track the full inner rect of its Flex allocation rather than staying at the construction-time default size, so empty coord/agent panes fill the available vertical space and split the horizontal space on the same proportions as their content counterparts. No source-PTY resize is dispatched for an unbound placeholder pane (there is no PTY to size).

#### Scenario: Coordinator selection is a full-width Coord pane

- **WHEN** a coordinator row (root or sub) is selected
- **THEN** the body MUST be the rail plus a full-width Coord pane bound to that coordinator's PTY (alongside the Details pane) AND no AGENT pane MUST be present

#### Scenario: Agent selection splits Coord + AGENT

- **WHEN** a worker/agent row is selected
- **THEN** the body MUST be the rail + a Coord pane bound to the agent's coordinator + an AGENT pane bound to the agent

#### Scenario: Freelance mode collapses to rail + full-width agent

- **WHEN** the rail selection moves to a freelance row
- **THEN** the body MUST be the navigation rail plus a single AGENT pane spanning the remaining width AND no Coord pane MUST be present

#### Scenario: Switching selection re-composes the mode

- **WHEN** the rail selection moves between a coordinator, an agent, and a freelancer
- **THEN** the body MUST re-compose to the corresponding mode (full-width Coord / split / full-width AGENT), tearing down the now-absent pane's subscription

#### Scenario: Project-mode rail traversal updates both panes

- **WHEN** rail selection moves to a worker agent whose project's coord differs from the previous selection's project
- **THEN** the COORD pane MUST switch to the new project's coord binding ring buffer AND the AGENT pane MUST switch to the new agent's binding ring buffer

#### Scenario: Empty coord and agent panes fill their allocation

- **WHEN** the body composes an empty coord pane and/or an empty agent pane (no task bound, showing the placeholder text)
- **THEN** each empty pane MUST fill the full vertical space of its Flex allocation and the two panes MUST split the horizontal space on the same proportions as when they hold live content (no shorter, unevenly-sized placeholder boxes)

#### Scenario: Top chrome bar carries no hera branding

- **WHEN** the view surface renders in any mode
- **THEN** the top chrome row MUST NOT contain the literal text `HERA` or `Hera` AND the coordinator pane MUST be titled `Coord` (not `HERA`)

### Requirement: Hera advertises focus-aware hotkeys to argus and renders no internal bottom bar

The system SHALL push a `{"type":"hotkeys","items":[...]}` text control frame to argus on view connect and on every focus-state change, describing the key bindings for the current focus state (`RAIL` / `COORD` / `AGENT`), with the operator-facing keys flagged `bar:true` to populate argus's context-sensitive bottom bar and the full set driving argus's help overlay. The system MUST NOT render its own bottom-bar row within the view surface; argus renders the plugin-mode status bar, including the reserved `^Q^Q argus` exit hint that hera neither advertises nor displaces.

The `RAIL`-focus hotkey set advertised with `bar:true` MUST include the spawn-worker key `w` (label "new agent") and the adopt-freelancer key `J` (label "adopt"), alongside the existing rail mutation keys, so both recently-added rail gestures are discoverable in argus's bottom bar. As with the other always-listed rail mutation keys (`n`/`r`/`a`), per-row applicability is surfaced by the op itself (a visible "not applicable" notice when pressed on a row it cannot act on), not by hiding the bar hint.

#### Scenario: hotkeys pushed on focus change

- **WHEN** the focus state changes (for example `RAIL` → `COORD`)
- **THEN** hera MUST send a `{"type":"hotkeys",...}` frame whose items reflect the new focus state's bindings

#### Scenario: no internal bottom bar rendered

- **WHEN** the view surface renders in any focus state
- **THEN** it MUST NOT include hera's own bottom-bar row (argus owns the plugin-mode status bar)

#### Scenario: spawn-worker and adopt keys advertised on the bottom bar

- **WHEN** hera pushes the `RAIL`-focus hotkeys frame
- **THEN** the `bar:true` items MUST include `w` ("new agent") and `J` ("adopt")
