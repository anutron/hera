# hera-view — delta for add-mouse-wheel-scrollback

## MODIFIED Requirements

### Requirement: Pane focus forwards keystrokes to the bound task's PTY input

The system SHALL forward keystrokes received while focus is `COORD` or `AGENT` to the bound task's input endpoint (`POST /api/tasks/{id}/input`) verbatim. Keystrokes that match the focus-traversal bindings (Cmd/Ctrl-←/→, Ctrl-Q) MUST be intercepted by the view application and MUST NOT be forwarded.

"Verbatim" means the **raw input bytes** received from the host over the WebSocket MUST be forwarded to the PTY unchanged — they MUST NOT be round-tripped through the view's tcell event parser and re-encoded. The host (argus) has already encoded each keystroke into the byte sequence a real terminal would produce; the view forwards those bytes as-is, preserving full terminal fidelity for keys a tcell re-parse would mangle or swallow — notably Shift+Enter / Alt+Enter (newline-insert, `ESC CR`), Alt+Backspace (word-delete, `ESC DEL`), and Alt+arrows / word-motion sequences. The view therefore routes inbound input by focus state at the WebSocket transport boundary, BEFORE the tcell parser: while focus is in a pane the raw bytes are forwarded to the bound task and MUST NOT additionally drive the parser (no double handling); while focus is `RAIL` the bytes are fed to the tcell parser unchanged so rail navigation and mutation keys work as specified.

The focus-traversal and in-pane control chords the view owns — Ctrl-Q (escape to RAIL), Cmd/Ctrl-←/→ (focus ladder), Cmd/Ctrl-↑/↓ (in-pane navigation), and Shift-↑/↓ (pane scrollback) — MUST continue to be recognized even while focus is in a pane: their byte sequences are fed to the parser (and intercepted by the view) rather than forwarded to the PTY. These chord sequences are modifier-distinguished from forwardable content (e.g. Alt-modified word-motion and ESC-prefixed sequences), so passthrough content never collides with a chord.

SGR mouse-report frames (`ESC [ < button ; x ; y M|m`, as encoded by the host's plugin-pane mouse forwarding) are view-owned in EVERY focus state and MUST be peeled off BEFORE the focus-routing fork: they MUST NOT be forwarded to any task's input endpoint (they are not typed content) and MUST NOT be fed to the view's tcell parser (where they would parse as garbage keystrokes in `RAIL` focus). A frame is treated as a mouse frame only when the entire frame matches the SGR mouse grammar; non-matching frames follow the focus routing above unchanged. Wheel events (button 64/65) are dispatched to the view's scroll routing (see "Mouse wheel scrolls the widget under the cursor"); all other mouse events (clicks, motion, release) are swallowed.

Forwarding MUST work regardless of HOW the pane gained focus: stepping in via the arrow ladder (`Ctrl-→`) and entering the selection's primary pane via `Enter` from `RAIL` MUST both leave the focused pane bound to a task such that the next typed key is forwarded to that task. After `Enter` enters a worker/freelancer row's AGENT pane, the AGENT pane's bound task target MUST be the selected row's argus task, so the next keystroke reaches it.

When a forward `POST /api/tasks/{id}/input` fails (argus unreachable, task gone, endpoint unsupported, or auth rejected), the system MUST NOT silently discard the failure: it MUST log a warning carrying the target task id and the underlying error, so a keystroke that never reaches a pane's PTY is diagnosable rather than invisible. A successful forward MUST NOT emit that warning. When focus is in a pane but no task is bound to it, the keystroke is dropped (there is nowhere to send it) and the drop SHOULD be logged at debug level.

Keystroke forwarding MUST NOT block the UI event loop. The view application resolves the target task at key-press time and hands the byte(s) to an asynchronous, ordered sender; the key handler MUST return without waiting for the `POST /api/tasks/{id}/input` round-trip. The sender MUST preserve byte/key order: bytes enqueued for the same task MUST be POSTed in the order they were typed. The sender MAY coalesce consecutive bytes destined for the SAME task into a single `POST /api/tasks/{id}/input` (concatenated in order) to cut round-trips for fast typing or paste; it MUST NOT coalesce across different task targets, so a focus change mid-stream still delivers the earlier bytes to the earlier task before the new target's bytes go out. The asynchronous sender MUST be tied to the session lifecycle and MUST stop cleanly on session teardown / context cancellation without leaking a goroutine.

#### Scenario: Typed key forwarded to COORD task

- **WHEN** focus is `COORD` and the operator types a single character `x`
- **THEN** the daemon MUST issue a `POST /api/tasks/{coord_task_id}/input` carrying the byte `x` AND the byte MUST NOT be rendered locally in the COORD pane (the byte is rendered when the source PTY echoes it back via SSE)

#### Scenario: Enter into an agent pane then type forwards to that agent

- **WHEN** the operator selects a worker/agent row in `RAIL`, presses `Enter` to enter its AGENT pane, then types a single character `Z`
- **THEN** focus MUST be `AGENT` AND the daemon MUST issue a `POST /api/tasks/{worker_task_id}/input` carrying the byte `Z` to that agent's bound task

#### Scenario: Raw multi-byte sequence forwarded verbatim from a pane

- **WHEN** focus is `AGENT`, a task is bound, and the host delivers the raw input byte sequence `ESC DEL` (`0x1b 0x7f`, Alt+Backspace / word-delete) over the WebSocket
- **THEN** the daemon MUST forward exactly the bytes `0x1b 0x7f` to that task's `POST /api/tasks/{id}/input` (the sequence MUST NOT be reduced, re-encoded, or split by a tcell re-parse)

#### Scenario: Bare Enter in a pane forwards CR

- **WHEN** focus is in a pane, a task is bound, and the host delivers the raw byte `CR` (`0x0d`)
- **THEN** the daemon MUST forward the byte `0x0d` to the bound task's input endpoint (so the agent's line discipline submits)

