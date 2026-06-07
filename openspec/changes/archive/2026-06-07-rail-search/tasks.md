## 1. Spec

- [x] 1.1 Write proposal + design + hera-view delta (rail `/` name filter: ancestry-preserving, Esc-restores, Enter-accepts, auto-expand; selection marker probe-gated on `HERA_LIVE_PROBE`); `openspec validate rail-search --strict` green

## 2. Tests first (TDD)

- [x] 2.1 Failing filter tests (`rail_list_test.go`): `SetFilter` narrows rows to name matches; a matching agent keeps its parent coordinator header (ancestry); auto-expand while filtering; whitespace terms; Esc/ClearFilter restores the full rail; Enter/AcceptFilter keeps the query but leaves input mode
- [x] 2.2 Failing router tests (`keys_test.go`): while the rail is filtering the router yields keys to the rail (a mutation rune like `a` does NOT fire the mutation handler); after accept, j/k resume navigating
- [x] 2.3 Failing marker-gate tests (`rail_list_test.go`): with `probeMarker` off no `›` renders (selection still styled); with `probeMarker` on the `›` renders once on the selected row; gutter never shifts content in either state

## 3. Implementation

- [x] 3.1 `rail_list.go`: `filtering`/`filter` state + `matchesFilter` + ancestry-preserving, auto-expanding `buildRows` filtering; `BeginFilter`/`AcceptFilter`/`ClearFilter`/`HandleFilterKey`/`Filtering`; the `/ <query>` input line in `Draw`; `/`-enters + filter-input routing in the rail `InputHandler`; probe-gated selection marker (`probeMarker` field from `HERA_LIVE_PROBE`)
- [x] 3.2 `keys.go`: `RailFilter` gate (`IsFiltering()`); `HandleKey` yields to the focused rail while filtering (mirrors `ModalGate`)
- [x] 3.3 `app.go`: wire the rail's filter state to the router's `RailFilter`; reflect the accepted query in the rail title
- [x] 3.4 Full `go test ./... -race -count=1` green (note the environmental 1Password-signer worktree-remover test if it appears)

## 4. Ship

- [x] 4.1 Commit code + tests + change folder together on the feature branch; report via hera_send (search semantics, probe-env var reused, files, spec path, tests, branch)
