# Fix ⊘ coord reattach showing a red error on the success path (BUG-022)

## Why

The BUG-019 repair-then-reattach path (Enter on a `⊘` mixed-coord header) now connects the coord successfully, but first flashes a popup with a long red error block, THEN connects. The error appears even when the reattach succeeds.

Root cause: argus's `/restart` endpoint reports a LIVE session as a failure code, and reattach treats that as terminal. Two situations produce it on the success path:

- Archiving an argus task does NOT stop its PTY — so a `⊘` coord's session is frequently still alive. After the repair unarchives the task, the restart hits a live session and argus answers `409 "task already running"`.
- The manual Enter reattach can race the focus-driven auto-reattach (`onDeadPaneReattach`), which fires its own `/restart` with no unarchive. One call spawns the session; the loser races past argus's non-atomic 409 guard and its `Start` returns a `500 "session already exists for task X"`.

In both cases a live session — exactly the reattach goal — already exists, yet hera surfaced the loser's HTTP error as a red modal while the pane connected via the winner.

## What Changes

- **`ReattachAgent` treats an already-live session as SUCCESS:** when argus reports `409 "task already running"` or the Start-race `500 "session already exists"`, `ops.ReattachAgent` returns nil instead of an error. The proxy subscription picks up the live session; no error modal flashes.
- **Terminal failures still surface:** worktree-missing (offer-delete), restart-not-supported (update-argus), and genuine start-failure 500s are unaffected — worktree-missing is checked before the already-running tolerance, and the already-exists 500 is matched by body marker, not status alone, so a real start failure still modals.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the mixed-coord reattach requirement — a reattach that finds the session already live is a success (no error modal), not a terminal failure.

## Impact

- `internal/argus/errors.go` — new `IsAlreadyRunning` helper (409, or 500 carrying the `session already exists` marker).
- `internal/view/ops/reattach.go` — `ReattachAgent` returns nil on an already-live session, after the worktree-missing check.
- `internal/argus/errors_test.go`, `internal/view/ops/reattach_test.go` — coverage for the 409 / race-500 success cases and the genuine-500 propagation.
