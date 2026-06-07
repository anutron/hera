## ADDED Requirements

### Requirement: Rail status glyphs mirror argus's task-panel table exactly

The status glyph and color rendered for a known argus task state — on every rail row kind (coordinator headers, agent/worker rows, sub-coordinator rows, and freelance rows) and in both active and archived/dimmed variants — SHALL mirror argus's task-panel mapping (argus `internal/tui/theme/theme.go:29-34` + `internal/tui/taskview/tasklist.go:1095-1132`) EXACTLY:

| Argus state | Glyph | Color |
|---|---|---|
| `pending` | `○` U+25CB | gray (`theme.ColorPending`) |
| `complete` | `✓` U+2713 | green (`theme.ColorComplete`) |
| `in_review` | `󰖔` U+F0594 (nf-md-weather_night, moon-with-stars) | blue (`theme.ColorInReview`) |
| `in_progress` + needs-input | `` U+F059 (nf-fa-question_circle) | `theme.ColorNeedsInput` (#faa378) |
| `in_progress` + idle | `` U+F186 (nf-fa-moon_o) | blue (`theme.ColorInReview`) |
| `in_progress` + running (idle false/omitted) | spinner frames U+EE06..U+EE0B, animated at argus's 150 ms cadence | orange (`theme.ColorInProgress`) |

`in_review` MUST be visually distinct from `complete` by GLYPH, not merely by color: the checkmark `✓` MUST NOT render for any state other than `complete`. Within `in_progress`, needs-input takes priority over idle and running, mirroring argus's switch nesting; the needs-input override applies only to `in_progress` (argus's API serves `needs_input` solely for `in_progress`). Argus's TUI-only `idleUnvisited` variant (moon-with-stars for unvisited-idle) is not in the API and MUST NOT be mirrored: all `in_progress`+idle rows render the plain moon `` U+F186. Colors SHALL come from the SDK theme's argus values (`github.com/anutron/argus-sdk/theme`), never re-hardcoded hex.

