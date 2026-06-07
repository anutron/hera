## Context

The body is a `tview.Flex` (columns) recomposed by `App.refreshBody` from two flags — `coordPresent` / `agentPresent` — set by `App.applyRailSelection` per the rail selection (the D13 three-mode layout). Coordinator mode is `coordPresent=true, agentPresent=false` (rail + full-width HERA). The Details pane slots into this mode only.

## Decisions

### Details renders only in coordinator mode

The pane is added to the body iff `coordPresent && !agentPresent` — the exact predicate for coordinator mode. Agent mode (both true → rail | HERA | AGENT) and freelance mode (`!coordPresent && agentPresent` → rail | AGENT) never add it, so today's layouts are byte-for-byte unchanged.

### Flex-proportioned right column (HERA keeps the majority)

In coordinator mode the space right of the fixed rail is split `coordDetailsHERAFlex:coordDetailsPaneFlex = 2:1` — HERA ~2/3, Details ~1/3. Flex (not fixed) was chosen over a fixed `DetailsWidth`: a fixed 40-col column starves the HERA pane at narrow terminals (at 80 cols, rail 36 + details 40 leaves only 4 for HERA, clipping its content), which broke the placeholder-visibility guard. With a 2:1 flex split the HERA pane is never starved — at 80 cols it keeps ~29 cols; at 140 cols Details grows to a comfortable ~35. tview computes the split at Draw time against the real width, so it reflows on resize without recomposition. Tradeoff vs a fixed width: Details width varies with the terminal; acceptable since its content wraps. Agent mode (Details absent) is unchanged.

### Data sources — render from available data, no inference

- **Name / status**: from the selected `*orchEntry` (`Name`, `CoordStatus`/`CoordHasState`/`CoordIdle`/`CoordNeedsInput`, `CoordTaskID`). The status glyph reuses the rail's shared `statusIcon` so Details and rail never disagree; the spinner frame is read at Draw time so a running coordinator animates.
- **Created**: orchestrator `created_at` (`Orchestrators.GetByID`).
- **Mission / Constraints**: the coordinator role's columns (`Roles.ListByOrchestratorInclusive`, first `coordinator`-kind role).
- **Repos in scope**: distinct non-empty `argus_project` across all the orchestrator's roles, sorted.
- **Last agent activity**: max over the orchestrator's role `created_at`, their bindings' `started_at`/`ended_at`, and their `role_status.updated_at`; falls back to the orchestrator `created_at`.
- **Agent roster**: the rail-displayed child rows (`orchEntry.Roles`) — name, kind, and the same status inputs the rail row uses, so a sub-coordinator (a promoted worker carrying `childOrch`) appears with `coordinator` kind. The coordinator itself is the header (top "Status" field), not a roster entry.

A sub-coordinator selection (`roleEntry` with `childOrch`) describes its `childOrch` (itself an `*orchEntry`), so the same builder serves both root and sub coordinators.

### Builder is pure; the pane is dumb

`buildCoordDetails(ctx, db, orch)` is a pure DB+orchEntry→struct function (unit-testable with an in-memory DB). `detailsPane` only renders the struct (a `tview.Box` subclass drawing labeled fields with `widget.DrawText` + the SDK theme, mirroring argus's `drawField`). This keeps the field-derivation logic out of the draw path and independently testable.

### Future inferred summary

A dimmed placeholder line reserves the spot for the bookmarked living-summary (inferred description/goal/scope). No inference is wired now.

## Risks / Tradeoffs

- Fixed width tightens HERA at narrow terminals (<~90 cols). Mitigated by coordinator-mode-only scope and the operator's ability to switch to an agent.
- `buildCoordDetails` issues a few local SQLite reads per coordinator selection (debounced via the existing rail-select debounce). No argus HTTP, so it does not add network latency to the event loop.
