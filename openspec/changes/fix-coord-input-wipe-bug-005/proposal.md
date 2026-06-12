# Preserve coordinator-pane in-progress input across nav and resize (BUG-005)

## Why

BUG-033 (#150) fixed in-progress, unsubmitted input being wiped when the operator navigated away from a live pane and back: it parks the live pane on nav-away (keyed by argus task id) and restores it on nav-back, reusing the emulator that already consumed the input off the live PTY channel instead of replaying a lossy ring-buffer snapshot through a fresh emulator. That fix was wired into both the AGENT and COORD rebind slots.

The operator still lost typed-but-unsent input in a COORDINATOR pane. The gap: #150 covered the cross-task NAV rebuild, but NOT the resize-triggered REFLOW rebuild. A coordinator pane renders FULL-WIDTH in coordinator mode and HALF-WIDTH in the coord+agent split. Drilling from a full-width coordinator into one of its own workers (and back) changes the coord pane's width, which fires the pane's `onReflow` callback. That callback rebuilt the emulator from the ring snapshot (`forceRebindCoord`) to re-wrap scrollback at the new width (BUG-038) — the exact lossy/racy replay BUG-033 identified, dropping the in-progress input line.

Worker↔worker navigation does not resize the agent pane (both render in the same split), so the agent slot rarely hits this path — which is why the wipe presented as coordinator-specific even though the latent gap existed on both slots.

## What Changes

- **The resize-reflow path now resizes the live emulator IN PLACE instead of rebuilding it from the ring snapshot.** New `App.reflowCoordInPlace` / `App.reflowAgentInPlace` resize the existing live `terminalpane` emulator to the new dimensions, preserving its on-screen state — including in-progress, unsubmitted input. The coord/agent reflow callbacks (`makeCoordReflowCallback` / `makeAgentReflowCallback`) route to these instead of `forceRebindCoord` / `forceRebindAgent`.
- **`forceRebindCoord` / `forceRebindAgent` are unchanged and still used by the REATTACH path** (`clearReattachAndResize`), where a fresh-subscription rebuild IS intended: `ResetSubscription` has already discarded the old session's ring buffer, and there is no in-progress input to protect.
- **Trade-off, deliberately accepted:** the SDK emulator's in-place `Resize` reflows the ACTIVE screen but not scrollback HISTORY, so lines already scrolled off keep their prior wrapping after a resize (visible only when the operator scrolls up after resizing). Never losing operator input outweighs perfect scrollback re-wrapping — and the prior wrapping self-heals as new output pushes those lines out of scrollback.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the in-progress-input preservation guarantee (introduced by BUG-033 for the nav path) is extended to the pane-RESIZE reflow path, for both the coordinator and agent slots.

## Impact

- `internal/view/app.go` — new `reflowCoordInPlace` / `reflowAgentInPlace`; `makeCoordReflowCallback` / `makeAgentReflowCallback` route to them instead of `forceRebind*`. `forceRebind*` retained for the reattach path.
- `internal/view/app_test.go` — coord nav-away-and-back preservation test (mirror of the #150 worker test), plus coord and agent resize-reflow in-place preservation tests.