Running rows SHOULD animate: while at least one visible rail row is in the running state, the view SHALL repaint at the spinner cadence so the spinner advances by wall clock (argus's animation source); each repaint MUST render the frame for the current wall-clock instant. When no running row is visible, the spinner driver MUST NOT schedule repaints.

This table defines the GLYPH for a known state; it composes with (and does not supersede) the rail-truthfulness requirement: archive state and binding liveness modulate only the STYLE (dimmed), never the glyph, and the dimmed `○` fallback remains reserved for archived/dead rows with UNKNOWN state.

#### Scenario: in_review renders the moon-with-stars, never a check

- **WHEN** a rail row's bound task has argus status `in_review`
- **THEN** the row MUST render `󰖔` (U+F0594) in blue (`theme.ColorInReview`) AND MUST NOT render `✓`

#### Scenario: complete is the only state that renders a checkmark

- **WHEN** rail rows render tasks in every known argus state
- **THEN** only the `complete` row renders `✓` (green, `theme.ColorComplete`); every other state renders its own table glyph

#### Scenario: pending renders the open circle

- **WHEN** a rail row's bound task has argus status `pending`
- **THEN** the row MUST render `○` in gray (`theme.ColorPending`)

#### Scenario: in_progress idle renders the plain moon in blue

- **WHEN** a rail row's bound task is `in_progress` with `idle` true and `needs_input` false
- **THEN** the row MUST render `` (U+F186) in blue (`theme.ColorInReview`), not dimmed and not the moon-with-stars

#### Scenario: in_progress running renders the animated spinner in orange

- **WHEN** a rail row's bound task is `in_progress` with `idle` false/omitted and `needs_input` false
- **THEN** the row MUST render the spinner frame for the current wall-clock instant (one of U+EE06..U+EE0B at a 150 ms cadence) in orange (`theme.ColorInProgress`), and successive repaints across frame boundaries MUST advance the frame

#### Scenario: needs-input outranks idle and running within in_progress

- **WHEN** a rail row's bound task is `in_progress` with `needs_input` true (regardless of `idle`)
- **THEN** the row MUST render `` (U+F059) in `theme.ColorNeedsInput` (#faa378)

#### Scenario: needs-input does not override a non-in_progress status

- **WHEN** a rail row's state carries `needs_input` true but a status other than `in_progress` (a shape argus's API never serves)
- **THEN** the row MUST render the glyph for its status, mirroring argus's switch nesting

#### Scenario: archived variants keep the table glyph dimmed

- **WHEN** a rail row bucketed as archived/dead has a known argus state
- **THEN** the row MUST render the same table glyph for that state in the dimmed style (a running archived row renders the spinner frame dimmed)

#### Scenario: spinner driver is quiet without running rows

- **WHEN** no visible rail row is in the running state
- **THEN** the spinner driver MUST NOT schedule spinner repaints

## MODIFIED Requirements

### Requirement: Rail renders coordinators as foldable rows with Archive expandos

The system SHALL render the rail as a tree mirroring argus's task panel: each coordinator (orchestrator root or sub-coordinator) is a selectable, foldable row rendered in argus's task-panel order — a status icon, then a chevron (`▾` expanded / `▸` collapsed), then a coordinator marker glyph (`󰹻`, U+F0E7B) before the name, then a live-child `(N)` count; its agents render as indented child rows; a worker that is itself a coordinator renders as a foldable coordinator row with its own nested children, recursively (a sub-coordinator MAY itself contain further sub-coordinators). Among a coordinator's children, sub-coordinators MUST sort before leaf workers (folders-first). Rows MUST NOT render kind pills.

Because hera's data model is flat (a role has a single orchestrator; orchestrators have no parent link), a sub-coordinator is modeled as a multi-binding: the SAME argus task is both a worker role under a parent orchestrator AND the coord of a separate child orchestrator (the join key is a worker role's bound argus task equalling a child orchestrator's coord task). The system SHALL resolve this multi-binding so the child orchestrator's roles nest beneath the worker row (which is rendered as a coordinator row), the child orchestrator MUST NOT also render at the top level, and resolution MUST guard against cycles. Every rail row carries a tree depth; rows MUST be indented by their depth (deeper rows further right), and an `Archive (N)` expando's archived children MUST indent one level deeper than the expando header.

The status icon (on both coordinator headers and agent rows) reflects argus status using argus's own task-panel glyph table (`` needs-input, `✓` complete, `󰖔` in-review, spinner running, `` idle, `○` pending — see the glyph-table requirement); a coordinator header's status icon is driven by its coord task's argus state, and a sub-coordinator row's by its own bound task's state. The coordinator marker glyph (distinct from the transient status icon) flags the row as a coordinator regardless of state; the prototype's `◆` root-coord icon is superseded by this status-icon + marker pairing. Every coordinator with archived direct children MUST render an `Archive (N)` expando below its active agents (collapsed by default) in the DEFAULT view — no `l` required: a hera-archived child role (`archived_at` set) MUST appear under its coordinator's `Archive (N)` expando exactly like an argus-archived or dead child, so archiving a row never makes it unreachable; `l` force-expands the expandos. Archived children MUST NOT inflate the coordinator header's live-child `(N)` count. Archived root coordinators MUST render under a top-level `Archive` section at the bottom of the rail. `space` MUST toggle the fold of the selected coordinator (root OR sub-coordinator) or Archive section.

The rail's currently selected row MUST be indicated by selected-text styling — its name rendered in argus's selected style (`theme.StyleSelected`, pink `theme.ColorSelected`) — applied consistently across ALL selectable row types (coordinator headers, agent/worker rows, sub-coordinator rows, Freelance repo headers, and Archive expandos). In ADDITION to the styling, the selected row MUST be identifiable WITHOUT color: the rail SHALL reserve a marker gutter at the start of every row and render a selection marker glyph (`›`) in that gutter on the selected row only; every other row MUST render a space there, so nothing shifts when the selection moves. The marker MUST apply to every selectable row kind (coordinator headers, agent/worker rows, sub-coordinator rows, Freelance repo headers, and Archive expandos), so a colorless text capture of the rail (a monochrome renderer, a screen reader, a reduced-color terminal) still reveals exactly which row the cursor is on. The rail MUST NOT paint any cell background to indicate selection: no row — selected or not — may render a filled (non-default) cell background. This guarantees no stale highlight cell can linger on a previously-selected row when the cursor moves, because no row ever writes a non-default background that a later draw would have to clear. The per-row selection indicator is distinct from, and MUST NOT be confused with, the rail pane's focus border (argus's focus color on the pane edge), which is governed by the focus-model requirement.

#### Scenario: Coordinator row is foldable with a count

- **WHEN** the rail renders a coordinator that has live agents
- **THEN** the row MUST show a chevron and a `(N)` live-child count AND pressing `space` on it MUST toggle whether its children are shown

#### Scenario: Coordinator header carries a status icon and coordinator marker

- **WHEN** the rail renders a coordinator whose coord task argus state is known
- **THEN** the header row MUST render, before the name, a status icon reflecting that argus state (the same task-panel glyph table as agent rows) AND the `󰹻` coordinator marker glyph AND MUST NOT render the prototype's `◆`

#### Scenario: Sub-coordinators sort before leaf workers

- **WHEN** a coordinator has both sub-coordinator children and leaf-worker children
- **THEN** the sub-coordinator rows MUST render above the leaf-worker rows

#### Scenario: Sub-coordinator renders as a nested foldable coord row with its children

- **WHEN** a worker role under a parent orchestrator has a bound argus task that is ALSO another (child) orchestrator's coord task
- **THEN** that worker MUST render as a foldable coordinator row (chevron + `󰹻` marker + live-child `(N)` count) with the child orchestrator's roles nested one level deeper, AND the child orchestrator MUST NOT also render as a top-level row

