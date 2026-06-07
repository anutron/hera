## 1. Spec

- [x] 1.1 Write proposal + hera-view delta (empty coord defaults collapsed; active child defaults expanded; manual toggle overrides and persists; `l` force-reveals); `openspec validate collapse-empty-coords --strict` green

## 2. Tests first (TDD)

- [x] 2.1 Failing rail_list tests: empty coord renders header-only with `▸` and no Archive expando; coord with an active child renders `▾` + children
- [x] 2.2 Failing tests: manual toggle expands a default-collapsed empty coord (Archive expando appears) and persists across rebuilds; manual collapse of a busy coord persists
- [x] 2.3 Failing tests: showArchived (`l`) force-reveals an untouched empty coord's archived children; an explicitly collapsed coord stays collapsed under `l`; sub-coordinator with zero active children defaults collapsed

## 3. Implementation

- [x] 3.1 Add effective-collapse helper to railList (explicit `collapsed` entry wins; else default = zero non-archived children && !showArchived) and route buildRows/appendOrchChildren, drawOrchRow/drawSubCoordRow chevrons, and ToggleCollapse through it
- [x] 3.2 Full `go test ./... -race -count=1` green

## 4. Ship

- [x] 4.1 Commit code + tests + change folder together; report via hera_send
