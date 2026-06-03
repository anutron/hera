# Design: add-mouse-wheel-scrollback

## Context

Hera-view renders a rail plus two terminal panes (coord, agent) inside an argus plugin view. Input arrives as raw bytes over the plugin WebSocket: argus encodes each keystroke exactly as a real terminal would (shared `keyenc`), and hera's `rawInputConn` routes whole binary frames by focus state — RAIL frames go to the tcell parser, pane frames are forwarded verbatim to the bound task's PTY, except view-owned chords (`isHeraChord`).

Three gaps prevent scrolling into pane history:

1. **argus** (`internal/tui/terminalpane`, the plugin-view pane — not the task terminal): no `MouseHandler`. Wheel events over the hera surface are dropped by tview. Mouse is already enabled app-wide (`app.go:690`).
2. **argus-sdk** (`terminalpane`): paints only the live emulator screen. The x/vt `SafeEmulator` already retains scrollback (10k-line default) and exposes `ScrollbackLen()` / `ScrollbackCellAt(x, y)`, but no paint path uses them.
3. **hera** (`pinned_pane.go`): `scrollOffset` is tracked on ⇧↑/⇧↓ but never rendered — the documented D15 limitation. The base spec already requires visible scrolling ("Shift-arrows scroll the focused pane"), so this is spec/code drift, not just a missing feature.

Reference implementation: argus's own task terminal (`internal/tui/terminal`) scrolls locally on wheel (`mouseScrollStep = 3`), shows a `[SCROLL]` badge, and anchor-locks content while new output streams in. Per the project's UI target, hera mirrors argus behavior rather than improvising.

## Goals / Non-Goals

**Goals:**

- Mouse wheel over the agent or coord pane scrolls that pane's history; wheel back down returns to live.
- Mouse wheel over the rail pans the rail viewport when it overflows, without moving the selection.
- ⇧↑/⇧↓ scrolling becomes visible (closes the D15 drift) and shares one scroll engine with the wheel.
- Scroll-mode UX mirrors argus's task terminal: `[SCROLL]` badge, anchor-lock while output streams, reset to live on pane rebind.
- New plugin-generic logic (scroll engine, paint-at-offset, SGR mouse decode) lands in argus-sdk, not hera.

**Non-Goals:**

- Click-to-focus, text selection, motion/drag handling (clicks and motion are swallowed; the argus encoder may forward them later with no hera change required).
- Forwarding mouse to the inner agent PTY when the inner app enables mouse reporting (argus's own pane doesn't; revisit only if an inner TUI demands it).
- Migrating existing hera pane machinery (redraw coalescer, pinned-pane resize negotiation, raw-input pattern) into argus-sdk — backlogged separately.
- Rail wheel acting as selection movement (would rebind panes per tick; rejected).

## Decisions

### D1: Transport = SGR mouse bytes on the existing binary input channel

Argus's plugin pane encodes wheel ticks exactly as xterm would report them to a mouse-enabled app: `ESC [ < 64|65 ; x ; y M` (64 = wheel-up, 65 = wheel-down; pane-relative 1-based coordinates). Sent down the existing `inputBack` channel → WS binary frame, one event per frame.

- **Why:** argus already behaves like a terminal toward plugins (shared keyenc, raw byte frames); zero new protocol surface; plugins that ignore mouse see nothing; any future plugin gets mouse for free.
- **Alternatives considered:** JSON wheel envelope (new protocol type for no gain); enabling tview mouse inside hera and letting tcell parse (depends on mouse-mode negotiation on a fake tty that never happens; the raw-frame interception path is already proven by `isHeraChord`).

### D2: Hera intercepts mouse frames before the focus fork, unconditionally

`rawInputConn.Read` checks each binary frame for an SGR mouse sequence FIRST — before the RAIL pass-to-parser and before the pane forward-to-PTY. Mouse frames are never forwarded to a PTY (escape garbage into Claude Code) and never fed to the tcell parser (garbage keystrokes in RAIL focus). Frame == exactly one SGR sequence (argus sends one frame per event), so detection is frame-aligned like `isHeraChord` — no streaming parser.

Wheel events dispatch to the tview event loop via `QueueUpdateDraw`; the read goroutine must not touch tview state (same discipline as the focus mirror). On the event loop, the handler hit-tests `(x-1, y-1)` against `GetRect()` of the rail, coord pane, and agent pane and scrolls the widget under the cursor. Hit-testing live rects (not recomputed layout math) keeps freelance mode (no coord pane) and future layout changes correct by construction. Non-wheel mouse sequences (clicks, motion, release) are swallowed.

### D3: Scroll engine and rendering live in argus-sdk `terminalpane`