#### Scenario: Rail input is parsed, not forwarded

- **WHEN** focus is `RAIL` and the host delivers input bytes
- **THEN** the bytes MUST be fed to the view's key parser for rail navigation / mutation AND MUST NOT be forwarded to any task's input endpoint

#### Scenario: In-pane control chord navigates and is not forwarded

- **WHEN** focus is in a pane and the host delivers a view-owned control chord byte sequence (e.g. Ctrl-Q `0x11`, or the in-pane navigation chord `ESC [ 1 ; 5 A`)
- **THEN** the chord MUST be handled by the view (focus returns to `RAIL`, or the in-pane selection moves) AND its bytes MUST NOT be forwarded to the bound task's input endpoint

#### Scenario: Mouse frame in pane focus is not forwarded to the PTY

- **WHEN** focus is `AGENT`, a task is bound, and the host delivers a binary frame containing exactly an SGR wheel sequence (e.g. `ESC [ < 64 ; 50 ; 10 M`)
- **THEN** no `POST /api/tasks/.../input` MUST be issued for that frame AND the frame MUST be routed to the view's scroll handling

#### Scenario: Mouse frame in RAIL focus is not fed to the key parser

- **WHEN** focus is `RAIL` and the host delivers a binary frame containing exactly an SGR mouse sequence
- **THEN** the frame MUST NOT drive the tcell parser (no rail navigation or mutation key may fire from its bytes) AND wheel sequences MUST be routed to the view's scroll handling

#### Scenario: Non-wheel mouse events are swallowed

- **WHEN** the host delivers an SGR mouse frame whose button is not a wheel direction (e.g. a left-click press `ESC [ < 0 ; 5 ; 5 M` or its release `...m`)
- **THEN** the view MUST swallow the frame: no PTY forward, no parser dispatch, no scroll action

#### Scenario: Failed forward is logged, not swallowed

- **WHEN** focus is `AGENT`, a task is bound, the operator types a character, and the `POST /api/tasks/{id}/input` call returns an error
- **THEN** the system MUST log a warning identifying the bound task id and the error AND MUST NOT crash or block the input pump

#### Scenario: Focus-traversal key not forwarded

- **WHEN** focus is `AGENT` and the operator presses `Ctrl-Q`
- **THEN** focus MUST transition to `RAIL` AND no `POST /api/tasks/.../input` MUST be issued for that key event

#### Scenario: Forwarding does not block the input event loop

- **WHEN** focus is in a pane, a task is bound, the operator types a key, and the `POST /api/tasks/{id}/input` round-trip is slow
- **THEN** the key handler MUST return immediately (it MUST NOT wait for the round-trip) AND the byte(s) MUST be delivered to the bound task by the asynchronous sender on its own goroutine

#### Scenario: Typed bytes are forwarded in order

- **WHEN** focus is in a pane bound to one task and the operator types `A`, then `B`, then `C` in rapid succession
- **THEN** the bytes MUST reach that task's `POST /api/tasks/{id}/input` in the order `A`, `B`, `C` (key order is never reordered)

#### Scenario: Consecutive same-target bytes may coalesce into one POST

- **WHEN** several bytes for the SAME bound task pile up faster than the sender can deliver them
- **THEN** the sender MAY batch the consecutive same-target bytes into a single `POST /api/tasks/{id}/input` with the bytes concatenated in typed order (fewer POSTs than bytes)
- **AND WHEN** the queued bytes target two DIFFERENT tasks (a focus change occurred mid-stream)
- **THEN** the sender MUST NOT merge them into one POST: the earlier task's bytes go out in their own `POST` before the new target's bytes

## ADDED Requirements

### Requirement: Mouse wheel scrolls the widget under the cursor

