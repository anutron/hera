## 1. Spec

- [x] 1.1 Write proposal + design + hera-view delta (coordinator selection renders a right-side Details pane with name/status/created/last-activity/mission/constraints/repos-in-scope/agent-roster; agent and freelancer modes unchanged); `openspec validate coord-details-pane --strict` green

## 2. Tests first (TDD)

- [x] 2.1 Failing builder tests: `buildCoordDetails` from a seeded orchestrator + roles + bindings yields the right name, created, mission, constraints, distinct repos, roster (name/kind/status incl. sub-coordinator), and a last-activity ≥ the latest binding/status time
- [x] 2.2 Failing composition tests: selecting a coordinator (root header and sub-coordinator) composes the Details pane in the body; selecting an agent or freelancer does NOT; the Details pane renders the coordinator's fields on screen

## 3. Implementation

- [x] 3.1 `coord_details.go`: `coordDetails`/`rosterEntry` model, pure `buildCoordDetails(ctx, db, orch)` builder, and the `detailsPane` tview primitive (labeled fields via `widget.DrawText` + SDK theme, status glyph via the shared `statusIcon`, wrapped mission/constraints, repos + roster lists, future-summary placeholder)
- [x] 3.2 `layout.go`: `detailsPane` in `buildLayout`/`layoutPieces` + the `coordDetailsHERAFlex`/`coordDetailsPaneFlex` 2:1 split constants
- [x] 3.3 `app.go`: `refreshBody` adds the Details pane iff `coordPresent && !agentPresent`; `applyRailSelection` populates it for the selected coordinator (root header and sub-coordinator)
- [x] 3.4 Full `go test ./... -race -count=1` green

## 4. Ship

- [x] 4.1 Commit code + tests + change folder together on the feature branch; report via hera_send (layout choice, fields, data sources, files, spec path, tests, branch)
