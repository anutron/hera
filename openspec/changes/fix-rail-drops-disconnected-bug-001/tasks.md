# Tasks

## 1. Implementation

- [x] 1.1 `appendOrchChildren` (`internal/view/rail_list.go`): in the archived-roles branch, recurse into a bucketed sub-coordinator's `childOrch` one level deeper (mirroring the active branch — same cycle guard `seen` and `orchCollapsed` fold check), so a consumed child orchestrator's subtree renders nested beneath the parent-link row inside the Archive expando instead of dropping out of the rail.

## 2. Tests

- [x] 2.1 `TestBUG001_DisconnectedWorkerStaysReachable` (`internal/view/bug001_rail_drops_test.go`): a worker whose binding ENDED but whose argus record still exists (warm cache, non-archived) stays a reachable rail row — locks the already-fixed primary case (BUG-027 latest-binding fallback).
- [x] 2.2 `TestBUG001_SubCoordSubtreeReachableWhenParentLinkBucketed`: a sub-coordinator's grandchild whose record still exists stays REACHABLE when the parent-link row buckets (its coord task gone) — RED before the fix, GREEN after.
- [x] 2.3 `go test ./internal/view/...` passes (and `-race` on `internal/view`).
