# Tasks

## 1. Implementation

- [x] 1.1 Add `App.reflowCoordInPlace(taskID, cols, rows)` (`internal/view/app.go`): under `a.mu`, no-op when closed / coord task changed / no coord pane, else resize the live coord emulator in place (`a.pieces.coord.Resize`). Preserves in-progress input; does NOT re-subscribe.
- [x] 1.2 Add `App.reflowAgentInPlace(taskID, cols, rows)`: AGENT-slot counterpart.
- [x] 1.3 Route `makeCoordReflowCallback` / `makeAgentReflowCallback` to `reflowCoordInPlace` / `reflowAgentInPlace` instead of `forceRebindCoord` / `forceRebindAgent`.
- [x] 1.4 Leave `forceRebindCoord` / `forceRebindAgent` unchanged; they remain the reattach-path rebuild (`clearReattachAndResize`), where a fresh subscription is intended after `ResetSubscription`.

## 2. Tests

- [x] 2.1 `TestRebindCoord_PreservesInProgressInputAcrossNavAwayAndBack`: coord nav A→B→A preserves live-channel-only input (mirror of the #150 worker test) — confirms the park/restore slot already covers coords.
- [x] 2.2 `TestReflowCoordInPlace_PreservesInProgressInputOnResize`: a resize reflow of a live coord pane preserves input typed only on the live channel (the operator's reported wipe).
- [x] 2.3 `TestReflowAgentInPlace_PreservesInProgressInputOnResize`: agent-slot symmetry.
- [x] 2.4 Existing reflow-callback and reattach (`forceRebind*`) tests stay green; `go test ./internal/view/...` passes with `-race`.
