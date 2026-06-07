## MODIFIED Requirements

### Requirement: In-review status glyph

The system SHALL render `theme.IconReview` (clipboard-check, U+0F00BC) for the `in_review` argus task status, with `theme.StyleInReview` (blue). The historical `theme.IconMoonStars` mapping for `in_review` is superseded by this requirement. The `in_review` status MUST NOT render a checkmark (`✓`); only `complete` renders a checkmark.

#### Scenario: in_review renders the clipboard-check glyph

- **GIVEN** a rail row whose bound argus task has status `in_review`
- **THEN** the status glyph MUST be `theme.IconReview` (U+0F00BC, clipboard-check) rendered in `theme.StyleInReview` (blue)
- **AND** the glyph MUST NOT be `theme.IconMoonStars` or `✓`

### Requirement: Role-row base indentation alignment

All role rows (leaf workers and sub-coordinators) at tree depth N MUST render their leading status icon at column `cx + N × indentStep`, with NO additional leading offset. A leading space prefix that historically shifted leaf worker icons 1 column right of sub-coordinator icons at the same depth is eliminated. Direct members of an orchestrator — regardless of whether a sibling is an expanded nested coordinator — MUST render at the orchestrator's child indent.

#### Scenario: Sibling worker aligns with sub-coordinator at same depth

- **GIVEN** an orchestrator with a sub-coordinator (a worker role that is also another orchestrator's coord) and a sibling leaf worker, both at tree depth 1
- **WHEN** the sub-coordinator is expanded
- **THEN** the sibling worker's status icon MUST appear at the same horizontal column as the sub-coordinator's status icon (both at `cx + 1 × indentStep`)
- **AND** the sibling worker's status icon MUST NOT appear at the depth-2 column (`cx + 2 × indentStep`)

## ADDED Requirements

### Requirement: PR review indicator cell

The system SHALL render a per-row GitHub PR review indicator glyph on role rows whose bound argus task carries an actionable PR review state. The indicator appears after the status icon and before the role name, using the following glyph mapping:

| pr_state string    | glyph                  | style                  |
|--------------------|------------------------|------------------------|
| `awaiting-review`  | `theme.IconPRAwaiting` | `theme.StylePRAwaiting` |
| `changes-requested`| `theme.IconPRChanges`  | `theme.StylePRChanges`  |
| `approved`         | `theme.IconPRApproved` | `theme.StylePRApproved` |

The PR indicator cell:
- MUST only consume 2 columns of horizontal space when the state is actionable; for non-actionable states (`none`, `draft`, `merged-closed`, `unknown`, or absent) the cell MUST be omitted and the name column reclaims the space.
- MUST NOT block rail rendering on per-row argus HTTP calls; the PR state MUST be read from the existing poll-and-cache mechanism used for task status (`ArgusStateCache`).
- MUST degrade gracefully (no cell, no error) when argus does not populate the `pr_state` field on the task DTO.
- Applies to managed worker/coordinator role rows only; freelance repo-group headers and Archive expandos carry no PR indicator.

#### Scenario: Actionable PR state renders glyph and consumes width

- **GIVEN** a role row whose bound task has PR state `awaiting-review`, `changes-requested`, or `approved`
- **THEN** the corresponding PR glyph MUST render after the status icon, using the correct style from the mapping above
- **AND** the glyph cell MUST consume 2 horizontal columns (shifting the name right)

#### Scenario: Non-actionable PR state renders no cell

- **GIVEN** a role row whose bound task has PR state `none`, `draft`, `merged-closed`, `unknown`, or no pr_state
- **THEN** NO PR glyph cell MUST render
- **AND** the name column MUST start immediately after the status icon (reclaiming the 2 columns)

#### Scenario: No pr_state field degrades gracefully

- **GIVEN** a role row whose argus task has no `pr_state` field (e.g. argus daemon is older)
- **THEN** the rail MUST render the row without error and without a PR indicator cell
