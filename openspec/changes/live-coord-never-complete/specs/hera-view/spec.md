## ADDED Requirements

### Requirement: Live coordinators never render ✓/complete

A LIVE coordinator (non-archived, not in the mixed-coord repair state) MUST
NOT render the `✓ complete` glyph or the "complete" status label in either the
rail glyph or the Details pane "Status:" field, regardless of the underlying
argus task status.

Argus auto-completes coordinator tasks when the agent session goes idle
(waiting for the human). This is an argus-internal lifecycle event that does not
mean the coordinator is done. Hera MUST mask this: when a live coordinator's
argus task status is `complete`, hera MUST display the status as `in_progress +
idle` (rendering the idle moon ☾ glyph, label "idle") in both the rail header
icon and the Details pane. The raw `CoordStatus` field MAY retain the argus
value internally; the masking is display-layer only.

The `orchIcon` function applies this masking so the rail header never shows `✓`
for a live coordinator. The `buildCoordDetails` function applies the same
masking so the Details pane status field agrees with the rail.

Archived coordinators are exempt — a coordinator rendered in the Archive section
may show its true argus status (including `✓`) because it represents a completed
and finished session.

#### Scenario: Live coordinator with argus-complete task renders idle glyph

- **WHEN** a live (non-archived) coordinator's argus task reports status
  `complete`
- **THEN** the rail header MUST render the idle moon glyph (☾, `theme.IconMoonOutline`)
  with `theme.StyleInReview` — NOT the `✓` checkmark
- **AND** the Details pane "Status:" field MUST show "idle", NOT "complete"

#### Scenario: Archived coordinator with complete task still shows ✓

- **WHEN** an archived coordinator's argus task reports status `complete`
- **THEN** the rail header MUST render `✓` with `theme.StyleDimmed` (consistent
  with all other archived rows)

### Requirement: Coordinator roles excluded from ^r prune targets

`ops.ListCompletedAgents` (the `^r` prune eligibility query) MUST skip roles
whose kind is `coordinator`. A coordinator role whose argus task reports
`complete` MUST NOT appear in the prune confirmation list, because argus
auto-completion does not indicate the coordinator is safe to destroy.

#### Scenario: Complete coordinator is not listed as a prune target

- **WHEN** a coordinator role's live binding's argus task reports status
  `complete`
- **THEN** `ListCompletedAgents` MUST NOT include that role in its result
- **AND** a worker role in the same orchestrator whose task is also `complete`
  MUST still be listed (the guard is coordinator-specific)
