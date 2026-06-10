# Tasks

## 1. Implementation

- [x] 1.1 Extend `OnReattach` (`internal/view/mutations.go`) with a mixed-coord orchestrator-header branch: when `selOrchestrator && !Archived && CoordArgusArchived && CoordTaskID != ""`, show the reattach splash, unarchive the coord task, then restart it.
- [x] 1.2 Gate the restart on a successful unarchive; on either failure, clear the splash and surface a human-readable error modal (no silent bounce).
- [x] 1.3 Leave the non-mixed coord header (enters pane normally) and hera-archived orchestrator (OnResurrect's job) paths returning `false`.

## 2. Tests

- [x] 2.1 Repair-then-reattach order: unarchive `T1` then restart `T1`, notify the reattach notifier, refresh the rail.
- [x] 2.2 Unarchive failure: restart does not run, notifier not called, error modal naming the coord.
- [x] 2.3 Restart failure after unarchive: error modal, notifier not called.
- [x] 2.4 Healthy coord header and hera-archived orchestrator: `OnReattach` returns `false`, no argus calls.
- [x] 2.5 `make test` passes with `-race`.
