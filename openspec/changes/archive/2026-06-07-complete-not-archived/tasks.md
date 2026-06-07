# Tasks

## 1. Spec

- [x] 1.1 Delta: task status never buckets a rail row; Dead = record-nonexistence only

## 2. Tests (failing first)

- [x] 2.1 Worker row: completed+existing task stays in active children (not Dead, renders visible, `✓`)
- [x] 2.2 Coord header: completed+existing coord task still feeds CoordTaskID/CoordStatus
- [x] 2.3 Freelancer: completed+existing task stays visible in its repo group and counts (covered by TestBuildApp_FreelanceGroupsByProjectExcludesManagedAndArchived's complete `free-b2`)
- [x] 2.4 Record-gone task (warm cache miss) still buckets dead (worker + coord variants)
- [x] 2.5 Spinner driver: hasRunning clears when the last running row leaves the rail

## 3. Implementation

- [x] 3.1 `taskGone` helper (state-cache miss + warm = record gone); `populateRail` uses it, drops per-row `TaskAliveChecker` calls
- [x] 3.2 `applyArgusState` dead branch routed through the same helper semantics
- [x] 3.3 Doc comments: `roleEntry.Dead`, `roleArchived`, populateRail narration corrected

## 4. Verify

- [x] 4.1 `go test ./... -race -count=1` green
- [x] 4.2 `openspec validate --all --strict` green
