## Why

Selecting a coordinator in the hera-view rail composes a full-width HERA pane and nothing else — the operator sees the coordinator's PTY but none of the metadata hera already knows about that coordination (its mission, constraints, when it started, which repos it spans, who its agents are). Argus's task view solves the same problem with a right-side "Details" column. The operator greenlit a "coord metadata view": when a coordinator is selected, surface its metadata in a right-side Details pane modeled on argus's Details column, rendered from data hera already stores — no inferred summaries yet.

## What Changes

- When the rail selection is a **coordinator** (orchestrator root header or a nested sub-coordinator), the body composes **rail | HERA | Details** — a right-side Details pane is added alongside the full-width HERA pane.
- The Details pane renders, for the selected coordinator, fields drawn from data already available: **name**, **status** (the same status glyph/state the rail shows for that coordinator), **created**, **last agent activity**, **mission**, **constraints**, **repos in scope** (distinct argus projects across its roles), and an **agent roster** (each role's name, kind, and current rail-displayed status, including sub-coordinators).
- A clearly-marked **placeholder** reserves space for a future inferred description/goal/scope (the living-summary idea) — inference is NOT implemented now.
- **Agent** selection (rail | HERA | AGENT) and **freelancer** selection (rail | AGENT) modes are unchanged. The Details pane is coordinator-mode-only.
- The Details pane is a **flex-proportioned right column** taking ~1/3 of the space right of the rail; the HERA pane keeps ~2/3, so the HERA pane is never starved at narrow terminals (it reflows on resize). This was chosen over a fixed width, which clipped the HERA pane at 80-col terminals.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: coordinator selection gains a right-side Details pane rendering name/status/created/last-activity/mission/constraints/repos-in-scope/agent-roster from existing data; agent and freelancer modes are unchanged.

## Impact

- `internal/view/coord_details.go` (new): the `coordDetails` data model, the `buildCoordDetails` builder (orchEntry + DB → fields), and the `detailsPane` tview primitive that renders them with the argus-sdk theme and the rail's shared `statusIcon`.
- `internal/view/layout.go`: `buildLayout` constructs the Details pane and `layoutPieces` carries it; `DetailsWidth` constant.
- `internal/view/app.go`: `refreshBody` composes the Details pane in coordinator mode; `applyRailSelection` populates it for the selected coordinator (root header or sub-coordinator).
- `internal/view/coord_details_test.go` (new): builder field tests + coord-mode-only composition tests.