The system SHALL route SGR wheel events (button 64 = up, 65 = down; 1-based viewport coordinates) by POSITION, not focus: on the tview event loop the view hit-tests the event's `(x−1, y−1)` cell against the live rects of the rail, coord pane, and agent pane, and scrolls the widget containing that cell. Wheel-over-pane scrolls that pane's terminal scrollback by 3 lines per tick (mirroring argus's task-terminal `mouseScrollStep`), regardless of which widget holds keyboard focus and without moving keyboard focus or the rail selection. Hit-testing MUST use the widgets' actual layout rects so body-mode variations (e.g. freelance mode without a coord pane) are handled by construction; a wheel event over none of the widgets (top bar, gaps) is swallowed. The dispatch from the WebSocket read goroutine to the event loop MUST NOT block the read goroutine.

#### Scenario: Wheel over the agent pane scrolls it 3 lines per tick

- **WHEN** the host delivers a wheel-up frame whose coordinates fall inside the agent pane's rect
- **THEN** the agent pane's scrollback offset MUST increase by 3 lines AND the rail selection and keyboard focus MUST NOT change

#### Scenario: Wheel routes by position, not focus

- **WHEN** focus is `AGENT` and a wheel-up frame's coordinates fall inside the COORD pane's rect
- **THEN** the COORD pane MUST scroll AND the agent pane's offset MUST NOT change

#### Scenario: Wheel outside every widget is swallowed

- **WHEN** a wheel frame's coordinates fall on the top bar (outside the rail and both panes)
- **THEN** no widget may scroll AND the frame MUST NOT be forwarded or parsed

### Requirement: Scrolled panes render history with argus-equivalent scroll-mode UX

When a pane's scrollback offset is non-zero the pane MUST visibly render history: the visible window is composed of the last `offset` scrollback lines stacked above the live screen, windowed to the pane's surface (the same content a real terminal shows mid-scroll). The offset is clamped to `[0, scrollback length]` — scrolling cannot pass the oldest retained line, and scrolling down past zero returns the pane to the live screen. While scrolled, a `[SCROLL]` badge MUST be painted on the pane's top row and removed at offset 0. While scrolled, newly arriving output MUST NOT shift the viewed content: the effective offset grows as lines are pushed to scrollback (anchor-lock), so the operator keeps reading the same lines; if the scrollback buffer trims at capacity the offset re-clamps rather than erroring. The keyboard scroll keys (⇧↑/⇧↓, 1 line per press) and the wheel (3 lines per tick) MUST drive this same engine, so both inputs produce identical rendering. Rebinding a pane to a different task MUST reset its offset to the live screen.

#### Scenario: Non-zero offset paints history, not the live screen

- **WHEN** a pane bound to a task with scrollback history is scrolled up by N lines
- **THEN** the pane MUST paint the history window ending N lines above the live tail (content that matches the emulator's scrollback), not the live screen

#### Scenario: SCROLL badge appears while scrolled and clears at live

- **WHEN** a pane's offset becomes non-zero
- **THEN** a `[SCROLL]` badge MUST render on the pane's top row
- **AND WHEN** the offset returns to 0
- **THEN** the badge MUST disappear and the live screen MUST paint

#### Scenario: Anchor-lock holds content still under new output

- **WHEN** a pane is scrolled into history and the bound task emits new output lines
- **THEN** the lines visible to the operator MUST remain the same lines (the effective offset grows by the number of newly retained scrollback lines)

#### Scenario: Scrolling clamps at both ends

- **WHEN** the operator scrolls up past the oldest retained line, or scrolls down past the newest content
- **THEN** the offset MUST clamp at the scrollback length (top) or at 0 (bottom, live screen) without error or blank frames

#### Scenario: Shift-arrows produce visible scrolling through the same engine

- **WHEN** focus is in a pane with scrollback history and the operator presses ⇧↑
- **THEN** the pane MUST visibly scroll up one line (the same rendering path as the wheel), without the rail selection moving

#### Scenario: Rebinding a pane resets to live

- **WHEN** a pane scrolled into history is rebound to a different task (rail selection change)
- **THEN** the new task's pane MUST render at the live screen with offset 0

### Requirement: Mouse wheel pans the rail viewport without moving the selection

When rail content overflows the rail's height, wheel events over the rail SHALL pan the rail's viewport offset (3 rows per tick) without changing the selected row, and therefore without rebinding any pane. The offset clamps to `[0, max(0, rowCount − viewportHeight)]`; when the content fits, wheel events over the rail are no-ops. The rail's existing snap-the-viewport-to-the-cursor behavior MUST apply only when the cursor (selection) has moved since the last draw — so a wheel-panned viewport persists across redraws (including rail refresh repaints from DAO writes) until the next selection movement (j/k, in-pane nav), which re-snaps the viewport to the cursor as today.

#### Scenario: Wheel over the rail pans without selecting

- **WHEN** the rail overflows its height and a wheel-down frame's coordinates fall inside the rail
- **THEN** the rail viewport offset MUST increase (later rows become visible) AND the selected row and both pane bindings MUST NOT change

#### Scenario: No panning when content fits

- **WHEN** the rail's rows all fit within its height and a wheel frame lands on the rail
- **THEN** the viewport offset MUST remain 0

#### Scenario: Wheel pan persists across a rail refresh

- **WHEN** the rail viewport has been wheel-panned away from the cursor and a rail refresh repaint fires (no cursor movement)
- **THEN** the viewport offset MUST remain where the wheel left it (no snap back to the cursor)

#### Scenario: Selection movement re-snaps the viewport

- **WHEN** the rail viewport has been wheel-panned away from the cursor and the operator presses `j`
- **THEN** the viewport MUST snap so the (moved) cursor row is visible, restoring the pre-existing follow behavior
