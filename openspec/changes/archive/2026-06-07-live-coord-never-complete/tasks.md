# Tasks

## 1. Spec

- [x] 1.1 Delta: live coordinators never render ✓/complete; coordinator roles excluded from prune targets

## 2. Tests (failing first)

- [x] 2.1 `orchIcon`: live coord with `CoordStatus=complete` renders idle moon (☾), not ✓
- [x] 2.2 `buildCoordDetails`: live coord with `CoordStatus=complete` produces `Status=in_progress+idle` (label "idle")
- [x] 2.3 `ListCompletedAgents`: coordinator role with complete argus task is excluded; sibling worker is still listed

## 3. Implementation

- [x] 3.1 `liveCoordDisplayStatus` helper: masks complete→in_progress+idle for non-archived coordinators
- [x] 3.2 `orchIcon`: uses helper before calling statusIcon
- [x] 3.3 `buildCoordDetails`: uses helper when setting Status/CoordIdle
- [x] 3.4 `ListCompletedAgents`: loads role first, skips `KindCoordinator`

## 4. Verify

- [x] 4.1 `go test ./... -race -count=1` green
- [x] 4.2 `openspec validate --all --strict` green