- Scroll state: `ScrollBy(±n)`, `ScrollOffset()`, `ResetScroll()`; effective offset clamped to `[0, emu.ScrollbackLen()]`.
- Paint at offset: visible window = the last `offset` scrollback lines stacked above the live screen, windowed to the surface height — i.e. rows `[total − offset − h, total − offset)` of the combined scrollback+live buffer, fetched via `ScrollbackCellAt` / `CellAt`.
- Anchor-lock: the pane records the scrollback length when the offset is set; as new lines are pushed to scrollback, the effective offset grows by the delta so the content being read holds still. Scrolling down past zero resets to live.
- `[SCROLL]` badge painted on the pane's top row whenever offset > 0, mirroring argus's task terminal.
- SGR mouse decoder helper (sequence → {wheel-up/down, x, y} or not-mouse) ships in the SDK so hera and future plugins share one parser with the encoder's format.
- **Why SDK:** the pane widget is the thing being scrolled; hera contributes only routing. Mirrors the project rule that plugin-generic widgets live in the SDK.
- **Alternative considered:** argus's replay-emulator architecture (rebuild a 50k-line emulator from the session log). Far heavier; hera's panes already hold a live emulator with adequate scrollback for the session's stream. Not needed.

### D4: Hera's `pinnedTerminalPane` delegates scroll to the SDK

The local tracked-only `scrollOffset` field and its `ScrollBy`/`ScrollOffset` methods are deleted; `KeyRouter`'s `PaneScroller` path (⇧↑/⇧↓, 1 line/press) and the new wheel path (3 lines/tick, matching argus `mouseScrollStep`) both call the embedded SDK pane. Rebinding a pane to a different task calls `ResetScroll()` so a fresh task starts at the live tail.

### D5: Rail wheel pans the viewport; cursor-follow snap relaxes

Today `railList.Draw` snaps `offset` to keep the cursor visible on every draw, which would instantly undo wheel panning. The snap becomes conditional: only when the cursor has moved since the last draw (tracked via a `lastCursor` field). Wheel adjusts `offset` directly, clamped to `[0, max(0, rows − h)]`; the selection — and therefore pane binding — does not move. j/k re-snap the viewport as today.

- **Alternative considered:** wheel moves the selection (like j/k). Rejected: every few ticks would rebind both panes through the select debounce — heavy side effects while casually scrolling.

## Risks / Trade-offs

- [Anchor-lock drift if x/vt trims scrollback at capacity (oldest lines dropped while scrolled)] → clamp the effective offset to `ScrollbackLen()` on every paint; worst case the view slides, never panics or blanks.
- [argus task forwards wheel only after its PR merges and the running argus is rebuilt — until then no end-to-end mouse path] → keyboard path (⇧↑/⇧↓) is fully functional from SDK+hera alone; hera's interception is inert without inbound frames.
- [SDK tag v0.0.3 must exist before hera builds] → execution order gates hera's go.mod bump on the tag; the hera-side routing work that doesn't touch the SDK API can proceed in parallel against a stubbed decoder only if needed (prefer waiting; the SDK task is small).
- [Stale local argus-sdk clone (initial-commit README only) — a spawned worktree would branch from it] → the SDK worker's first action is `iris_fetch` then `git reset --hard origin/main` in its worktree before any work.
- [tcell parser receiving a partial/unknown mouse variant if argus ever batches frames] → detection requires the full frame to match the SGR grammar; non-matching frames follow the existing focus routing unchanged (fail-open to today's behavior).
- [Rail snap relaxation regresses j/k visibility] → covered by explicit tests: cursor-move still snaps; wheel-pan persists across redraws (DAO refresh ticks repaint the rail frequently).

## Migration Plan

1. **argus-sdk worker task** (`ARGUS-SDK` project): scroll engine + paint-at-offset + anchor-lock + badge + SGR decoder + tests → merge to main → tag `v0.0.3`.
2. **argus worker task** (`ARGUS` project, gated on user confirmation): plugin-pane MouseHandler encoding wheel → SGR bytes → PR → merge → rebuild the running argus.
3. **hera (this change):** bump argus-sdk to v0.0.3; implement interception, hit-testing, pane delegation, rail panning; spec deltas + tests; PR.

Rollback: each repo's change is independently revertable; hera pinned to v0.0.2 simply keeps today's behavior (offset tracked, not painted). No data or schema impact.

## Open Questions

- Should the argus MouseHandler also forward left-clicks now (hera swallows them) so a future click-to-focus needs no argus release? Cheap; default = wheel-only unless the user opts in.

## Acceptance criteria

**Pane wheel scrolling (hera + sdk + argus):**

- it should scroll the agent pane's history up 3 lines per wheel-up tick when the cursor is over the agent pane
- it should scroll the coord pane (not the agent pane, not the rail) when the cursor is over the coord pane
- it should return the pane to the live screen when wheel-down reaches the newest content
- it should never forward mouse bytes to the bound task's PTY
- it should never emit mouse bytes to the tcell parser in any focus state

**Scroll-mode rendering (sdk):**

- it should paint history lines (not the live screen) when the scroll offset is non-zero
- it should show a [SCROLL] badge on the pane's top row while scrolled and remove it at offset 0
- it should hold the viewed content still (anchor-lock) while new output streams in
- it should clamp scrolling at the oldest retained line and at the live screen
- it should reset to the live screen when the pane is rebound to a different task

**Keyboard parity (hera):**

- it should scroll the focused pane visibly on ⇧↑/⇧↓ through the same engine (1 line per press)

**Rail viewport panning (hera):**

- it should pan the rail viewport on wheel without moving the selection or rebinding panes
- it should not pan when the rail content fits the viewport
- it should re-snap the viewport to the cursor when the selection moves (j/k) after a wheel pan
- it should preserve a wheel-panned viewport across rail refresh repaints
