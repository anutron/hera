# Proposal: add-mouse-wheel-scrollback

## Why

The agent and coord panes cannot scroll into history: the SDK terminal pane paints only the live emulator screen, so the existing ⇧↑/⇧↓ scroll requirement (base spec: "Status, open-PR, scroll, and in-pane navigation keys") is satisfied in plumbing but invisible in practice — the offset is tracked and never painted. Mouse wheel input never reaches hera at all: argus's plugin-view pane has no MouseHandler, so wheel events over the hera surface die in argus's tview layer. Operators expect to wheel up into an agent's output the way they do in argus's own task terminal.

## What Changes

- **argus-sdk (`anutron/argus-sdk`, new tag v0.0.3)** — the `terminalpane` widget gains a real scroll engine: `ScrollBy` / `ScrollOffset` / `ResetScroll` clamped to the emulator's scrollback length, paint-at-offset rendering composed from scrollback + live screen, anchor-lock (content holds still as new output arrives), a `[SCROLL]` badge while scrolled, and an SGR mouse-sequence decoder helper for plugins.
- **argus (separate repo, gated on user confirmation)** — the plugin-view pane (`internal/tui/terminalpane`) gets a MouseHandler that encodes wheel ticks as standard SGR mouse bytes (`ESC[<64|65;x;yM`, pane-relative 1-based coordinates) onto the existing binary input channel.
- **hera (this repo)** — `rawInputConn` peels SGR mouse frames off before the focus fork (they are never forwarded to a PTY and never fed to the tcell parser), hit-tests the coordinates against the rail/coord/agent rects on the event loop, and scrolls the widget under the cursor: panes scroll history via the SDK engine (wheel = 3 lines/tick), the rail pans its viewport without moving the selection. `pinnedTerminalPane`'s tracked-but-invisible `scrollOffset` is deleted in favor of the SDK engine, making ⇧↑/⇧↓ visibly scroll (closing the existing spec/code drift). Rail viewport snap-to-cursor relaxes to fire only when the cursor moves. go.mod bumps argus-sdk to v0.0.3.

The keyboard path (⇧↑/⇧↓) becomes fully functional with only the argus-sdk + hera changes; the mouse path additionally requires the argus change.

## Capabilities

### New Capabilities

(none — all behavior lands in the existing `hera-view` capability)

### Modified Capabilities

- `hera-view`: the ⇧↑/⇧↓ scroll requirement gains visible-rendering, anchor-lock, badge, and reset-on-rebind semantics; the raw-input forwarding requirement gains mouse-frame interception (never forwarded, never parsed); new requirements cover wheel-over-pane scrolling, wheel-over-rail viewport panning, and scroll-mode rendering.

## Impact

- **Code:** `internal/view/raw_input.go` (mouse frame detection), `internal/view/app.go` (event-loop scroll dispatch + hit-testing), `internal/view/pinned_pane.go` (delegate scroll to SDK), `internal/view/rail_list.go` (viewport/cursor decoupling, wheel panning), `internal/view/keys.go` (⇧↑/⇧↓ delegate to the same engine), `go.mod` (argus-sdk v0.0.3).
- **Dependencies:** argus-sdk v0.0.3 must exist before hera builds (worker task spawned for it); argus MouseHandler must ship before wheel events flow end-to-end (keyboard path works without it).
- **Cross-repo coordination:** argus-sdk and argus changes are executed as sandboxed argus tasks (`ARGUS-SDK`, `ARGUS` projects) specified from this change's design doc.
