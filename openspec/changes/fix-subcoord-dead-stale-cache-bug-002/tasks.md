# Tasks

## 1. Implementation

- [x] 1.1 `ArgusStateCache` (`internal/view/taskstate.go`): add `lastSuccess time.Time`, `staleAfter time.Duration` (8× interval, floored at 15s), and an injectable `now func() time.Time` clock; stamp `lastSuccess = now()` on each successful poll; add `Fresh()` = ready AND `now()-lastSuccess <= staleAfter`.
- [x] 1.2 `managerPaneSource` (`internal/view/session.go`): add `StatesFresh()` delegating to `ArgusStateCache.Fresh()`.
- [x] 1.3 `taskGone` (`internal/view/app.go`): classify `Dead` only when the provider's `StatesFresh()` is true (in addition to the existing `StatesReady` gate) — a stale snapshot reports a miss as "unknown", not "gone".

## 2. Tests

- [x] 2.1 `TestBUG002_StaleCacheDoesNotClassifyLiveTaskDead` (`internal/view/bug002_stale_cache_dead_test.go`): a worker bound to a task absent from a READY-but-STALE cache MUST NOT be `Dead` — RED before the fix (Dead=true), GREEN after.
- [x] 2.2 `TestBUG002_FreshCacheStillBucketsGoneTask`: a fresh cache with no record for the task still buckets `Dead` (genuine prune unaffected).
- [x] 2.3 `TestArgusStateCache_Fresh_*` (`internal/view/taskstate_test.go`): fresh right after a successful poll; false before the first poll; false once the staleness window elapses with no fresh success (while `Ready()` stays true and the snapshot is still readable); tolerant of a brief blip within the window.
- [x] 2.4 `go test ./internal/view/` passes; `go build ./...` and `go vet` clean.
