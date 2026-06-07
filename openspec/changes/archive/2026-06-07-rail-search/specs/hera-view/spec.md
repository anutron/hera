## ADDED Requirements

### Requirement: The rail supports a `/` name filter

While the RAIL is focused, pressing `/` SHALL enter a rail search input mode. While in input mode, typed characters SHALL build a filter query and the rail SHALL narrow to the rows whose name matches the query by case-insensitive substring, across coordinators, agents, and freelancers. Whitespace-separated query terms SHALL each match the row name (or, for a freelance row, its repo), so every term must match for the row to be shown.

While a filter is active the rail SHALL remain ancestry-preserving and legible:

- A coordinator whose name matches, OR which has any descendant role whose name matches, SHALL remain visible so a matching agent always keeps its parent coordinator header.
- Sections (coordinators, Freelance repo groups, the Archive expando) SHALL auto-expand while a filter is active so matching rows are never hidden behind a fold.
- A section header or separator SHALL render only when it has at least one visible row beneath it, so the operator never lands on an empty section.

`Esc` while in input mode SHALL exit search and restore the full, unfiltered rail (clearing the query). `Enter` while in input mode SHALL accept the filter — keeping the query applied but leaving input mode so `j`/`k` navigate the filtered set. The active query SHALL be shown unobtrusively (a `/ <query>` input line while typing, and the active query reflected in the rail title once accepted).

While in input mode the rail's mutation keys (`n`/`r`/`a`/`l`/`s`/`S`/`P`/`?`) and the `Esc`-release-to-argus behavior SHALL NOT fire; those keystrokes are filter input instead. After the filter is accepted (input mode off), normal rail key handling SHALL resume.

#### Scenario: `/` narrows the rail to matching rows

- **WHEN** the operator presses `/` and types a query
- **THEN** the rail MUST show only rows whose name matches the query (case-insensitive substring), hiding non-matching coordinators, agents, and freelancers

#### Scenario: A matching agent keeps its parent coordinator visible

- **WHEN** a filter matches an agent whose name does not match its coordinator's name
- **THEN** the agent's parent coordinator header MUST remain visible (ancestry-preserving) and expanded so the matching agent is shown under it

#### Scenario: Esc restores the full rail

- **WHEN** the operator presses `Esc` while in search input mode
- **THEN** the filter MUST clear, input mode MUST exit, and the rail MUST render every row it showed before the filter

#### Scenario: Enter accepts the filter and returns to navigation

- **WHEN** the operator presses `Enter` while in search input mode
- **THEN** input mode MUST exit, the query MUST stay applied (the rail stays filtered), and `j`/`k` MUST navigate the filtered set

#### Scenario: Mutation keys are filter input while typing

- **WHEN** the operator is in search input mode and types a character that is otherwise a rail mutation key (such as `n` or `a`)
- **THEN** that character MUST be appended to the filter query and MUST NOT trigger the mutation

### Requirement: The selection marker is gated behind the probe env

The selected rail row's left-gutter `›` selection marker SHALL render only when the live-probe environment variable (`HERA_LIVE_PROBE`) is set. In normal operation (the variable unset), the selected row's gutter SHALL render a blank cell — no glyph — and the selection SHALL be indicated by the selected-row text style (`theme.StyleSelected`) alone. The gutter width SHALL be reserved unconditionally so that toggling the marker on or off, and moving the cursor between rows, never shifts row content horizontally.

#### Scenario: No marker in normal operation

- **WHEN** the rail renders with the probe environment variable unset
- **THEN** no `›` selection marker MUST appear on any row, and the selected row MUST still be distinguishable by its selected text style

#### Scenario: Marker present under the probe env

- **WHEN** the rail renders with the probe environment variable set
- **THEN** the `›` selection marker MUST render exactly once, on the selected row, as before

#### Scenario: Gutter does not shift content

- **WHEN** the selection moves between rows, with the marker either gated off or on
- **THEN** no row's rendered content MUST shift horizontally (the marker gutter is reserved in both states)