#### Scenario: Selecting a sub-coordinator composes full-width HERA bound to its own task

- **WHEN** focus is `RAIL` and a sub-coordinator row is selected
- **THEN** the body MUST compose the full-width `HERA` pane (no `AGENT` pane) bound to the sub-coordinator's OWN argus task (its own coordinator PTY), not the parent orchestrator's coord task, AND `Enter` MUST move focus into that `HERA` pane

#### Scenario: Archived children indent one level deeper than their Archive expando

- **WHEN** a coordinator's `Archive (N)` expando is folded open
- **THEN** its archived children MUST render indented one tree-depth level deeper than the `Archive (N)` expando header

#### Scenario: Archived agents live in their coordinator's Archive expando

- **WHEN** an agent under a coordinator is archived (hera `archived_at` set, argus-archived, or its task gone)
- **THEN** in the DEFAULT view (without `l`) it MUST NOT appear among the coordinator's active rows AND it MUST appear inside that coordinator's `Archive (N)` expando, which is collapsed by default — archiving a row MUST never make it vanish from the rail's reachable tree

#### Scenario: Hera-archiving a row never makes it unreachable

- **WHEN** the operator presses `a` on an agent row in the default view (showArchived off) and the role's `archived_at` is set
- **THEN** the next rail rebuild MUST render the coordinator's `Archive (N)` expando counting that role AND folding the expando open MUST reveal the row; the coordinator header's live-child `(N)` count MUST exclude it AND startup auto-selection MUST never bind an archived role's task

#### Scenario: Archived root coordinators live in the top-level Archive

- **WHEN** a root coordinator is archived
- **THEN** it MUST appear only under the top-level `Archive` section at the bottom of the rail

#### Scenario: Coordinator with no workers renders header-only

- **WHEN** an orchestrator has a live coord but no worker agents
- **THEN** the rail MUST render only its foldable coordinator row with no child agent row, and selecting it MUST compose the full-width HERA pane bound to the coord's PTY

#### Scenario: Selected row is indicated by selected-text styling, not a background fill

- **WHEN** the rail renders with the cursor on a selectable row (a coordinator header or an agent/worker row)
- **THEN** that row's name MUST render in `theme.StyleSelected` (pink `theme.ColorSelected`) AND none of that row's cells may carry a non-default cell background (no `theme.ColorHighlight` fill)

#### Scenario: Selected row is identifiable without color via the marker glyph

- **WHEN** the rail renders with the cursor on any selectable row (coordinator header, agent/worker row, sub-coordinator row, Freelance repo header, or Archive expando) and all color/styling is stripped from the output
- **THEN** the selected row MUST begin with the `›` selection marker glyph in the marker gutter AND no other row may render the marker, so the cursor position is identifiable from the text alone

#### Scenario: Marker gutter keeps columns stable across selection moves

- **WHEN** the rail cursor moves from one selectable row to another
- **THEN** the newly selected row MUST gain the `›` marker, the previously selected row MUST render a space in the marker gutter, AND no row content may shift columns as a result of the move

#### Scenario: Non-selected rows carry no lingering background

- **WHEN** the rail renders with the cursor on one row
- **THEN** every other (non-selected) row MUST render with the default cell background, so no stale highlight from a previous cursor position can persist

### Requirement: All hera-rendered surfaces share one consistent background

Every hera-rendered surface — the root canvas, the top bar, the body, the rail, the coordinator and agent pane frames (including their empty/placeholder state), the Details pane, the gaps/canvas between or around panes, and every modal/popup overlay (input, two-field, confirm, select, error) — SHALL paint the SAME background color the SDK terminalpane uses for its emulator interior cells: the terminal's default background (`tcell.ColorDefault`, exposed as the view package's single `heraBackground` constant). No hera surface may render the grey-blue backgrounds that previously leaked through — neither tview's stock primitive/contrast defaults nor the argus status-bar dark gray (`theme.ColorStatusBG`). A modal overlay is distinguished from the chrome behind it by its cyan border + title and its highlighted buttons/fields, NOT by a different background fill.

#### Scenario: Chrome surfaces use the single hera background

- **WHEN** the hera-view application is built
- **THEN** the root, top bar, body, rail, both panes, and the Details pane MUST each report `heraBackground`, AND none may report `theme.ColorStatusBG`, AND the global tview default background (primitive and contrast) MUST be repointed to `heraBackground` so any unset primitive falls through to the same black

#### Scenario: Modal overlays use the single hera background, not grey-blue

- **WHEN** any modal/popup overlay (input, two-field, confirm, select, or error) is rendered over the base layout
- **THEN** no cell on the drawn surface may carry tview's stock contrast background or `theme.ColorStatusBG`, AND at least one cell of the overlay MUST carry `heraBackground`
