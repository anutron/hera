# hera-view Specification

## Purpose
TBD - created by archiving change add-hera-view. Update Purpose after archive.
## Requirements
### Requirement: Plugin view registered with argus on daemon startup

The system SHALL register a single argus plugin view when the hera daemon starts, via `POST /api/plugins/views`. The registration MUST include a non-empty `title`, a `hotkey` (default tentative `Ctrl-H`; final choice flagged as an open question), and a `callback_url` pointing at hera's in-process WebSocket route (`ws://127.0.0.1:7744/view`). The registration MUST use hera's existing scope token. The registration MUST be heartbeated every 5 minutes (matching the existing MCP-tool registrar shape) to stay within argus's idle sweep. On graceful shutdown (SIGINT/SIGTERM), hera MUST delete the registration via `DELETE /api/plugins/views/{id}` before exiting.

#### Scenario: Plugin view registered on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** an HTTP GET against argus's plugin-views registry MUST show exactly one plugin view owned by hera with `callback_url` pointing at `/view` on hera's own HTTP listener

#### Scenario: Plugin view heartbeated

- **WHEN** the hera daemon has been running for more than 5 minutes
- **THEN** hera MUST have re-POSTed the plugin-view registration at least once since startup

#### Scenario: Plugin view unregistered on shutdown

- **WHEN** the hera daemon receives SIGTERM and shuts down cleanly
- **THEN** hera MUST issue a DELETE for the plugin-view registration before the process exits

### Requirement: View server lives in the daemon process

The system SHALL serve the plugin-view WebSocket from the same HTTP listener as the existing MCP callback (`127.0.0.1:7744`). The view MUST NOT require a separate process or binary; the daemon owns the route directly.

#### Scenario: View served on the existing MCP listener

- **WHEN** an HTTP client opens a WebSocket against `ws://127.0.0.1:7744/view`
- **THEN** the hera daemon process MUST handle the upgrade in-process; no separate `hera-view` binary or subprocess MUST be required

### Requirement: WebSocket upgrade at /view with last-writer-wins reconnection

The system SHALL accept WebSocket upgrades at the `GET /view` route. The protocol mirrors argus's plugin-view contract: binary frames carry ANSI bytes (server → client) and keystroke bytes (client → server); text frames carry JSON control envelopes for `resize`, `focus`, and `blur`. When a new WebSocket connection arrives while a prior one is still open, the prior connection MUST be closed; only one active session per daemon is supported.

#### Scenario: WebSocket upgrade accepted

- **WHEN** an HTTP client sends a valid WebSocket upgrade request to `/view`
- **THEN** the hera daemon MUST complete the upgrade and start a per-connection rendering goroutine

#### Scenario: Second connection closes the first

- **WHEN** a second WebSocket upgrade arrives at `/view` while a prior connection is still open
- **THEN** the daemon MUST close the prior connection before continuing to serve the new one

### Requirement: PTY proxy pre-loads snapshot and SSE per live binding at daemon startup

The system SHALL, at daemon startup, open a snapshot fetch (`GET /api/tasks/{id}/output`) followed by a live SSE subscription (`GET /api/tasks/{id}/stream?since=<X-Output-Total>`) for every live binding present at startup. For bindings created after startup, the snapshot+SSE MAY be opened lazily on first pane selection rather than proactively. Bytes from each task MUST be appended to a per-task in-memory ring buffer with a cap of approximately 256 KiB. The ring buffer MUST drop the oldest bytes when full. If an SSE stream drops on a transient error, the proxy SHALL reconnect with bounded backoff and resume from its last byte cursor. Rail navigation between agents MUST swap pane subscriptions to the relevant ring buffer in-process without triggering a new output-snapshot or SSE-stream round-trip.

#### Scenario: Snapshot and SSE opened per live binding at startup

- **WHEN** the hera daemon completes startup with N live bindings present in the DB
- **THEN** the PTY proxy MUST have issued exactly N snapshot fetches and N SSE subscriptions, one per binding

#### Scenario: Rail navigation swaps without a network call

- **WHEN** the operator moves rail selection from agent A to agent B and both bindings are already pre-loaded
- **THEN** the panes MUST render agent B's coord and agent B's PTY from in-memory ring buffers without issuing a new output-snapshot or SSE-stream request (a one-time `POST /api/tasks/{id}/size` to align the source PTY to the pane allocation is permitted)

#### Scenario: Ring buffer bounded at ~256 KiB

- **WHEN** an SSE stream has emitted more than 256 KiB of output for a single task
- **THEN** the ring buffer for that task MUST retain only approximately the most recent 256 KiB; older bytes MUST be dropped

### Requirement: Body layout adapts to the selected row's kind

The system SHALL render the body inside top and bottom chrome bars whenever the view application is active, in one of THREE modes determined by the rail's current selection (mirroring the canonical prototype's SOLO/PAIR behavior):

- **Coordinator mode** (a coordinator row is selected — root or sub): rail + a full-width **Coord pane** (the coordinator's own PTY) + a right-side Details pane. No AGENT pane is composed.
- **Agent mode** (a worker/agent row is selected): rail + **Coord pane** (the agent's coordinator's PTY) + **AGENT pane** (the agent's PTY) — a split body.
- **Freelance mode** (a freelance row is selected): rail + a single full-width **AGENT pane** (the freelancer's PTY). No Coord pane is composed.

The center coordinator pane is titled **Coord** (parallel to the **Agent** pane title), NOT "HERA": the argus chrome already identifies the view, so a hera-stamped "HERA" label is redundant. The top chrome bar MUST NOT stamp any hera branding (no literal `HERA`/`Hera` text); it is kept as an empty 1-row bar so the body's vertical geometry is unchanged. The rail keeps its "Rail" title. Bottom-bar hints are advertised to argus per the key-surrender contract (argus draws the plugin-mode bar); when rendered standalone the bottom bar is focus-aware.

A pane that has no bound task — the **empty/placeholder state** ("(no coord selected)" / "(no agent selected)") — MUST fill its layout-allocated rect exactly as a bound pane does: the placeholder pane's emulator surface MUST track the full inner rect of its Flex allocation rather than staying at the construction-time default size, so empty coord/agent panes fill the available vertical space and split the horizontal space on the same proportions as their content counterparts. No source-PTY resize is dispatched for an unbound placeholder pane (there is no PTY to size).

#### Scenario: Coordinator selection is a full-width Coord pane

- **WHEN** a coordinator row (root or sub) is selected
- **THEN** the body MUST be the rail plus a full-width Coord pane bound to that coordinator's PTY (alongside the Details pane) AND no AGENT pane MUST be present

#### Scenario: Agent selection splits Coord + AGENT

- **WHEN** a worker/agent row is selected
- **THEN** the body MUST be the rail + a Coord pane bound to the agent's coordinator + an AGENT pane bound to the agent

#### Scenario: Freelance mode collapses to rail + full-width agent

- **WHEN** the rail selection moves to a freelance row
- **THEN** the body MUST be the navigation rail plus a single AGENT pane spanning the remaining width AND no Coord pane MUST be present

#### Scenario: Switching selection re-composes the mode

- **WHEN** the rail selection moves between a coordinator, an agent, and a freelancer
- **THEN** the body MUST re-compose to the corresponding mode (full-width Coord / split / full-width AGENT), tearing down the now-absent pane's subscription

#### Scenario: Project-mode rail traversal updates both panes

- **WHEN** rail selection moves to a worker agent whose project's coord differs from the previous selection's project
- **THEN** the COORD pane MUST switch to the new project's coord binding ring buffer AND the AGENT pane MUST switch to the new agent's binding ring buffer

#### Scenario: Empty coord and agent panes fill their allocation

- **WHEN** the body composes an empty coord pane and/or an empty agent pane (no task bound, showing the placeholder text)
- **THEN** each empty pane MUST fill the full vertical space of its Flex allocation and the two panes MUST split the horizontal space on the same proportions as when they hold live content (no shorter, unevenly-sized placeholder boxes)

#### Scenario: Top chrome bar carries no hera branding

- **WHEN** the view surface renders in any mode
- **THEN** the top chrome row MUST NOT contain the literal text `HERA` or `Hera` AND the coordinator pane MUST be titled `Coord` (not `HERA`)

### Requirement: Freelance (unmanaged argus) agents render in a Freelance rail section grouped by repo

A **freelancer** is a live argus task that hera does not currently manage. The system SHALL surface freelancers in the rail so the operator never has to leave hera to notice that an unmanaged agent needs attention — and so that EVERY non-archived argus task is reachable in the rail.

The system SHALL determine the freelancer set from argus's live task list (the argus state cache): every non-archived argus task is a freelancer UNLESS (a) at least one hera binding for it is LIVE (`ended_at` null) — it renders under its orchestrator — or (b) it already renders as a role row in the orchestrator tree (workers and sub-coordinators render via the latest-binding fallback even after their bindings end). A hera binding is hera's claim on a task: a live task whose bindings have ALL ENDED (a coordinator binding reconciled away by resync, an archive round-trip that ended the binding) and that no rendered role row carries MUST fall back to the Freelance section — hera's claim has lapsed, and a live argus task MUST NOT become unreachable through hera-side binding bookkeeping. (A task hera has never bound remains the common freelancer case; a coordinator task surfaced this way is ADDITIONALLY still reachable through its orchestrator header's coord-pane binding.) Freelancers SHALL be rendered in a "Freelance" section below all project (orchestrator) rows and above the Archive separator, introduced by a "Freelance" separator that is shown ONLY when at least one freelancer exists (so the operator never lands on an empty section).

Within the Freelance section, freelancers SHALL be grouped by argus project (repo) — "the same way Argus shows them" — under per-repo headers sorted by project name. Each repo header MUST render a collapse chevron (`▾` expanded / `▸` collapsed), the project name, and the count of its live freelance tasks, and MUST toggle expand/collapse when the operator presses Space while that header is selected. Repo groups default to expanded so freelancers are visible by default. Each freelance row MUST render its argus-reported state (status / idle / needs-input) via the same icon rules as managed rows, and its elapsed column MUST show argus's own age string.

Archived argus tasks MUST NOT appear in the Freelance section by default; they MUST appear only when the Archive view is revealed via `l`.

#### Scenario: Unmanaged argus tasks surface as freelancers grouped by repo

- **WHEN** argus reports live tasks whose ids no hera binding references
- **THEN** a "Freelance" section MUST appear below all project rows, with those tasks grouped under per-repo headers sorted by project name

#### Scenario: Tasks with a live hera binding are excluded from Freelance

- **WHEN** an argus task has at least one live hera binding
- **THEN** that task MUST NOT appear in the Freelance section (it renders under its orchestrator instead)

#### Scenario: A live task whose hera bindings have all ended falls back to Freelance

- **WHEN** a non-archived argus task's hera bindings have ALL ended (e.g. a coordinator role's binding ended by `resync_missing`) and no role row in the orchestrator tree renders that task id
- **THEN** the task MUST appear as a named row in the Freelance section under its repo group, selectable like any freelancer

#### Scenario: Role rows rendered via the latest-binding fallback do not duplicate into Freelance

- **WHEN** a worker role's only binding has ended but the role still renders as a row in the orchestrator tree carrying that task id via the latest-binding fallback
- **THEN** that task MUST NOT additionally appear in the Freelance section

#### Scenario: Space toggles a Freelance repo group

- **WHEN** focus is `RAIL`, a Freelance repo header is selected, and the operator presses Space
- **THEN** that repo group MUST expand (or collapse if already expanded), revealing (or hiding) its freelance rows, independently of other repo groups

#### Scenario: No freelancers hides the section

- **WHEN** every live argus task is excluded from the freelancer set
- **THEN** the "Freelance" separator and all repo headers MUST NOT be rendered

#### Scenario: Archived freelancers appear only in Archive

- **WHEN** an argus task in the freelancer set is archived
- **THEN** it MUST NOT appear in the live Freelance section AND MUST appear only when the Archive view is revealed via `l`

### Requirement: Three-state focus model

The system SHALL maintain focus in exactly one of three states at any given time: `RAIL`, `COORD`, or `AGENT`. On first open, focus MUST start in `RAIL`. The focused element MUST be visually indicated by a colored border. The focus border color MUST mirror argus: the focused element's border uses argus's title/focus color (`theme.ColorTitle`) and unfocused borders use argus's dim border color (`theme.ColorBorder`), applied consistently across the rail and both panes. Because the SDK terminalpane paints its own border in a non-argus default (white) on focus, the pane border MUST be repainted in the argus focus color so the rail and panes share one focus color.

#### Scenario: Focus border uses the argus theme color

- **WHEN** an element (rail, HERA pane, or AGENT pane) holds focus
- **THEN** its border MUST be painted in argus's focus color (`theme.ColorTitle`) — not tview's yellow default or the SDK terminalpane's white-on-focus — so a focused pane and a focused rail show the same color

#### Scenario: First-open focus is RAIL

- **WHEN** a new WebSocket connection is established and the view application starts
- **THEN** focus MUST be in the `RAIL` state

#### Scenario: Focus indicator on focused element

- **WHEN** focus is in any one of `RAIL`, `COORD`, or `AGENT`
- **THEN** the corresponding element MUST render with a colored border distinct from the other two elements' borders

### Requirement: Focus traversal via arrow ladder and Ctrl-Q escape

The system SHALL advance focus along the `RAIL → COORD → AGENT` ladder on Cmd/Ctrl-→ and retreat along the `AGENT → COORD → RAIL` ladder on Cmd/Ctrl-←. From `RAIL` focus, pressing `Enter` MUST jump directly to `AGENT` focus (skipping `COORD`). From any focus state, pressing `Ctrl-Q` MUST return focus to `RAIL`. When the body is in freelance mode (a freelance row is selected and no COORD pane is present), the `COORD` state MUST be skipped: Cmd/Ctrl-→ from `RAIL` advances directly to `AGENT`, and Cmd/Ctrl-← from `AGENT` retreats directly to `RAIL`.

#### Scenario: Cmd/Ctrl-right advances RAIL → COORD

- **WHEN** focus is `RAIL` and the operator presses Cmd/Ctrl-→
- **THEN** focus MUST transition to `COORD`

#### Scenario: Cmd/Ctrl-right advances COORD → AGENT

- **WHEN** focus is `COORD` and the operator presses Cmd/Ctrl-→
- **THEN** focus MUST transition to `AGENT`

#### Scenario: Cmd/Ctrl-left retreats AGENT → COORD

- **WHEN** focus is `AGENT` and the operator presses Cmd/Ctrl-←
- **THEN** focus MUST transition to `COORD`

#### Scenario: Cmd/Ctrl-left retreats COORD → RAIL

- **WHEN** focus is `COORD` and the operator presses Cmd/Ctrl-←
- **THEN** focus MUST transition to `RAIL`

#### Scenario: Enter from RAIL jumps to AGENT

- **WHEN** focus is `RAIL` and the operator presses `Enter` against a live (non-archived) agent row
- **THEN** focus MUST transition to `AGENT` (skipping `COORD`)

#### Scenario: Ctrl-Q returns to RAIL from any state

- **WHEN** focus is `COORD` or `AGENT` and the operator presses `Ctrl-Q`
- **THEN** focus MUST transition to `RAIL`

#### Scenario: Freelance mode skips COORD on advance

- **WHEN** the body is in freelance mode, focus is `RAIL`, and the operator presses Cmd/Ctrl-→
- **THEN** focus MUST transition directly to `AGENT` without entering `COORD`

#### Scenario: Freelance mode skips COORD on retreat

- **WHEN** the body is in freelance mode, focus is `AGENT`, and the operator presses Cmd/Ctrl-←
- **THEN** focus MUST transition directly to `RAIL` without entering `COORD`

### Requirement: Pane focus forwards keystrokes to the bound task's PTY input

The system SHALL forward keystrokes received while focus is `COORD` or `AGENT` to the bound task's input endpoint (`POST /api/tasks/{id}/input`) verbatim. Keystrokes that match the focus-traversal bindings (Cmd/Ctrl-←/→, Ctrl-Q) MUST be intercepted by the view application and MUST NOT be forwarded.

"Verbatim" means the **raw input bytes** received from the host over the WebSocket MUST be forwarded to the PTY unchanged — they MUST NOT be round-tripped through the view's tcell event parser and re-encoded. The host (argus) has already encoded each keystroke into the byte sequence a real terminal would produce; the view forwards those bytes as-is, preserving full terminal fidelity for keys a tcell re-parse would mangle or swallow — notably Shift+Enter / Alt+Enter (newline-insert, `ESC CR`), Alt+Backspace (word-delete, `ESC DEL`), and Alt+arrows / word-motion sequences. The view therefore routes inbound input by focus state at the WebSocket transport boundary, BEFORE the tcell parser: while focus is in a pane the raw bytes are forwarded to the bound task and MUST NOT additionally drive the parser (no double handling); while focus is `RAIL` the bytes are fed to the tcell parser unchanged so rail navigation and mutation keys work as specified.

The focus-traversal and in-pane control chords the view owns — Ctrl-Q (escape to RAIL), Cmd/Ctrl-←/→ (focus ladder), Cmd/Ctrl-↑/↓ (in-pane navigation), and Shift-↑/↓ (pane scrollback) — MUST continue to be recognized even while focus is in a pane: their byte sequences are fed to the parser (and intercepted by the view) rather than forwarded to the PTY. These chord sequences are modifier-distinguished from forwardable content (e.g. Alt-modified word-motion and ESC-prefixed sequences), so passthrough content never collides with a chord.

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

### Requirement: Panes repaint live on PTY output, independent of keystroke input

The system SHALL repaint a pane's terminal surface whenever new PTY output bytes are ingested into that pane's emulator, WITHOUT depending on any keystroke or other input event to force a draw. This MUST hold for BOTH echoed keystrokes (output the bound task emits in response to typed input) AND fully autonomous agent output (output the task emits with no input at all): in both cases the newly-painted cells MUST become visible as the bytes arrive, not only after some later unrelated event (a re-selection, resize, or focus change) incidentally triggers a draw.

Because the view forwards pane-focus keystrokes as raw bytes at the transport boundary BEFORE the tcell parser (per the keystroke-forwarding requirement), keystrokes no longer incidentally drive a tview redraw. The pane-output path therefore MUST schedule its own redraw: when the pane's emulator ingests a non-empty output chunk, the system MUST schedule a redraw on the UI event loop. The redraw scheduling MUST be safe to invoke from the output-consumer goroutine (it MUST NOT race the event loop).

The redraw scheduling MUST coalesce a burst of ingested chunks into at most one draw per frame interval, and MUST NOT block the output-consumer goroutine on the event loop. The consumer therefore drains a burst fully into the emulator BETWEEN coalesced draws, so each painted frame reflects a settled emulator state rather than a partial mid-stream frame. This prevents the two failure modes of a per-chunk draw: (a) on pane bind, visibly scrolling through the bound task's entire scrollback before landing at the tail — instead the settled snapshot MUST paint once at the tail; and (b) garbled, interleaved, or stale cells while a chatty task or a SIGWINCH whole-screen re-render streams in. The coalescer MUST still guarantee the latest output is painted within approximately one frame interval of the last ingested chunk (it MUST NOT starve a continuous stream). This mirrors argus's own task-terminal repaint cadence (a ticker that draws only when there is new output), not argus's plugin-pane path (one draw per chunk), because hera's panes render a chatty PTY with scrollback rather than discrete full-screen frames.

#### Scenario: Autonomous agent output repaints the pane without input

- **WHEN** a pane is bound to a task and the task emits output bytes while the operator types nothing
- **THEN** the pane MUST repaint to show the new output as it arrives (a redraw is scheduled on the event loop when the emulator ingests the chunk), without waiting for a keystroke, re-selection, resize, or focus change

#### Scenario: Echoed keystrokes appear live in the focused pane

- **WHEN** focus is in a pane bound to a task, the operator types characters, and the task echoes them back as PTY output
- **THEN** the echoed characters MUST become visible in the pane as the echo arrives, NOT only after the operator navigates away and back to re-select the pane

#### Scenario: Output-driven redraw does not require a redraw per byte

- **WHEN** a bound task emits output faster than the UI can draw
- **THEN** the system MUST still repaint with the newest output AND MAY coalesce the redraws (it is not required to perform one full redraw per output byte)

#### Scenario: Pane bind paints the settled snapshot once at the tail

- **WHEN** a pane is bound to a task with existing scrollback and the snapshot bytes are ingested into the emulator as one or more chunks
- **THEN** the redraw scheduling MUST coalesce those chunks so the pane paints the settled tail of the snapshot, and MUST NOT visibly replay/scroll through the scrollback history one intermediate frame at a time

#### Scenario: A burst of chunks coalesces to a bounded number of settled draws

- **WHEN** the emulator ingests a burst of N output chunks within a single frame interval (e.g. a chatty agent or a whole-screen re-render after a resize)
- **THEN** the system MUST schedule at most one draw for that interval (far fewer than N), and that draw MUST paint a settled emulator frame rather than a partial mid-stream frame

### Requirement: Mutation keys are RAIL-focus-only

The system SHALL recognize the RAIL-only key set (`n`, `w`, `r`, `a`, `l`, `?`, `s`, `S`, `P`, `^d`, `^r`, `^p`) ONLY when focus is `RAIL`. When focus is `COORD` or `AGENT`, every one of these keys — including the destructive/external verbs `^d`, `^r`, and `^p` — MUST be treated as ordinary input and forwarded to the bound task's PTY (per the keystroke-forwarding requirement): a printable key forwards its byte (`P` forwards the byte `P`), and `^d`/`^r`/`^p` forward their control bytes (Ctrl-D=0x04, Ctrl-R=0x12, Ctrl-P=0x10) so an agent gets EOF / reverse-search / history-prev normally. None of these keys fires a mutation or is intercepted while focus is in a pane.

#### Scenario: `n` in RAIL focus opens new-project modal

- **WHEN** focus is `RAIL` and the operator presses `n`
- **THEN** the view MUST open the new-project input modal

#### Scenario: `n` in COORD focus types into the PTY

- **WHEN** focus is `COORD` and the operator presses `n`
- **THEN** the daemon MUST POST the byte `n` to the COORD task's input endpoint AND MUST NOT open the new-project modal

#### Scenario: `r` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `r`
- **THEN** the daemon MUST POST the byte `r` to the AGENT task's input endpoint AND MUST NOT open the rename modal

#### Scenario: `?` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `?`
- **THEN** the daemon MUST POST the byte `?` to the AGENT task's input endpoint AND MUST NOT open the help modal

#### Scenario: `P` in RAIL focus toggles pin

- **WHEN** focus is `RAIL` and the operator presses `P` against a coordinator or agent row
- **THEN** the view MUST toggle that row's pinned state AND MUST NOT forward a byte to any PTY

#### Scenario: `P` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `P`
- **THEN** the daemon MUST POST the byte `P` to the AGENT task's input endpoint AND MUST NOT toggle any pin

#### Scenario: `^d` in AGENT focus forwards Ctrl-D to the PTY

- **WHEN** focus is `AGENT` and the operator presses `^d`
- **THEN** the daemon MUST forward the control byte Ctrl-D (`0x04`) to the AGENT task's input endpoint AND MUST NOT open the delete confirm modal

### Requirement: `n` creates a new orchestrator via spawned argus task

The system SHALL, when the operator confirms the new-project modal with a unique non-empty name and an optional coord mission, create a new argus task via `POST /api/tasks` whose prompt invokes `hera_new_orchestrator(cwd=$PWD, name=<chosen>, coord_role_name="coord", mission=<chosen>)` as its first action. The argus project MUST default to the chosen orchestrator name (creating the argus project if absent). The view MUST NOT directly insert orchestrator / role / binding rows; those rows MUST be created by the existing `hera_new_orchestrator` handler when the spawned task makes its first MCP call.

#### Scenario: New-project confirm spawns argus task

- **WHEN** the operator confirms the new-project modal with name `foo` and mission `ship F`
- **THEN** the daemon MUST issue a `POST /api/tasks` whose prompt contains `hera_new_orchestrator(cwd=$PWD, name="foo", coord_role_name="coord", mission="ship F")`

#### Scenario: New-project confirm with duplicate non-archived name rejected

- **WHEN** the operator confirms the new-project modal with a name that matches an existing non-archived orchestrator
- **THEN** the modal MUST surface a validation error AND no argus task MUST be spawned

### Requirement: `r` renames the selected orchestrator or role

The system SHALL, when the operator confirms the rename modal with a unique non-empty name, update the chosen orchestrator's or role's `name` column in hera's DB. Uniqueness is enforced across non-archived orchestrators (when renaming an orchestrator) or within the orchestrator's non-archived roles (when renaming a role). Rename MUST NOT affect the argus task's name or its worktree; argus task names are independent of hera role names.

#### Scenario: Rename role updates DB and reflects in rail

- **WHEN** the operator confirms the rename modal against role `foo/coord` with the new name `lead`
- **THEN** the `roles` row MUST be updated to `name="lead"` AND the rail MUST reflect the new name on the next refresh tick AND no argus side effects MUST occur

#### Scenario: Rename to duplicate name rejected

- **WHEN** the operator confirms the rename modal with a name that conflicts with another non-archived row at the same scope
- **THEN** the modal MUST surface a validation error AND no DB write MUST occur

### Requirement: `^d` deletes a role or cascade-deletes an orchestrator

The system SHALL, on `^d` confirmation against a role, end the role's live binding (if any) with `end_reason="user_deleted"`, set the role's `archived_at` to the current timestamp, and invoke `git worktree remove --force <worktree_path>` against the binding's worktree path. The role row MUST persist (archived). On `^d` confirmation against an orchestrator, the same operations MUST cascade to every role under the orchestrator, and the orchestrator row's `archived_at` MUST also be set. If a worktree path is empty or the directory does not exist, the `git worktree remove` step MUST be a soft no-op (logged and skipped, not an error).

#### Scenario: Delete role ends binding and removes worktree

- **WHEN** the operator confirms the `^d` modal against role `foo/worker-1` whose live binding has worktree path `/Users/x/.argus/worktrees/foo/worker-1`
- **THEN** hera MUST update the binding's `ended_at` and `end_reason="user_deleted"`, set the role's `archived_at` to the current timestamp, AND execute `git worktree remove --force /Users/x/.argus/worktrees/foo/worker-1`

#### Scenario: Delete orchestrator cascades to all roles

- **WHEN** the operator confirms the `^d` modal against orchestrator `foo` which has roles `coord`, `w1`, `w2`
- **THEN** hera MUST end every live binding under `foo`, set `archived_at` on `foo`, `coord`, `w1`, and `w2`, AND invoke `git worktree remove --force` against each role's binding's worktree path

#### Scenario: Worktree missing is soft no-op

- **WHEN** the operator confirms `^d` against a role whose binding's `worktree_path` is empty OR the directory does not exist on disk
- **THEN** hera MUST skip the `git worktree remove` step, log the skip, AND still mark the role archived AND end the binding

### Requirement: `a` toggles archived state on an orchestrator or role

The system SHALL, on `a` against a non-archived role, set the role's `archived_at` to the current timestamp AND invoke argus's archive endpoint (`POST /api/tasks/{id}/archive`) on the binding's `argus_task_id`. The worktree MUST be preserved. On `a` against a non-archived orchestrator, the same MUST cascade to every role under that orchestrator — EXCEPT when the header is in the mixed-coord state (displayed-active orchestrator whose coord task is argus-archived), where `a` MUST instead repair by unarchiving the coord's argus task, per the mixed-coord repair requirement. The toggle is SYMMETRIC: on `a` against an already-archived role, `archived_at` MUST be cleared AND hera MUST invoke argus's unarchive endpoint on the role's bound argus task — the rail buckets a row into the Archive expando when EITHER side is archived, so clearing only the hera side would produce no visible change.

The toggle DIRECTION MUST follow the row's EFFECTIVE rendered archived state — the same predicate the rail uses to bucket the row into an Archive expando (hera `archived_at` set, OR argus-archived, OR dead) — never a single backing flag alone: `a` MUST unarchive any row that DISPLAYS as archived and archive any row that displays as active. The view layer SHALL compute the effective state from the selected row and dispatch an EXPLICIT archive or unarchive verb (not a flag-inspecting toggle), so on a mixed-flag row (e.g. hera-active + argus-archived — a state historical asymmetric toggles could produce) `a` clears BOTH sides instead of re-archiving the side that was already clear. Unarchive MUST clear both the hera and argus sides; archive MUST set both. In BOTH directions the bound task MUST be resolved from the role's live binding when one exists, FALLING BACK to the role's most recent binding (by `started_at`) regardless of `ended_at` — archiving a task ENDS its binding (`end_reason='argus_archived'`) while preserving its `argus_task_id`, so for exactly the rows that need unarchiving no live binding exists and a live-only lookup would silently skip the argus side, defeating the symmetric toggle; likewise, archiving a role whose binding was ended by a previous archive (the hera-active + argus-active row that a partial unarchive or historical asymmetric toggle leaves behind) MUST still archive its bound argus task via the same fallback — a live-only lookup there would stamp hera's `archived_at` while silently leaving the argus task active, recreating the mixed state. The ended-binding fallback MUST carry a **shared-task guard** in the ARCHIVE direction only: when the task id was resolved via an ENDED binding AND any OTHER role holds a LIVE binding (`ended_at` NULL) to that same argus task, the argus-side archive MUST be skipped — the live binding is that other role's ownership claim, and archiving through the stale ended binding would yank a task out from under an ACTIVE role or orchestrator (the cascade-collateral hazard: archiving an old orchestrator archived the operator's live coord's task). The hera-side role archive still proceeds, and the skip MUST be logged at info naming both roles (the role being archived and the live-bound role). A task resolved via the role's OWN live binding archives unconditionally — that live binding IS the ownership — and the unarchive direction stays permissive (no guard). Only when the role has no binding at all (or the resolved binding carries no argus task id) is the argus step skipped entirely (nothing to archive or unarchive); the hera-side write still proceeds. On `a` against an already-archived orchestrator, the orchestrator's `archived_at` MUST be cleared AND hera MUST likewise invoke argus's unarchive endpoint on the coord role's live binding's argus task; unarchiving an orchestrator MUST NOT cascade to its worker roles (workers unarchive individually). On `a` against a freelance row (an unmanaged argus task with no hera role or binding), hera MUST address the argus task directly — issuing `POST /api/tasks/{id}/archive` (or the unarchive endpoint when the task is already archived per argus state) against the row's argus task id — with no hera DB write (there is no role row to stamp).

#### Scenario: Archive role calls argus and preserves worktree

- **WHEN** the operator presses `a` against non-archived role `foo/w1` bound to argus task `T1`
- **THEN** hera MUST set the role's `archived_at` to the current timestamp, issue `POST /api/tasks/T1/archive` to argus, AND MUST NOT touch the worktree directory

#### Scenario: Unarchive role unarchives the argus task too

- **WHEN** the operator presses `a` against archived role `foo/w1` whose live binding's argus task `T1` is archived on the argus side
- **THEN** hera MUST clear the role's `archived_at` AND issue the argus unarchive endpoint for `T1`, so the row visibly returns to the coordinator's active children

#### Scenario: Mixed-flag role (hera-active + argus-archived) unarchives both sides

- **WHEN** the operator presses `a` against a role row that DISPLAYS as archived (it sits in an Archive expando) because its bound argus task is archived, while its hera `archived_at` is NULL
- **THEN** hera MUST treat the press as UNARCHIVE — leaving `archived_at` clear (no fresh archive timestamp may be written) AND issuing the argus unarchive endpoint for the bound task — so the row visibly leaves the Archive expando rather than being re-archived

#### Scenario: Dead row treated as displayed-archived

- **WHEN** the operator presses `a` against a role row that displays as archived only because its binding is dead (the argus task is gone)
- **THEN** hera MUST treat the press as UNARCHIVE (clear `archived_at`, attempt the argus unarchive), never as a fresh archive

#### Scenario: Archive role with an ended binding falls back to the latest binding

- **WHEN** the operator presses `a` against a role that DISPLAYS as active whose only binding was ended by a previous archive (`end_reason='argus_archived'`) and records argus task `T1`, and no other role holds a live binding to `T1`
- **THEN** hera MUST set the role's `archived_at` AND issue `POST /api/tasks/T1/archive` resolved via the most recent binding — the ended binding MUST NOT cause the argus side to be silently skipped, leaving the argus task active under a hera-archived role

#### Scenario: Archive via an ended binding skips argus when the task is live-bound to another role

- **WHEN** role `old/w1` is archived (directly or via an orchestrator cascade) and its task id `T1` resolves via an ENDED binding, while role `new/coord` holds a LIVE binding to `T1`
- **THEN** hera MUST set `old/w1`'s `archived_at`, MUST NOT issue the argus archive endpoint for `T1`, AND MUST log the skip at info naming both `old/w1` and `new/coord`

#### Scenario: Archive via the role's own live binding archives even when the task is shared

- **WHEN** the operator presses `a` against an active role whose LIVE binding records argus task `T1`, while another role also holds a live binding to `T1` (multi-binding)
- **THEN** hera MUST issue `POST /api/tasks/T1/archive` as today — the role's own live binding is its ownership claim and the shared-task guard MUST NOT apply

#### Scenario: Orchestrator cascade does not archive a sibling orchestrator's live task

- **WHEN** the operator cascade-archives an OLD orchestrator containing a role whose ended binding records task `T1`, and `T1` is live-bound under a DIFFERENT active orchestrator
- **THEN** the cascade MUST archive the old orchestrator and its roles hera-side, MUST NOT issue the argus archive endpoint for `T1`, and MUST succeed (the skip is not a failure)

#### Scenario: Unarchive role with an ended binding falls back to the latest binding

- **WHEN** the operator presses `a` against an archived role whose only binding was ended by the archive (`end_reason='argus_archived'`) and records argus task `T1`
- **THEN** hera MUST clear the role's `archived_at` AND issue the argus unarchive endpoint for `T1` resolved via the most recent binding — the ended binding MUST NOT cause the argus side to be silently skipped

#### Scenario: Unarchive role with no binding at all skips argus

- **WHEN** the operator presses `a` against an archived role that has never had a binding
- **THEN** hera MUST clear the role's `archived_at` AND MUST NOT call argus

#### Scenario: Archive orchestrator cascades to roles

- **WHEN** the operator presses `a` against non-archived orchestrator `foo` with roles `coord`, `w1`, `w2`, and `foo` is NOT in the mixed-coord state
- **THEN** hera MUST set `archived_at` on `foo`, `coord`, `w1`, and `w2` AND issue an archive call to argus for each role's resolved binding's argus_task_id (live preferred, latest fallback — the cascade calls the role-level archive and inherits its resolution)

#### Scenario: Mixed-coord header repairs instead of cascading

- **WHEN** the operator presses `a` against a displayed-active orchestrator header whose coord task is argus-archived
- **THEN** hera MUST issue the argus unarchive endpoint for the coord task directly (no hera DB write, no cascade), per the mixed-coord repair requirement

#### Scenario: Unarchive orchestrator unarchives its coord task but does not cascade to workers

- **WHEN** the operator presses `a` against an archived orchestrator `foo` whose roles `coord`, `w1` are also archived
- **THEN** hera MUST clear `archived_at` on `foo`, issue the argus unarchive endpoint for the coord role's live binding's argus task, AND MUST leave `archived_at` on `coord` and `w1` set

#### Scenario: Archive freelancer addresses the argus task directly

- **WHEN** the operator presses `a` against a non-archived freelance row whose argus task is `T9`
- **THEN** hera MUST issue `POST /api/tasks/T9/archive` to argus AND MUST NOT write any hera DB row

#### Scenario: Unarchive freelancer addresses the argus task directly

- **WHEN** the operator presses `a` against a freelance row whose argus task `T9` is archived per argus state
- **THEN** hera MUST issue the argus unarchive endpoint for `T9` AND MUST NOT write any hera DB row

### Requirement: `l` toggles visibility of archived items in the rail

The system SHALL render archived orchestrators and roles in a collapsible "Archive" section at the bottom of the rail. This section MUST be hidden on first view-application open. The `l` key (when focus is `RAIL`) MUST toggle visibility of the Archive section. Toggle state is in-memory for the WebSocket session; a fresh WebSocket connection MUST start with the Archive section hidden.

#### Scenario: First-open hides archived items

- **WHEN** the view application starts (new WebSocket connection)
- **THEN** archived orchestrators and roles MUST NOT be visible in the rail

#### Scenario: `l` reveals archived items

- **WHEN** focus is `RAIL`, the Archive section is hidden, and the operator presses `l`
- **THEN** the rail MUST render a collapsible Archive section listing all archived orchestrators and roles

#### Scenario: `l` toggles back to hidden

- **WHEN** focus is `RAIL`, the Archive section is visible, and the operator presses `l`
- **THEN** the rail MUST hide the Archive section

### Requirement: Resurrect archived orchestrator on Enter when Archive visible

The system SHALL, when the Archive section is visible and the operator presses `Enter` against an archived coord row, prompt for confirmation ("Resurrect <project>?") and on confirm: clear `archived_at` on the orchestrator and the coord role, then spawn a fresh argus task via `POST /api/tasks` in the role's stored `argus_project` whose prompt invokes `hera_join(cwd=$PWD)`. The new task's worktree is fresh; the role's stored mission and constraints are inherited by the rebinding when `hera_join` resolves the cwd to the dormant binding-slot.

#### Scenario: Resurrect spawns argus task in role's argus_project

- **WHEN** the operator presses `Enter` against the archived coord row of orchestrator `foo` whose coord role has `argus_project="foo-frontend"` AND confirms the modal
- **THEN** hera MUST clear `archived_at` on orchestrator `foo` and the coord role AND MUST issue `POST /api/tasks` to argus's `foo-frontend` project with a prompt containing `hera_join(cwd=$PWD)`

#### Scenario: Resurrect inherits mission and constraints

- **WHEN** the operator resurrects an archived coord role whose `mission="ship F"` and `constraints="ship by friday"`
- **THEN** the dormant role row's `mission` and `constraints` columns MUST remain unchanged (the new task inherits them on `hera_join`)

### Requirement: `?` opens argus's help overlay via a help control frame

Under the argus key-surrender contract (argus forwards every key to a focused plugin view), the system SHALL, when the operator presses `?` while focus is `RAIL`, send a `{"type":"help"}` text control frame to argus over the view WebSocket so argus pops its help overlay rendered from hera's pushed hotkey dictionary. The system MUST NOT render its own in-surface help modal. While focus is `COORD` or `AGENT`, `?` MUST be forwarded to the bound task's PTY (not intercepted). Sending a help frame MUST NOT trigger any DB read or write.

#### Scenario: `?` in RAIL sends a help frame

- **WHEN** focus is `RAIL` and the operator presses `?`
- **THEN** hera MUST send a `{"type":"help"}` text frame to argus AND MUST NOT render an in-surface help modal

#### Scenario: `?` in a pane is forwarded to the PTY

- **WHEN** focus is `AGENT` (or `COORD`) and the operator presses `?`
- **THEN** the byte MUST be forwarded to the bound task's input endpoint AND no help frame MUST be sent

### Requirement: Esc returns control to argus from RAIL via a release frame

Because argus surrenders Esc to the focused plugin view, the system SHALL, when the operator presses Esc while focus is `RAIL`, send a `{"type":"release"}` text control frame to argus — handing the keyboard back (argus blurs, closes the connection, and returns to its task list). While focus is `COORD` or `AGENT`, Esc MUST be forwarded to the bound task's PTY verbatim and MUST NOT release the view, so an agent (vim, Claude, etc.) can receive Esc. Leaving from a pane is therefore a two-step (Ctrl-Q to `RAIL`, then Esc) or argus's always-available double-Ctrl-Q failsafe.

#### Scenario: Esc in RAIL releases to argus

- **WHEN** focus is `RAIL` and the operator presses Esc
- **THEN** hera MUST send a `{"type":"release"}` text frame to argus AND MUST NOT forward the Esc byte to any task's input endpoint

#### Scenario: Esc in a pane is forwarded to the PTY

- **WHEN** focus is `AGENT` (or `COORD`) and the operator presses Esc
- **THEN** the Esc byte MUST be forwarded to the bound task's input endpoint AND the view MUST NOT release to argus

### Requirement: Hera advertises focus-aware hotkeys to argus and renders no internal bottom bar

The system SHALL push a `{"type":"hotkeys","items":[...]}` text control frame to argus on view connect and on every focus-state change, describing the key bindings for the current focus state (`RAIL` / `COORD` / `AGENT`), with the operator-facing keys flagged `bar:true` to populate argus's context-sensitive bottom bar and the full set driving argus's help overlay. The system MUST NOT render its own bottom-bar row within the view surface; argus renders the plugin-mode status bar, including the reserved `^Q^Q argus` exit hint that hera neither advertises nor displaces.

The `RAIL`-focus hotkey set advertised with `bar:true` MUST include the spawn-worker key `w` (label "new agent") and the adopt-freelancer key `J` (label "adopt"), alongside the existing rail mutation keys, so both recently-added rail gestures are discoverable in argus's bottom bar. As with the other always-listed rail mutation keys (`n`/`r`/`a`), per-row applicability is surfaced by the op itself (a visible "not applicable" notice when pressed on a row it cannot act on), not by hiding the bar hint.

#### Scenario: hotkeys pushed on focus change

- **WHEN** the focus state changes (for example `RAIL` → `COORD`)
- **THEN** hera MUST send a `{"type":"hotkeys",...}` frame whose items reflect the new focus state's bindings

#### Scenario: no internal bottom bar rendered

- **WHEN** the view surface renders in any focus state
- **THEN** it MUST NOT include hera's own bottom-bar row (argus owns the plugin-mode status bar)

#### Scenario: spawn-worker and adopt keys advertised on the bottom bar

- **WHEN** hera pushes the `RAIL`-focus hotkeys frame
- **THEN** the `bar:true` items MUST include `w` ("new agent") and `J` ("adopt")

### Requirement: Every mutation is gated behind a confirmation or input modal

The system SHALL display a confirmation or input modal for every destructive, creative, or external rail-level mutation (`n`, `w`, `r`, `^d`, `^r` prune, `^p` open-PR, and resurrect-on-Enter against an archived coord). The DB writes, worktree/branch destruction, and external HTTP calls MUST NOT occur unless the operator explicitly confirms the modal. Reversible single-key toggles — `a` archive/unarchive and `l` listall — MUST NOT require a modal (`a` is reversible by pressing `a` again; `l` is a pure view-state toggle).

#### Scenario: `^d` shows confirmation before deleting

- **WHEN** the operator presses `^d` in `RAIL` against a role
- **THEN** a confirmation modal MUST appear naming the role and (if applicable) its worktree path AND MUST require an explicit confirm keystroke before any DB write or `git worktree remove` invocation

#### Scenario: `w` shows an input modal before spawning

- **WHEN** the operator presses `w` in `RAIL` against a coordinator
- **THEN** an input modal MUST appear AND no argus task, role, or binding MUST be created until the operator confirms it with a non-empty prompt

#### Scenario: `l` listall does not require a modal

- **WHEN** the operator presses `l` in `RAIL`
- **THEN** the Archive section visibility MUST toggle immediately with no modal

### Requirement: Modal overlays take keyboard focus, dismiss via Enter and Esc, restore prior focus, and use the argus theme

The system SHALL move tview keyboard focus to the modal primitive whenever any modal overlay (input, two-field form, confirm, error) opens — including modals opened from the async mutation goroutine via the event-loop bounce — so the modal's buttons receive Enter directly. While a modal is active, NOTHING ELSE may take focus: background focus writes (the focus-border repaint chokepoint driven by rail repopulates, body-mode changes, or argus state refreshes) MUST NOT move tview focus off the modal — a focus steal leaves the operator trapped with a visible modal that no longer receives keys. Every modal MUST be dismissable from the keyboard: Enter MUST activate the focused button (OK / Yes / No), and Esc MUST dismiss — closing an error modal, and acting as cancel/No for confirm and input modals. On dismissal the system MUST restore tview focus to the primitive that held it before the modal opened (the rail, for RAIL-only mutation keys). Modal surfaces MUST use the argus theme — dark background, argus border/title colors, argus text colors — not tview's default contrast (lavender) styling, so overlays read as part of the same application.

#### Scenario: Error modal from a failed mutation takes focus and Enter dismisses

- **WHEN** a rail mutation fails and the error modal opens via the async goroutine's event-loop bounce, and the operator presses Enter
- **THEN** the modal MUST have tview keyboard focus on open AND Enter MUST activate OK, closing the modal and restoring focus to the rail

#### Scenario: Esc dismisses an error modal

- **WHEN** an error modal is visible and the operator presses Esc
- **THEN** the modal MUST close AND focus MUST be restored to the primitive that held it before the modal opened

#### Scenario: Background refresh does not steal focus from an open modal

- **WHEN** a modal is open and a background rail repopulate (DAO write or argus state refresh) triggers the focus-border repaint path
- **THEN** tview focus MUST remain on the modal AND a subsequent Enter MUST still dismiss it

#### Scenario: Confirm modal dismisses via Esc as No

- **WHEN** a confirm modal (e.g. the `^d` delete flow) is visible and the operator presses Esc
- **THEN** the modal MUST close without running the destructive action AND focus MUST be restored

#### Scenario: Modals render with the argus theme

- **WHEN** any modal overlay is drawn
- **THEN** its surface MUST use the argus theme's dark background and border/title/text colors AND MUST NOT render tview's default contrast-color (lavender) background

### Requirement: Rail refreshes within ~100 ms of any DAO write

The system SHALL subscribe the rail to an in-process broadcaster fed by the orchestrators / roles / bindings DAOs. Any insert, update, or delete on those tables MUST cause the rail to refresh its rendered tree within approximately 100 ms (debounced to coalesce bursts). The system MUST NOT poll the database on a timer for rail updates.

#### Scenario: Newly-adopted worker appears in rail

- **WHEN** the auto-adopt event handler inserts a new role + binding row for orchestrator `foo`
- **THEN** the rail MUST render the new agent under `foo` within approximately 100 ms

#### Scenario: No polling timer

- **WHEN** the daemon is idle (no DAO writes) for 60 seconds
- **THEN** the rail subsystem MUST NOT issue any DB read for the purpose of rail refresh during that interval

### Requirement: Resize envelope re-lays out the local view and source PTYs

The system SHALL handle a `{type:"resize", cols, rows}` text-frame envelope from argus by recalculating the tview Application's layout (top bar, bottom bar, fixed-width rail, equal-split coord + agent panes). For each task bound to a coord or agent pane, the system SHALL also request that the task's source PTY be resized to match the pane's allocated cols/rows via `POST /api/tasks/{id}/size`.

Source-PTY resize dispatches MUST be coalesced per task behind a short debounce window in which the latest requested size wins: a session's first frames can draw at the WebSocket screen's default 80x24 geometry before argus's resize envelope is processed, and the transient pane allocation that geometry produces (20x21) MUST NOT reach argus — argus kick-rerenders the worker's agent at the requested width, permanently baking narrow-wrapped output into the session history. The system MUST dedupe redundant resize requests (same cols/rows as the last value argus ACKNOWLEDGED for that task). A dispatch that fails (e.g., argus returns 404 "no active session" while the worker is mid-kick-restart from a prior resize) MUST NOT be recorded as applied; the system MUST retry with the latest desired size at a short interval, bounded by a maximum attempt count, so the correction lands once the worker session is back.

#### Scenario: Resize re-lays out panes

- **WHEN** argus sends `{type:"resize", cols:120, rows:40}` over the WebSocket
- **THEN** the view MUST re-render the layout with the new dimensions on the next frame

#### Scenario: Initial bind aligns source PTY to pane allocation

- **WHEN** a coord or agent pane is bound to an argus task and rendered for the first time
- **THEN** the daemon MUST issue `POST /api/tasks/{id}/size` with the pane's allocated cols/rows (after the debounce window settles) unless that size already matches the task's current PTY size

#### Scenario: Layout change re-aligns source PTY

- **WHEN** the bound pane's allocated cols/rows change (whether from a WebSocket resize envelope or any other layout shift)
- **THEN** the daemon MUST issue `POST /api/tasks/{id}/size` with the new allocation once the debounce window settles

#### Scenario: Transient pre-envelope allocation never reaches argus

- **WHEN** a pane dispatches a resize for the default-surface allocation (e.g., 20x21 from an 80x24 screen) and the real resize envelope re-layout supersedes it within the debounce window
- **THEN** the daemon MUST issue exactly one `POST /api/tasks/{id}/size` carrying only the settled allocation

#### Scenario: Redundant resize is deduped

- **WHEN** the daemon would issue `POST /api/tasks/{id}/size` with cols/rows equal to the last value argus acknowledged for that task
- **THEN** the daemon MUST skip the HTTP call

#### Scenario: Failed resize is retried with the latest size

- **WHEN** a `POST /api/tasks/{id}/size` dispatch fails (e.g., 404 while the worker is mid-kick-restart)
- **THEN** the daemon MUST NOT record the size as applied AND MUST retry with the current desired size at a bounded interval until it succeeds or the attempt cap is reached

### Requirement: Rail renders coordinators as foldable rows with Archive expandos

The system SHALL render the rail as a tree mirroring argus's task panel: each coordinator (orchestrator root or sub-coordinator) is a selectable, foldable row rendered in argus's task-panel order — a status icon, then a chevron (`▾` expanded / `▸` collapsed), then a coordinator marker glyph (`󰹻`, U+F0E7B) before the name, then a live-child `(N)` count; its agents render as indented child rows; a worker that is itself a coordinator renders as a foldable coordinator row with its own nested children, recursively (a sub-coordinator MAY itself contain further sub-coordinators). Among a coordinator's children, sub-coordinators MUST sort before leaf workers (folders-first). Rows MUST NOT render kind pills.

Because hera's data model is flat (a role has a single orchestrator; orchestrators have no parent link), a sub-coordinator is modeled as a multi-binding: the SAME argus task is both a worker role under a parent orchestrator AND the coord of a separate child orchestrator (the join key is a worker role's bound argus task equalling a child orchestrator's coord task). The system SHALL resolve this multi-binding so the child orchestrator's roles nest beneath the worker row (which is rendered as a coordinator row), the child orchestrator MUST NOT also render at the top level, and resolution MUST guard against cycles. Every rail row carries a tree depth; rows MUST be indented by their depth (deeper rows further right), and an `Archive (N)` expando's archived children MUST indent one level deeper than the expando header.

The status icon (on both coordinator headers and agent rows) reflects argus status using argus's own task-panel glyph table (`` needs-input, `✓` complete, `󰖔` in-review, spinner running, `` idle, `○` pending — see the glyph-table requirement); a coordinator header's status icon is driven by its coord task's argus state, and a sub-coordinator row's by its own bound task's state. The coordinator marker glyph (distinct from the transient status icon) flags the row as a coordinator regardless of state; the prototype's `◆` root-coord icon is superseded by this status-icon + marker pairing. Every coordinator with archived direct children MUST render an `Archive (N)` expando below its active agents (collapsed by default) in the DEFAULT view — no `l` required: a hera-archived child role (`archived_at` set) MUST appear under its coordinator's `Archive (N)` expando exactly like an argus-archived or dead child, so archiving a row never makes it unreachable; `l` force-expands the expandos. Archived children MUST NOT inflate the coordinator header's live-child `(N)` count. Archived root coordinators MUST render under a top-level `Archive` section at the bottom of the rail. `space` MUST toggle the fold of the selected coordinator (root OR sub-coordinator) or Archive section.

The rail's currently selected row MUST be indicated by selected-text styling — its name rendered in argus's selected style (`theme.StyleSelected`, pink `theme.ColorSelected`) — applied consistently across ALL selectable row types (coordinator headers, agent/worker rows, sub-coordinator rows, Freelance repo headers, and Archive expandos). In ADDITION to the styling, the selected row MUST be identifiable WITHOUT color: the rail SHALL reserve a marker gutter at the start of every row and render a selection marker glyph (`›`) in that gutter on the selected row only; every other row MUST render a space there, so nothing shifts when the selection moves. The marker MUST apply to every selectable row kind (coordinator headers, agent/worker rows, sub-coordinator rows, Freelance repo headers, and Archive expandos), so a colorless text capture of the rail (a monochrome renderer, a screen reader, a reduced-color terminal) still reveals exactly which row the cursor is on. The rail MUST NOT paint any cell background to indicate selection: no row — selected or not — may render a filled (non-default) cell background. This guarantees no stale highlight cell can linger on a previously-selected row when the cursor moves, because no row ever writes a non-default background that a later draw would have to clear. The per-row selection indicator is distinct from, and MUST NOT be confused with, the rail pane's focus border (argus's focus color on the pane edge), which is governed by the focus-model requirement.

#### Scenario: Coordinator row is foldable with a count

- **WHEN** the rail renders a coordinator that has live agents
- **THEN** the row MUST show a chevron and a `(N)` live-child count AND pressing `space` on it MUST toggle whether its children are shown

#### Scenario: Coordinator header carries a status icon and coordinator marker

- **WHEN** the rail renders a coordinator whose coord task argus state is known
- **THEN** the header row MUST render, before the name, a status icon reflecting that argus state (the same task-panel glyph table as agent rows) AND the `󰹻` coordinator marker glyph AND MUST NOT render the prototype's `◆`

#### Scenario: Sub-coordinators sort before leaf workers

- **WHEN** a coordinator has both sub-coordinator children and leaf-worker children
- **THEN** the sub-coordinator rows MUST render above the leaf-worker rows

#### Scenario: Sub-coordinator renders as a nested foldable coord row with its children

- **WHEN** a worker role under a parent orchestrator has a bound argus task that is ALSO another (child) orchestrator's coord task
- **THEN** that worker MUST render as a foldable coordinator row (chevron + `󰹻` marker + live-child `(N)` count) with the child orchestrator's roles nested one level deeper, AND the child orchestrator MUST NOT also render as a top-level row

#### Scenario: Selecting a sub-coordinator composes full-width HERA bound to its own task

- **WHEN** focus is `RAIL` and a sub-coordinator row is selected
- **THEN** the body MUST compose the full-width `HERA` pane (no `AGENT` pane) bound to the sub-coordinator's OWN argus task (its own coordinator PTY), not the parent orchestrator's coord task, AND `Enter` MUST move focus into that `HERA` pane

#### Scenario: Archived children indent one level deeper than their Archive expando

- **WHEN** a coordinator's `Archive (N)` expando is folded open
- **THEN** its archived children MUST render indented one tree-depth level deeper than the `Archive (N)` expando header

#### Scenario: Archived agents live in their coordinator's Archive expando

- **WHEN** an agent under a coordinator is archived (hera `archived_at` set, argus-archived, or its task gone)
- **THEN** in the DEFAULT view (without `l`) it MUST NOT appear among the coordinator's active rows AND it MUST appear inside that coordinator's `Archive (N)` expando, which is collapsed by default — archiving a row MUST never make it vanish from the rail's reachable tree

#### Scenario: Hera-archiving a row never makes it unreachable

- **WHEN** the operator presses `a` on an agent row in the default view (showArchived off) and the role's `archived_at` is set
- **THEN** the next rail rebuild MUST render the coordinator's `Archive (N)` expando counting that role AND folding the expando open MUST reveal the row; the coordinator header's live-child `(N)` count MUST exclude it AND startup auto-selection MUST never bind an archived role's task

#### Scenario: Archived root coordinators live in the top-level Archive

- **WHEN** a root coordinator is archived
- **THEN** it MUST appear only under the top-level `Archive` section at the bottom of the rail

#### Scenario: Coordinator with no workers renders header-only

- **WHEN** an orchestrator has a live coord but no worker agents
- **THEN** the rail MUST render only its foldable coordinator row with no child agent row, and selecting it MUST compose the full-width HERA pane bound to the coord's PTY

#### Scenario: Selected row is indicated by selected-text styling, not a background fill

- **WHEN** the rail renders with the cursor on a selectable row (a coordinator header or an agent/worker row)
- **THEN** that row's name MUST render in `theme.StyleSelected` (pink `theme.ColorSelected`) AND none of that row's cells may carry a non-default cell background (no `theme.ColorHighlight` fill)

#### Scenario: Selected row is identifiable without color via the marker glyph

- **WHEN** the rail renders with the cursor on any selectable row (coordinator header, agent/worker row, sub-coordinator row, Freelance repo header, or Archive expando) and all color/styling is stripped from the output
- **THEN** the selected row MUST begin with the `›` selection marker glyph in the marker gutter AND no other row may render the marker, so the cursor position is identifiable from the text alone

#### Scenario: Marker gutter keeps columns stable across selection moves

- **WHEN** the rail cursor moves from one selectable row to another
- **THEN** the newly selected row MUST gain the `›` marker, the previously selected row MUST render a space in the marker gutter, AND no row content may shift columns as a result of the move

#### Scenario: Non-selected rows carry no lingering background

- **WHEN** the rail renders with the cursor on one row
- **THEN** every other (non-selected) row MUST render with the default cell background, so no stale highlight from a previous cursor position can persist

### Requirement: Enter enters the selection's primary pane

The system SHALL, when the operator presses `Enter` while focus is `RAIL` on a bindable row, move focus into that selection's primary pane: a coordinator row → its `HERA` pane (focus `COORD`); an agent row → its `AGENT` pane; a freelancer → its `AGENT` pane. On a header/expando row (Freelance or Archive), `Enter` MUST toggle the section fold instead. `space` MUST always fold/unfold (never enter a pane). Focus traversal (`^→`/`^←`) MUST step through only the panes present in the current mode.

#### Scenario: Enter on a coordinator enters the HERA pane

- **WHEN** focus is `RAIL` and a coordinator row is selected and the operator presses `Enter`
- **THEN** focus MUST move to the full-width `HERA` (COORD) pane

#### Scenario: Enter on an agent enters the AGENT pane

- **WHEN** focus is `RAIL` and an agent row is selected and the operator presses `Enter`
- **THEN** focus MUST move to the `AGENT` pane

#### Scenario: Traversal skips absent panes

- **WHEN** focus is `RAIL` on a coordinator (no AGENT pane present) and the operator presses `^→`
- **THEN** focus MUST move to `HERA` (COORD) AND a further `^→` MUST NOT reach a non-existent AGENT pane

### Requirement: Archive, delete, and prune are distinct removal verbs

The system SHALL provide three distinct removal actions on the rail: `a` toggles archive on the selected coordinator/agent (reversible; moves it into the appropriate Archive expando per the rail requirement). `^d` deletes the selected coordinator/agent — destroying its argus task, git worktree, and branch — behind a destructive confirmation that names the target (and warns when it has child agents). `^r` prunes all completed agents fleet-wide — removing finished tasks and cleaning their worktrees/branches — behind a confirmation, mirroring argus's prune-completed. Neither `^d` nor `^r` MUST perform any destructive operation without explicit confirmation.

#### Scenario: Archive is reversible and moves to the fold

- **WHEN** the operator presses `a` on an active agent
- **THEN** the agent MUST move into its coordinator's Archive expando AND pressing `a` on it again MUST restore it to the active list

#### Scenario: Delete confirms before destroying the worktree

- **WHEN** the operator presses `^d` on an agent
- **THEN** a confirmation naming the agent MUST appear AND no task/worktree/branch deletion MUST occur until the operator confirms

#### Scenario: Prune confirms and targets only completed agents

- **WHEN** the operator presses `^r`
- **THEN** a confirmation listing the completed agents to remove MUST appear AND only agents in the completed state MUST be pruned on confirm

### Requirement: Status, open-PR, scroll, and in-pane navigation keys

The system SHALL support, mirroring argus's keymap now that argus surrenders the full keyboard: `s` / `S` advance / revert the selected row's argus task status (pending → in_progress → in_review → complete) — an agent/worker row steps its bound task; an orchestrator header steps the orchestrator's coord role's bound task (mirroring how `a` on a header addresses the orchestrator); a freelance row (an unmanaged argus task with no hera role/binding) steps the argus task directly by task id, bypassing the hera binding lookup. Status stepping is INDEPENDENT of archive state: a role's bound task MUST be resolved from its live binding when one exists, FALLING BACK to the role's most recent binding (by `started_at`) regardless of `ended_at` — archiving a task ends its binding while preserving its `argus_task_id`, so `s`/`S` against an archived row MUST still step that task rather than erroring. Only when the role has no binding at all (or the resolved binding carries no argus task id) MUST `s`/`S` surface a clear error naming the condition (e.g. "role has no argus task recorded"); `^p` opens a pull request for the selected agent's, coordinator's, OR freelancer's task via the host git flow — an agent/worker row resolves to that role's bound task, a coordinator selection (root orchestrator header or a sub-coordinator role) resolves to the coordinator's bound argus task, and a freelance row (an unmanaged argus task with no hera role/binding) resolves to the argus task's own worktree (the same way argus opens a PR for that task); `⇧↑` / `⇧↓` scroll the focused pane's scrollback; `⌘↑` / `⌘↓` (or `^↑`/`^↓`) move the rail selection to the next/previous agent while focus remains inside a pane (re-entering the new selection's primary pane), so the operator can flip through agents without returning to the rail.

#### Scenario: s/S step the selected agent's status

- **WHEN** focus is `RAIL` on an agent and the operator presses `s`
- **THEN** the agent's argus task status MUST advance one step (and `S` MUST revert one step)

#### Scenario: s/S on a freelancer step the argus task directly

- **WHEN** focus is `RAIL` on a freelance row whose argus task is `T9` and the operator presses `s`
- **THEN** `T9`'s argus status MUST advance one step, resolved by task id with no hera binding lookup (and `S` MUST revert one step)

#### Scenario: s/S on an orchestrator header step the coord task

- **WHEN** focus is `RAIL` on an orchestrator header whose coord role is bound to argus task `C1` and the operator presses `s`
- **THEN** `C1`'s argus status MUST advance one step, resolved via the coord role's live binding (and `S` MUST revert one step)

#### Scenario: s/S on an archived row fall back to the latest ended binding

- **WHEN** focus is `RAIL` on an archived role row whose only binding was ended by the archive (`end_reason='argus_archived'`) and records argus task `T1`, and the operator presses `s`
- **THEN** `T1`'s argus status MUST advance one step, resolved via the role's most recent binding — the ended binding MUST NOT produce a "no live binding" error

#### Scenario: s/S with no binding at all surface a clear error

- **WHEN** focus is `RAIL` on a role row that has never had a binding and the operator presses `s`
- **THEN** a dismissible error MUST appear stating the role has no argus task recorded AND no argus call MUST be issued

#### Scenario: `^p` on a coordinator opens a PR for the coord's task

- **WHEN** focus is `RAIL` on a coordinator selection (a root orchestrator header or a sub-coordinator row) bound to a coord argus task and the operator presses `^p` and confirms
- **THEN** the host git flow MUST open a pull request from the coordinator's bound task's worktree (not a no-op)

#### Scenario: `^p` on a freelancer opens a PR for that freelancer's argus-task branch from its worktree

- **WHEN** focus is `RAIL` on a freelance row (an unmanaged argus task with no hera role/binding) whose argus task has a worktree path and the operator presses `^p` and confirms
- **THEN** the host git flow MUST open a pull request from the freelancer's argus-task worktree (not a no-op), resolving the worktree from argus's task list rather than a hera binding

#### Scenario: In-pane navigation keeps focus in a pane

- **WHEN** focus is inside a pane and the operator presses `⌘↓`
- **THEN** the rail selection MUST move to the next agent AND focus MUST remain in a pane bound to the new selection (not return to `RAIL`)

#### Scenario: Shift-arrows scroll the focused pane

- **WHEN** focus is inside a pane and the operator presses `⇧↑` / `⇧↓`
- **THEN** the focused pane's terminal scrollback MUST scroll up / down without moving the rail selection

### Requirement: Rail mutations never block the tview event loop

The system SHALL execute every rail mutation's blocking work (argus HTTP calls, DB-cascade operations, worktree removal, PR creation) OFF the tview event loop. A mutation key handler MUST return to the event loop before the mutation's blocking work executes: the handler captures the current rail selection synchronously, hands the operation to a background goroutine, and that goroutine bounces ALL subsequent UI work (rail repopulation, error modals, confirm-result handling) back through the event-loop queue. Modal button callbacks (confirm/submit handlers) run on the event loop and MUST follow the same hand-off pattern for their own blocking work. The UI MUST remain responsive (keys processed, panes repainting) while a mutation is in flight, and the rail MUST repaint after the mutation completes. While a mutation's blocking work is in flight, a second mutation key MUST NOT fire a concurrent conflicting operation — it MUST no-op with visible feedback instead.

#### Scenario: Mutation key returns before the argus call completes

- **WHEN** the operator presses `s` (or `a`, `S`, or confirms a `^d`/`^r` modal) against a row whose argus call is slow to respond
- **THEN** the key handler MUST return to the event loop before the argus call completes AND the UI MUST continue to process keys and repaint while the call is in flight

#### Scenario: Rail repaints after an in-flight mutation completes

- **WHEN** a rail mutation's background work completes successfully
- **THEN** the rail MUST repopulate via the event-loop queue, reflecting the mutation's result, without the operator pressing another key

#### Scenario: Mutation failure surfaces without freezing

- **WHEN** a rail mutation's background work returns an error
- **THEN** an error modal MUST appear via the event-loop queue AND the UI MUST NOT have been blocked at any point

#### Scenario: Second mutation while one is in flight no-ops with feedback

- **WHEN** a mutation's blocking work is in flight and the operator presses another mutation key whose operation would execute blocking work
- **THEN** the second operation MUST NOT fire AND the operator MUST receive visible feedback that an operation is already in flight

### Requirement: Non-applicable mutation keys give visible feedback

The system SHALL give visible feedback (a dismissible message naming the key and why it does not apply) whenever a RAIL-focus mutation key is pressed on a selection it cannot act on — e.g. `s`/`S` or `a` on a separator/expando row, `s`/`S` on an orchestrator header with no coord role, `^p` on a selection with no resolvable worktree, `r`/`^d` on a freelance row (freelancers are argus tasks with no hera role to rename or delete), or `w` on a selection that cannot resolve a target coordinator (a freelance row, a separator/expando row, an orchestrator header with no coord role, a sub-coordinator row with no child orchestrator, or a leaf role row with no owning coordinator). A RAIL-focus mutation key MUST NOT be a silent no-op.

#### Scenario: s on a non-addressable row gives feedback

- **WHEN** focus is `RAIL` on a non-addressable row (e.g. the Archive separator) and the operator presses `s`
- **THEN** a dismissible message MUST appear indicating the key does not apply to that row AND no argus or DB call MUST be issued

#### Scenario: a on a non-addressable row gives feedback

- **WHEN** focus is `RAIL` on a non-addressable row and the operator presses `a`
- **THEN** a dismissible message MUST appear indicating the key does not apply to that row AND no argus or DB call MUST be issued

#### Scenario: ^d on a freelancer gives feedback instead of a dead-end error

- **WHEN** focus is `RAIL` on a freelance row and the operator presses `^d`
- **THEN** a dismissible message MUST explain that delete does not apply to a freelance row AND no destructive operation MUST be issued

#### Scenario: w on a coordinator-shaped row with no resolvable target gives feedback

- **WHEN** focus is `RAIL` on a coordinator-shaped row that cannot resolve a target coordinator (an orchestrator header with no coord role, OR a sub-coordinator row with no child orchestrator) and the operator presses `w`
- **THEN** a dismissible "not applicable" notice MUST appear AND no argus or DB call MUST be issued

### Requirement: `w` spawns a worker under the selected coordinator

The system SHALL, when the operator presses `w` while focus is `RAIL`, open an input modal with a **Project** selector and a **Prompt** field. The Project selector SHALL be initialized to the coordinator's own `argus_project` and SHALL allow the operator to cycle to any other configured argus project. On confirm with a non-empty prompt, the system SHALL spawn a worker agent under the coordinator implied by the current selection, in the **selected project**, and attach it programmatically, WITHOUT requiring the spawned worker's agent to call any hera MCP tool for the attachment.

Selection resolves to a target coordinator as follows: a coordinator row (root orchestrator header OR a sub-coordinator role row) targets that coordinator; an agent/worker row whose role belongs to a coordinator targets that agent's coordinator (the orchestrator of the agent's role). The selected agent/worker row's own liveness is irrelevant to resolution: an archived or dead agent row still resolves to its (valid) coordinator and spawns. A freelance row, a separator/expando row, or any selection not attached to a coordinator MUST NOT spawn anything — it MUST surface a dismissible "not applicable" notice (never a silent no-op).

The **effective project** is the project chosen in the modal's Project selector; when the operator leaves the selector untouched it is the coordinator role's `argus_project` (the default). When the configured project list is empty, the selector MUST degrade to a visible "(no projects configured)" entry that maps to the coordinator's `argus_project` on confirm.

On confirm the system SHALL, in order: (1) resolve the target orchestrator and the effective project; (2) derive a worker role name from the prompt and make it unique among the orchestrator's non-archived roles; (3) create an argus task via `POST /api/tasks` in the effective project whose body carries the worker's prompt, AND mirror `meta:hera.role=worker` to the created task; (4) read the created task's `worktree_path` via `GET /api/tasks/{id}`; (5) insert a worker role (`kind=worker`, the effective project as its `argus_project`, `mission` set to the operator's prompt, the derived name) and a binding tying the role to the created task id and carrying the resolved `worktree_path`. The role and binding inserts MUST emit on the rail broadcaster so the rail repopulates, rendering the new worker nested under its coordinator within approximately 100 ms, and the system MUST auto-select the new worker row while leaving focus in `RAIL`. Auto-select is best-effort and broadcaster-driven: the new row is selected on the next `populateRail` in which it exists; if the row does not appear within a bounded number of repopulates, the pending selection MUST be abandoned and the abandonment logged (the cursor simply does not move), never retried unboundedly.

The created task's prompt SHALL be a short hera orientation prefix (naming the coordinator and noting the worker may report progress via `hera_send`) followed by the operator's prompt text verbatim. An empty (or whitespace-only) prompt MUST be rejected: no argus task MUST be created and no role or binding row MUST be inserted, AND the rejection MUST be operator-visible — a dismissible notice (never a silent modal close).

The spawn's blocking work (argus calls and DB inserts) MUST run off the tview event loop per the rail-mutation contract; a failure MUST surface as an error modal via the event-loop queue without freezing the UI. The argus task is created via the existing scope-token `POST /api/tasks`, so the worker branches off the effective project's default ref; no base branch is selected.

The spawn is resilient to two partial-failure modes after the argus task has been created:

- If `GET /api/tasks/{id}` (step 4) fails, the system MUST still insert the worker role and binding (with an empty `worktree_path`) and log the failure; the spawn MUST NOT be aborted. The worker is reachable and managed; worktree-dependent operations (`^d`, `^p`, resize) soft-skip an empty path.
- If the role or binding insert (step 5) fails after the argus task was created, the system MUST NOT delete the created argus task. It MUST log the orphaned task id and surface the error; the live argus task survives and appears in the rail's Freelance section (recoverable), rather than being destroyed by a fallible rollback.

#### Scenario: `w` in RAIL opens the spawn-worker modal on a coordinator

- **WHEN** focus is `RAIL`, a coordinator row (root or sub) is selected, and the operator presses `w`
- **THEN** the view MUST open an input modal with a Project selector and a Prompt field

#### Scenario: Project selector defaults to the coordinator's project

- **WHEN** focus is `RAIL`, a coordinator whose coord role's `argus_project` is `foo-frontend` is selected, and the operator presses `w`
- **THEN** the modal's Project selector MUST be initialized to `foo-frontend`

#### Scenario: `w` resolves an agent selection to its coordinator

- **WHEN** focus is `RAIL`, an agent/worker row under coordinator `foo` is selected, the operator presses `w`, and confirms the modal with prompt `implement X`
- **THEN** the spawned worker role MUST be created under orchestrator `foo` (the selected agent's coordinator)

#### Scenario: `w` resolves an archived or dead agent row to its coordinator

- **WHEN** focus is `RAIL`, an archived (or dead) agent/worker row under coordinator `foo` is selected, the operator presses `w`, and confirms with a non-empty prompt
- **THEN** the worker MUST be spawned under coordinator `foo` (the selected row's archived/dead state MUST NOT block resolution to its still-valid coordinator)

#### Scenario: `w` on a non-coordinator selection gives feedback

- **WHEN** focus is `RAIL`, a freelance row OR a separator/expando row is selected, and the operator presses `w`
- **THEN** a dismissible "not applicable" notice MUST appear AND no argus or DB call MUST be issued

#### Scenario: Empty prompt is rejected with a visible notice

- **WHEN** the operator confirms the spawn-worker modal with an empty (or whitespace-only) prompt
- **THEN** a dismissible "prompt is required" notice MUST appear (NOT a silent modal close) AND no argus task MUST be created AND no role or binding row MUST be inserted

#### Scenario: Untouched selector spawns in the coordinator's project

- **WHEN** the operator confirms the spawn-worker modal against coordinator `foo` whose coord role's `argus_project` is `foo-frontend` WITHOUT cycling the Project selector, with prompt `build the sidebar`
- **THEN** the daemon MUST issue a `POST /api/tasks` with project `foo-frontend` AND MUST mirror `meta:hera.role=worker` to the created task AND the inserted worker role's `argus_project` MUST be `foo-frontend`

#### Scenario: Cycled selector spawns in the chosen project

- **WHEN** the operator confirms the spawn-worker modal against coordinator `foo` (coord project `foo-frontend`) after cycling the Project selector to `foo-backend`, with prompt `build the API`
- **THEN** the daemon MUST issue a `POST /api/tasks` with project `foo-backend` AND the inserted worker role's `argus_project` MUST be `foo-backend`

#### Scenario: Empty project list degrades to the coordinator's project

- **WHEN** the configured argus project list is empty and the operator confirms the spawn-worker modal against coordinator `foo` (coord project `foo-frontend`) with a non-empty prompt
- **THEN** the Project selector MUST have shown a "(no projects configured)" entry AND the daemon MUST issue a `POST /api/tasks` with project `foo-frontend`

#### Scenario: Role and binding are inserted programmatically with the worktree path

- **WHEN** the spawn-worker confirm creates argus task `T9` whose `GET /api/tasks/T9` reports `worktree_path=/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar`
- **THEN** hera MUST insert a worker role under coordinator `foo`'s orchestrator AND a binding tying that role to `T9` with `worktree_path=/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar` — without the worker's agent calling any hera MCP tool

#### Scenario: GetTask failure still binds with an empty worktree path

- **WHEN** the spawn-worker confirm successfully creates the argus task but the subsequent `GET /api/tasks/{id}` fails
- **THEN** hera MUST still insert the worker role and a binding with an empty `worktree_path` AND log the failure AND MUST NOT abort the spawn

#### Scenario: Insert failure after task creation does not delete the argus task

- **WHEN** the spawn-worker confirm successfully creates the argus task but the subsequent role or binding insert fails
- **THEN** hera MUST NOT issue a `DELETE` for the created argus task AND MUST log the orphaned task id AND surface the error (the orphaned task survives as a freelancer)

#### Scenario: Role name is derived from the prompt and uniqued

- **WHEN** the operator spawns a worker under an orchestrator that already has a non-archived role named `build-the-sidebar`, with a prompt that also derives to `build-the-sidebar`
- **THEN** the new role's name MUST be a suffixed variant (e.g. `build-the-sidebar-2`) so it does not collide with the existing non-archived sibling

#### Scenario: Worker prompt carries an orientation prefix

- **WHEN** the operator confirms the spawn-worker modal with prompt `migrate the schema`
- **THEN** the created argus task's prompt MUST be a hera orientation prefix (naming the coordinator) followed by `migrate the schema`

#### Scenario: New worker renders nested and is auto-selected

- **WHEN** the spawn-worker confirm completes successfully
- **THEN** the rail MUST render the new worker as a child row under its coordinator within approximately 100 ms AND the rail selection MUST move to the new worker row AND focus MUST remain `RAIL`

#### Scenario: Auto-select is abandoned if the new row never appears

- **WHEN** a worker spawn queues an auto-select but the new row never appears in the rail across a bounded number of `populateRail` repopulates
- **THEN** the pending auto-select MUST be abandoned and the abandonment logged, AND the rail MUST NOT keep retrying the selection unboundedly

#### Scenario: Spawn failure surfaces without freezing

- **WHEN** the spawn-worker background work returns an error (e.g. the argus `POST /api/tasks` fails)
- **THEN** an error modal MUST appear via the event-loop queue AND the UI MUST NOT have been blocked

### Requirement: In-review status glyph

The system SHALL render `theme.IconReview` (clipboard-check, U+0F00BC) for the `in_review` argus task status, with `theme.StyleInReview` (blue). The historical `theme.IconMoonStars` mapping for `in_review` is superseded by this requirement. The `in_review` status MUST NOT render a checkmark (`✓`); only `complete` renders a checkmark.

#### Scenario: in_review renders the clipboard-check glyph

- **GIVEN** a rail row whose bound argus task has status `in_review`
- **THEN** the status glyph MUST be `theme.IconReview` (U+0F00BC, clipboard-check) rendered in `theme.StyleInReview` (blue)
- **AND** the glyph MUST NOT be `theme.IconMoonStars` or `✓`

### Requirement: Role-row base indentation alignment

All role rows (leaf workers and sub-coordinators) at tree depth N MUST render their leading status icon at column `cx + N × indentStep`, with NO additional leading offset. A leading space prefix that historically shifted leaf worker icons 1 column right of sub-coordinator icons at the same depth is eliminated. Direct members of an orchestrator — regardless of whether a sibling is an expanded nested coordinator — MUST render at the orchestrator's child indent.

#### Scenario: Sibling worker aligns with sub-coordinator at same depth

- **GIVEN** an orchestrator with a sub-coordinator (a worker role that is also another orchestrator's coord) and a sibling leaf worker, both at tree depth 1
- **WHEN** the sub-coordinator is expanded
- **THEN** the sibling worker's status icon MUST appear at the same horizontal column as the sub-coordinator's status icon (both at `cx + 1 × indentStep`)
- **AND** the sibling worker's status icon MUST NOT appear at the depth-2 column (`cx + 2 × indentStep`)

### Requirement: PR review indicator cell

The system SHALL render a per-row GitHub PR review indicator glyph on role rows whose bound argus task carries an actionable PR review state. The indicator appears after the status icon and before the role name, using the following glyph mapping:

| pr_state string    | glyph                  | style                  |
|--------------------|------------------------|------------------------|
| `awaiting-review`  | `theme.IconPRAwaiting` | `theme.StylePRAwaiting` |
| `changes-requested`| `theme.IconPRChanges`  | `theme.StylePRChanges`  |
| `approved`         | `theme.IconPRApproved` | `theme.StylePRApproved` |

The PR indicator cell:
- MUST only consume 2 columns of horizontal space when the state is actionable; for non-actionable states (`none`, `draft`, `merged-closed`, `unknown`, or absent) the cell MUST be omitted and the name column reclaims the space.
- MUST NOT block rail rendering on per-row argus HTTP calls; the PR state MUST be read from the existing poll-and-cache mechanism used for task status (`ArgusStateCache`).
- MUST degrade gracefully (no cell, no error) when argus does not populate the `pr_state` field on the task DTO.
- Applies to managed worker/coordinator role rows only; freelance repo-group headers and Archive expandos carry no PR indicator.

#### Scenario: Actionable PR state renders glyph and consumes width

- **GIVEN** a role row whose bound task has PR state `awaiting-review`, `changes-requested`, or `approved`
- **THEN** the corresponding PR glyph MUST render after the status icon, using the correct style from the mapping above
- **AND** the glyph cell MUST consume 2 horizontal columns (shifting the name right)

#### Scenario: Non-actionable PR state renders no cell

- **GIVEN** a role row whose bound task has PR state `none`, `draft`, `merged-closed`, `unknown`, or no pr_state
- **THEN** NO PR glyph cell MUST render
- **AND** the name column MUST start immediately after the status icon (reclaiming the 2 columns)

#### Scenario: No pr_state field degrades gracefully

- **GIVEN** a role row whose argus task has no `pr_state` field (e.g. argus daemon is older)
- **THEN** the rail MUST render the row without error and without a PR indicator cell

### Requirement: Coordinators with zero non-archived children default collapsed

The rail SHALL derive each coordinator's DEFAULT fold state from its non-archived child count: a coordinator (orchestrator root or nested sub-coordinator) with zero non-archived child agents defaults COLLAPSED (`▸` chevron, children hidden — including its `Archive (N)` expando, per the existing fold semantics under which a collapsed coordinator renders no child rows); a coordinator with one or more non-archived child agents defaults EXPANDED (`▾`). "Non-archived" is the existing active/archived partition: hera-archived, argus-archived, and dead children all count as archived.

The default applies ONLY to coordinators the operator has not explicitly toggled this session. An explicit fold toggle (`space`, or the Enter-on-header fold) SHALL override the default in either direction and persist for the session: toggling a default-collapsed empty coordinator expands it (revealing its `Archive (N)` expando as today), toggling it again re-collapses it, and a manually-collapsed coordinator with active children stays collapsed across rail rebuilds.

While `l`/showArchived is active, the collapsed DEFAULT SHALL be overridden (an untouched empty coordinator renders expanded so its archived children are reachable through its force-expanded `Archive (N)` expando); an explicit manual collapse still wins over `l`. The Freelance section and the top-level `Archive` expando keep their existing fold defaults, unaffected by this rule.

#### Scenario: Empty coordinator defaults collapsed

- **WHEN** the rail renders a coordinator with zero non-archived child agents (no children at all, or only archived/dead children) that the operator has not toggled
- **THEN** the coordinator row MUST render with the collapsed chevron (`▸`) AND none of its child rows — including its `Archive (N)` expando — may render

#### Scenario: Coordinator with an active child defaults expanded

- **WHEN** the rail renders a coordinator with at least one non-archived child agent that the operator has not toggled
- **THEN** the coordinator row MUST render with the expanded chevron (`▾`) AND its active children MUST render as today

#### Scenario: Manual toggle overrides the default and persists

- **WHEN** the operator toggles the fold of a default-collapsed empty coordinator (`space` or Enter-on-header)
- **THEN** the coordinator MUST expand (revealing its `Archive (N)` expando when it has archived children) AND that explicit state MUST persist across rail rebuilds until toggled again; symmetrically a manually-collapsed coordinator with active children MUST stay collapsed across rebuilds

#### Scenario: `l` force-reveal overrides the collapsed default while active

- **WHEN** showArchived (`l`) is active and the rail renders an untouched coordinator with zero non-archived children
- **THEN** the coordinator MUST render expanded with its `Archive (N)` expando force-expanded so its archived children are visible, AND a coordinator the operator explicitly collapsed MUST remain collapsed

### Requirement: Task status never buckets a rail row

Task STATUS SHALL NOT affect rail bucketing. A row buckets into an Archive expando (or the top-level Archive section) ONLY when at least one of the following holds: the hera role is archived (`archived_at` set), the bound argus task is archived (argus `archived` flag), or the argus task RECORD no longer exists (404 / pruned — a state-cache miss once the cache is warm). The `Dead` classification SHALL mean record-nonexistence ONLY; a task in any terminal STATUS (`complete`, `failed`, `stopped`, …) whose argus record still exists is NOT dead.

A completed task whose argus record still exists MUST render in the ACTIVE tree — among its coordinator's active children, in its freelance repo group, or as a live coordinator header — with its status glyph (green `✓` for `complete`, per the status-glyph table), selectable and bindable like any active row.

A coordinator binding whose task is completed-but-existing MUST still feed the orchestrator header's coord task binding (`CoordTaskID`) and status (`CoordStatus`), so the header renders the `✓` glyph and its pane shows the coordinator's last output; its children's bucketing is unaffected. Only an archived or record-gone coord binding is skipped as a tombstone.

Deadness MUST be derived from the argus state cache snapshot (the same source that drives row icons), not from per-row status-classifying calls: the rail rebuild path MUST NOT issue a synchronous argus HTTP request per row. A cold cache MUST NOT transiently classify rows dead. Status-based aliveness MAY still inform initial pane selection (preferring a live task at session start), but never bucketing.

#### Scenario: Stepping a task to complete keeps it in the active children

- **WHEN** a worker row's bound argus task transitions to status `complete` while its argus record still exists with `archived=0` and the hera role's `archived_at` is NULL
- **THEN** the row MUST remain among its coordinator's active children rendering the green `✓` glyph, MUST NOT move into the coordinator's `Archive (N)` expando, and MUST NOT be classified `Dead`

#### Scenario: Completed coordinator header stays bound and checked

- **WHEN** an orchestrator's coord binding points at an argus task with status `complete` whose record still exists and is not archived
- **THEN** the orchestrator header MUST carry that task as its coord binding (`CoordTaskID` set, `CoordStatus` complete, rendering `✓`) AND the header and its children MUST NOT be hidden or re-bucketed on account of the status

#### Scenario: Completed freelancer stays in its repo group

- **WHEN** an unmanaged argus task in the Freelance section has status `complete` and is not archived
- **THEN** it MUST remain visible in its repo group (counted in the group's `(N)`) rendering `✓`, without `l`

#### Scenario: Record-gone task still buckets as dead

- **WHEN** a role's bound argus task id is absent from the warm argus state cache (the record was pruned / returns 404)
- **THEN** the row MUST be classified `Dead` and bucket into its coordinator's Archive expando (hidden in the default view, dimmed under `l`)

#### Scenario: Rail rebuild issues no per-row argus HTTP

- **WHEN** the rail repopulates (any rebuild trigger) with N live-bound roles
- **THEN** deadness MUST be read from the state-cache snapshot; the rebuild MUST NOT perform a per-row `GET /api/tasks/{id}` aliveness probe on the event loop

### Requirement: Coordinator selection renders a right-side Details pane

When the rail selection is a coordinator — an orchestrator root header or a nested sub-coordinator — the body SHALL compose a right-side Details pane alongside the rail and the HERA pane (layout `rail | HERA | Details`). The HERA pane SHALL keep the majority of the width available between the rail and the right edge, so the Details pane never starves the HERA pane at narrow terminals. The Details pane SHALL NOT replace or displace the rail or the HERA pane.

The Details pane SHALL render, for the selected coordinator, fields derived from data hera already stores:

- **Name** of the coordinator.
- **Status** rendered with the same status glyph and state the rail shows for that coordinator (the shared status-icon vocabulary), so the Details status and the rail status never disagree.
- **Created** — the orchestrator's creation time.
- **Last agent activity** — the most recent activity timestamp across the coordinator's roles (role creation, their bindings' start/end, and their status updates).
- **Mission** and **Constraints** — the coordinator role's stored mission and constraints (empty when unset).
- **Repos in scope** — the distinct argus projects across the coordinator's roles.
- **Agent roster** — each of the coordinator's child roles by name and kind with its current rail-displayed status, including any nested sub-coordinators.

The Details pane SHALL include a clearly-marked placeholder for a future inferred description/goal/scope summary; that inference SHALL NOT be implemented by this change.

#### Scenario: Details pane appears on coordinator selection

- **WHEN** the operator selects a coordinator row (orchestrator root header or a nested sub-coordinator)
- **THEN** the body MUST compose the rail, the full-width HERA pane, AND a right-side Details pane (three columns), with the rail and HERA pane still present

#### Scenario: Details fields are rendered from available data

- **WHEN** the Details pane renders for a selected coordinator
- **THEN** it MUST show the coordinator's name, a status indicator matching the rail's status glyph for that coordinator, its created time, its last agent activity, its mission and constraints, the distinct repos across its roles, and a roster of its child roles (name, kind, current status) including sub-coordinators

#### Scenario: Agent and freelancer selections are unchanged

- **WHEN** the operator selects an agent (worker) row or a freelancer row
- **THEN** the body MUST compose exactly as before — `rail | HERA | AGENT` for an agent and `rail | AGENT` for a freelancer — and MUST NOT render the Details pane

#### Scenario: Sub-coordinator Details describe the sub-coordinator's own group

- **WHEN** the operator selects a nested sub-coordinator row
- **THEN** the Details pane MUST render the metadata of the sub-coordinator's own orchestration group (its name, mission, repos, and roster), not the parent coordinator's

### Requirement: Live coordinators never render ✓/complete

A LIVE coordinator (non-archived, not in the mixed-coord repair state) MUST
NOT render the `✓ complete` glyph or the "complete" status label in either the
rail glyph or the Details pane "Status:" field, regardless of the underlying
argus task status.

Argus auto-completes coordinator tasks when the agent session goes idle
(waiting for the human). This is an argus-internal lifecycle event that does not
mean the coordinator is done. Hera MUST mask this: when a live coordinator's
argus task status is `complete`, hera MUST display the status as `in_progress +
idle` (rendering the idle moon ☾ glyph, label "idle") in both the rail header
icon and the Details pane. The raw `CoordStatus` field MAY retain the argus
value internally; the masking is display-layer only.

The `orchIcon` function applies this masking so the rail header never shows `✓`
for a live coordinator. The `buildCoordDetails` function applies the same
masking so the Details pane status field agrees with the rail.

Archived coordinators are exempt — a coordinator rendered in the Archive section
may show its true argus status (including `✓`) because it represents a completed
and finished session.

#### Scenario: Live coordinator with argus-complete task renders idle glyph

- **WHEN** a live (non-archived) coordinator's argus task reports status
  `complete`
- **THEN** the rail header MUST render the idle moon glyph (☾, `theme.IconMoonOutline`)
  with `theme.StyleInReview` — NOT the `✓` checkmark
- **AND** the Details pane "Status:" field MUST show "idle", NOT "complete"

#### Scenario: Archived coordinator with complete task still shows ✓

- **WHEN** an archived coordinator's argus task reports status `complete`
- **THEN** the rail header MUST render `✓` with `theme.StyleDimmed` (consistent
  with all other archived rows)

### Requirement: Coordinator roles excluded from ^r prune targets

`ops.ListCompletedAgents` (the `^r` prune eligibility query) MUST skip roles
whose kind is `coordinator`. A coordinator role whose argus task reports
`complete` MUST NOT appear in the prune confirmation list, because argus
auto-completion does not indicate the coordinator is safe to destroy.

#### Scenario: Complete coordinator is not listed as a prune target

- **WHEN** a coordinator role's live binding's argus task reports status
  `complete`
- **THEN** `ListCompletedAgents` MUST NOT include that role in its result
- **AND** a worker role in the same orchestrator whose task is also `complete`
  MUST still be listed (the guard is coordinator-specific)

### Requirement: Mixed-coord headers render a repair cue and `a` repairs first

The system SHALL detect the **mixed-coord state**: an orchestrator that DISPLAYS as active (hera `archived_at` NULL, header rendered in the active tree) while its coord-pane binding's argus task is ARCHIVED on the argus side (the argus state cache reports `archived` for the header's coord task). Only external argus-side archiving produces this state; it leaves the header's coordinator broken — the coord task is a tombstone argus hides — without any hera-side flag recording it.

The system SHALL render a DISTINCT visible cue on a mixed-coord header: the header's status icon MUST render `⊘` (U+2298, circled division slash) in the error style (red), replacing the normal status glyph, so the operator can SEE "this coord is broken/archived" at a glance. The cue MUST be distinct from the needs-input `?`, from the dimmed-archived treatment, and from the 󰹻 coordinator marker (which keeps rendering beside it). The cue applies ONLY to the mixed state: an orchestrator that is itself archived renders the normal dimmed-archived treatment regardless of its coord task's argus flag, and a header whose coord task is not argus-archived renders its normal status glyph.

The system SHALL make `a` REPAIR-FIRST on a mixed-coord header: pressing `a` (focus `RAIL`, mixed-coord header selected) MUST invoke argus's unarchive endpoint on the header's coord task — directly by task id, with no hera DB write — aligning argus reality to the displayed active orchestrator, and MUST NOT cascade-archive the orchestrator. An argus 404 (task pruned) is tolerated as a skip, per the existing archive-tolerance rule. Once the coord task is unarchived (or whenever the header is NOT in the mixed state), `a` MUST behave exactly as the standard orchestrator toggle (cascade-archive when displayed active, unarchive when displayed archived). The icon cue reverting to the normal status glyph on the post-repair rail refresh is the primary repair feedback. Enter on a mixed-coord header keeps its current behavior (no new flow) — the repair-first `a` is the affordance.

#### Scenario: Mixed-coord header renders the ⊘ repair cue

- **WHEN** an active orchestrator's coord-pane binding task is reported archived by the argus state cache
- **THEN** the header's status icon MUST render `⊘` in the error style instead of the coord task's status glyph, while the 󰹻 coordinator marker and the header's name render as usual

#### Scenario: Archived orchestrator does not render the cue

- **WHEN** an orchestrator is itself archived (hera `archived_at` set) and its coord task is also argus-archived
- **THEN** the header MUST render the normal dimmed-archived treatment, NOT the `⊘` cue (the state is not mixed — both sides agree)

#### Scenario: `a` on a mixed-coord header unarchives the coord task instead of cascade-archiving

- **WHEN** the operator presses `a` against a mixed-coord header whose coord task is `T1`
- **THEN** hera MUST issue the argus unarchive endpoint for `T1` directly (no hera DB write) AND MUST NOT archive the orchestrator or any of its roles

#### Scenario: Repaired header cascades as before

- **WHEN** the operator presses `a` against an active orchestrator header whose coord task is NOT argus-archived
- **THEN** hera MUST run the standard cascade-archive (orchestrator + roles), exactly as the `a`-toggle requirement specifies

### Requirement: Pane byte streams are scrubbed of OSC sequences before emulation

The system SHALL remove every OSC sequence (`ESC ]` … terminator, any OSC code: 0/1/2/8/…) from a pane's byte stream BEFORE the bytes reach the pane's terminal emulator, so an OSC payload (e.g. a Claude Code set-title string) is never painted as printable text in the pane. The scrub MUST apply to the FULL stream a pane ingests: the ring snapshot delivered at bind time AND every subsequent live byte chunk.

The scrubber MUST recognize BOTH OSC terminators — BEL (`0x07`) and 7-bit ST (`ESC \`) — and MUST drop the terminator together with the payload. CAN (`0x18`) and SUB (`0x1a`) cancel an in-flight OSC; an ESC that begins a new sequence inside an OSC payload cancels the OSC and the new sequence MUST be processed normally. A 0x9C byte inside an OSC payload MUST NOT be treated as a terminator (it may be a UTF-8 continuation byte — e.g. in Claude's "✳" spinner title — and treating it as C1 ST is the upstream parser bug that leaks the payload).

The scrubber MUST be stateful across chunk boundaries: an OSC sequence split across two or more chunks (including the snapshot→live boundary, and a terminator's `ESC` and `\` arriving in different chunks) MUST still be removed in full. A lone trailing `ESC` at the end of a chunk MUST be held until the next chunk decides whether it introduces an OSC.

Non-OSC escape sequences (CSI colors, cursor movement, SGR, etc.) and all ordinary printable bytes MUST pass through byte-for-byte unchanged. The scrub MUST be bounded: a pathological OSC that never terminates MUST stop being dropped after a fixed cap so the filter can never swallow a stream unboundedly.

#### Scenario: Inline OSC title with BEL terminator is not painted

- **WHEN** a pane's stream contains `before` + `ESC ] 0 ; My Title BEL` + `after` within a single chunk
- **THEN** the bytes delivered to the pane's emulator MUST be exactly `beforeafter` — no byte of the OSC introducer, payload, or BEL terminator may reach the emulator

#### Scenario: OSC with ST terminator is removed

- **WHEN** a pane's stream contains `ESC ] 2 ; My Title ESC \` between ordinary text
- **THEN** the entire sequence including the two-byte ST terminator MUST be removed and the surrounding text delivered unchanged

#### Scenario: OSC split across chunk boundaries is removed

- **WHEN** an OSC sequence is split across two chunks (e.g. chunk 1 ends mid-payload, or chunk 1 ends with the terminator's `ESC` and chunk 2 begins with `\`), including the case where chunk 1 is the bind-time snapshot and chunk 2 is the first live chunk
- **THEN** no byte of the sequence may reach the emulator, and bytes after the terminator MUST be delivered unchanged

#### Scenario: Non-OSC escape sequences survive the scrub

- **WHEN** a pane's stream contains SGR color codes and cursor-movement CSI sequences (e.g. `ESC [ 3 1 m`, `ESC [ 6 G`) around an OSC title sequence
- **THEN** every non-OSC escape sequence MUST be delivered to the emulator byte-for-byte unchanged while the OSC sequence is removed

#### Scenario: 0x9C inside an OSC payload does not terminate it

- **WHEN** an OSC payload contains a multi-byte UTF-8 glyph whose encoding includes a `0x9C` byte (e.g. `✳` = `E2 9C B3`), followed by more payload and then a BEL
- **THEN** the scrub MUST continue dropping through the `0x9C` and terminate only at the BEL, so no payload byte after the `0x9C` leaks to the emulator

### Requirement: `J` adopts a freelancer into a chosen coordinator

While the RAIL is focused and the selection is a freelancer row (an unmanaged argus task with no hera role or live binding, rendered in the Freelance section), pressing `J` SHALL open a target picker listing the active (non-archived) orchestrators. The picker SHALL be a themed, focusable, dismissable modal in which `Enter` selects the highlighted orchestrator and `Esc` cancels without change. The picker SHALL identify each orchestrator by its name and SHALL name the freelancer being adopted in its title. When no active (non-archived) orchestrator exists, pressing `J` SHALL surface visible feedback that a coordinator must be created first and SHALL NOT open the picker or create any role or binding.

Selecting an orchestrator SHALL adopt the freelancer into it by creating, server-side and without any agent action:

- a `worker` role under the chosen orchestrator, whose name defaults to the freelancer's argus task name and is de-collided (a numeric suffix is appended) when an active role of that name already exists under the orchestrator. The role SHALL record the freelancer's argus repo as its `argus_project` (write-once, consistent with roles created via the bootstrap flow), carried from the rail selection; and
- a live binding from the freelancer's argus task to that role. The binding SHALL record the freelancer's argus-task worktree path, so the adopted row's `^p` open-PR and pane operations resolve the same worktree the freelancer used.

This role-and-binding creation SHALL reuse the same creation path that `hera_join`'s attach-mode uses (the shared DAO `Roles.Create` + `Bindings.Create`), not a duplicate implementation. The freelancer's argus task SHALL be best-effort stamped `meta:hera.role=worker` for parity with `hera_join`; a transient failure to stamp the meta SHALL NOT undo or fail the binding.

After adoption the row SHALL leave the Freelance section and render as a worker under the chosen coordinator. The adopted agent SHALL remain independent: the binding exists immediately without the agent acting, and the agent MAY later `hera_join(cwd)` to claim the role and receive coordinator messages.

The binding operation SHALL run off the tview event loop (the async-mutate pattern), so `J` never blocks or deadlocks the loop; while one adopt is in flight a second SHALL no-op with visible feedback rather than running concurrently.

`J` SHALL be RAIL-focus-only. In a pane (COORD/AGENT focus) the `J` rune SHALL forward to the bound task's PTY like any other character. The lowercase `j` navigation key SHALL be unaffected.

#### Scenario: `J` on a freelancer creates a worker binding under the chosen coordinator

- **WHEN** the operator selects a freelancer, presses `J`, and picks an orchestrator from the picker
- **THEN** a `worker` role and a live binding from the freelancer's argus task to that role MUST be created under the chosen orchestrator, and the row MUST re-render as a worker under that coordinator (out of the Freelance section)

#### Scenario: The default role name is de-collided

- **WHEN** the freelancer's task name matches an existing active role name under the chosen orchestrator
- **THEN** the adopted role MUST be created under a de-collided name (a numeric suffix appended) rather than failing or colliding with the existing role

#### Scenario: `J` on a non-freelancer row surfaces feedback

- **WHEN** the operator presses `J` while a coordinator, a managed agent, an orchestrator header, or a section row is selected
- **THEN** the view MUST surface visible feedback that only freelancers can be adopted, and MUST NOT create any role or binding (never a silent no-op)

#### Scenario: `J` on a freelancer with no argus task id surfaces feedback

- **WHEN** the operator presses `J` on a freelancer row that carries no argus task id
- **THEN** the view MUST surface visible feedback and MUST NOT create any role or binding

#### Scenario: An already-bound task is not adopted again

- **WHEN** the freelancer's argus task already has a live binding to some orchestrator (a race or mislabeled row)
- **THEN** the adopt MUST be rejected with visible feedback and MUST NOT create a second binding

#### Scenario: No active coordinator to adopt into surfaces feedback

- **WHEN** the operator presses `J` on a valid freelancer but no active (non-archived) orchestrator exists
- **THEN** the view MUST surface visible feedback that a coordinator must be created first, and MUST NOT open the picker or create any role or binding

#### Scenario: `J` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `J`
- **THEN** the `J` rune MUST be forwarded to the bound task's PTY and MUST NOT open the picker

### Requirement: The rail supports a `/` name filter

While the RAIL is focused, pressing `/` SHALL enter a rail search input mode. While in input mode, typed characters SHALL build a filter query and the rail SHALL narrow to the rows whose name matches the query by case-insensitive substring, across coordinators, agents, and freelancers. Whitespace-separated query terms SHALL each match the row name (or, for a freelance row, its repo), so every term must match for the row to be shown.

While a filter is active the rail SHALL remain ancestry-preserving and legible:

- A coordinator whose name matches, OR which has any descendant role whose name matches, SHALL remain visible so a matching agent always keeps its parent coordinator header.
- Sections (coordinators, Freelance repo groups, the Archive expando) SHALL auto-expand while a filter is active so matching rows are never hidden behind a fold.
- A section header or separator SHALL render only when it has at least one visible row beneath it, so the operator never lands on an empty section.

`Esc` while in input mode SHALL exit search and restore the full, unfiltered rail (clearing the query). `Enter` while in input mode SHALL accept the filter — keeping the query applied but leaving input mode so `j`/`k` navigate the filtered set. The active query SHALL be shown unobtrusively (a `/ <query>` input line while typing, and the active query reflected in the rail title once accepted).

While in input mode the rail's mutation keys (`n`/`r`/`a`/`l`/`s`/`S`/`P`/`?`) and the `Esc`-release-to-argus behavior SHALL NOT fire; those keystrokes are filter input instead. After the filter is accepted (input mode off), normal rail key handling SHALL resume.

#### Scenario: `/` narrows the rail to matching rows

- **WHEN** the operator presses `/` and types a query
- **THEN** the rail MUST show only rows whose name matches the query (case-insensitive substring), hiding non-matching coordinators, agents, and freelancers

#### Scenario: A matching agent keeps its parent coordinator visible

- **WHEN** a filter matches an agent whose name does not match its coordinator's name
- **THEN** the agent's parent coordinator header MUST remain visible (ancestry-preserving) and expanded so the matching agent is shown under it

#### Scenario: Esc restores the full rail

- **WHEN** the operator presses `Esc` while in search input mode
- **THEN** the filter MUST clear, input mode MUST exit, and the rail MUST render every row it showed before the filter

#### Scenario: Enter accepts the filter and returns to navigation

- **WHEN** the operator presses `Enter` while in search input mode
- **THEN** input mode MUST exit, the query MUST stay applied (the rail stays filtered), and `j`/`k` MUST navigate the filtered set

#### Scenario: Mutation keys are filter input while typing

- **WHEN** the operator is in search input mode and types a character that is otherwise a rail mutation key (such as `n` or `a`)
- **THEN** that character MUST be appended to the filter query and MUST NOT trigger the mutation

### Requirement: The selection marker is gated behind the probe env

The selected rail row's left-gutter `›` selection marker SHALL render only when the live-probe environment variable (`HERA_LIVE_PROBE`) is set. In normal operation (the variable unset), the selected row's gutter SHALL render a blank cell — no glyph — and the selection SHALL be indicated by the selected-row text style (`theme.StyleSelected`) alone. The gutter width SHALL be reserved unconditionally so that toggling the marker on or off, and moving the cursor between rows, never shifts row content horizontally.

#### Scenario: No marker in normal operation

- **WHEN** the rail renders with the probe environment variable unset
- **THEN** no `›` selection marker MUST appear on any row, and the selected row MUST still be distinguishable by its selected text style

#### Scenario: Marker present under the probe env

- **WHEN** the rail renders with the probe environment variable set
- **THEN** the `›` selection marker MUST render exactly once, on the selected row, as before

#### Scenario: Gutter does not shift content

- **WHEN** the selection moves between rows, with the marker either gated off or on
- **THEN** no row's rendered content MUST shift horizontally (the marker gutter is reserved in both states)

### Requirement: Pinned section at the rail top with a `P` toggle

The system SHALL render a `Pinned` section at the TOP of the rail, above the orchestrator list, mirroring argus's task-panel Pinned section. The section is introduced by a `Pinned` separator that is rendered ONLY when at least one pinned coordinator or pinned agent exists (so the operator never lands on an empty section). Pinned items SHALL be fully reachable by normal j/k navigation.

A coordinator (orchestrator header) or an agent (a worker or sub-coordinator role) is PINNED when its hera row carries a `pinned_at` timestamp. A pinned coordinator SHALL render in the Pinned section as a foldable coordinator row carrying its subtree (its children render nested beneath it as usual), and MUST NOT also render in the active orchestrator tree. A pinned agent SHALL float OUT of its coordinator's child list and render in the Pinned section as a standalone row, and MUST NOT be counted in its (former) coordinator's live-child `(N)` count. Unpinning a row SHALL return it to its normal place on the next rail rebuild.

The system SHALL bind `P` (focus `RAIL`) to toggle the pinned state of the selected coordinator or agent: pin when the row is not pinned, unpin when it is. Pin and archive SHALL be MUTUALLY EXCLUSIVE, mirroring argus's `SetPinned`/`SetArchived`: pinning a row MUST clear its archived state (its hera `archived_at`, and when its bound argus task is argus-archived, the argus side too via the unarchive endpoint), and archiving a row MUST clear its `pinned_at`. Pinned state takes PRECEDENCE over archived state in rendering — a row can never be in both the Pinned and an Archive section.

Pin state SHALL be persisted HERA-SIDE in a nullable `pinned_at` column on `orchestrators` and `roles` (mirroring `archived_at`), because argus exposes no pin/SetPinned REST endpoint. Consequently a pinned hera coordinator/agent does NOT reflect into argus's own TUI Pinned section. `P` against a freelance row (an unmanaged argus task with no hera role) is NOT applicable and MUST give visible feedback naming the gap (pinning is hera-side; a freelancer has no hera row to persist a pin on), never a silent no-op.

#### Scenario: Pinned coordinator floats to the Pinned section

- **WHEN** an orchestrator's `pinned_at` is set
- **THEN** a `Pinned` separator MUST render at the top of the rail AND the orchestrator MUST render as a foldable coordinator row under it (with its subtree) AND MUST NOT also render in the active orchestrator tree

#### Scenario: Pinned agent floats out of its coordinator

- **WHEN** a worker role under coordinator `foo` has its `pinned_at` set
- **THEN** the worker MUST render as a standalone row in the Pinned section AND MUST NOT appear among `foo`'s active children AND MUST NOT be counted in `foo`'s live-child `(N)` count

#### Scenario: `P` pins the selected row and clears archived

- **WHEN** focus is `RAIL` and the operator presses `P` against a non-pinned, archived role
- **THEN** hera MUST set the role's `pinned_at`, clear its `archived_at`, and (when its bound argus task is argus-archived) issue the argus unarchive endpoint, so the row moves into the Pinned section and out of any Archive section

#### Scenario: `P` again unpins

- **WHEN** focus is `RAIL` and the operator presses `P` against a pinned coordinator
- **THEN** hera MUST clear its `pinned_at`, so it returns to the active orchestrator tree on the next rebuild

#### Scenario: Archiving a pinned row clears the pin

- **WHEN** a pinned row is archived (via `a`)
- **THEN** the row's `pinned_at` MUST be cleared so it leaves the Pinned section, AND it MUST render in its Archive section per the archive rules

#### Scenario: `P` on a freelancer pins it at root level (BUG-024)

- **WHEN** focus is `RAIL` and the operator presses `P` against a freelance row
- **THEN** hera MUST toggle the freelancer's pinned state in the rail's in-memory `pinnedFreelance` map (persisted via `railViewState` to the config table), AND the pinned freelancer MUST float to the ROOT level of the Pinned block (depth 0, intermixed with pinned coordinators, no ancestry shown), AND MUST NOT also render in the Freelance section (no double-render), AND MUST NOT write any hera role DB row or call argus. Pressing `P` again MUST unpin and return the freelancer to the Freelance section.

#### Scenario: Every freelance row carries the (F) marker (BUG-024 / BUG-026)

- **WHEN** a freelance task renders in the Freelance section, in the Pinned block, OR in the consolidated bottom Archive's per-project sub-groups
- **THEN** the row MUST display the `iconFreelance` marker (`nf-md-alpha_f_box_outline`, U+F0BFA — confirmed present in HackNerdFont-Regular v3 via post-table glyph name; the prior codepoint U+F0229 was `md-file_presentation_box`, not the intended alpha-f icon) after the status icon, so freelancers are visually distinct from managed agents and coordinators at a glance. The marker MUST render consistently at the same icon-column position in all sections.

#### Scenario: Pinned managed sub-item renders as a stacked two-line breadcrumb (BUG-025)

- **WHEN** a pinned managed role (worker or sub-coordinator) that lives INSIDE a coordinator floats to the Pinned block
- **THEN** its entry in the Pinned block MUST span TWO consecutive visual lines:
  - **Line 1** (`railRowPinnedBreadcrumb`, selectable cursor target, DIMMED): the role's status icon followed by the full ancestry trail from the root coordinator down to the role's immediate parent, each name separated by ` › ` and ending with a trailing ` › ` (e.g. `○ kbtest › nested-sub › `). This line is the cursor target for j/k and pane-binding; `currentRef()` MUST return the role, identical to selecting the name line.
  - **Line 2** (`railRowRole` with `isBreadcrumbContinuation=true`, NON-selectable): the role's name in full-bright style (`StyleNormal`, or `StyleSelected` when line 1 is the cursor), right-aligned age, indented one `indentStep` more than line 1. This line MUST NOT be individually selectable; j/k MUST treat both lines as a single navigation unit (pressing j once from the entry before lands on line 1; pressing j once from line 1 skips line 2 and lands on the next selectable row after both lines).
- A **top-level pinned coordinator** (a pinned `orchEntry`) MUST stay single-line (the existing `railRowOrch` render path).
- A **pinned freelancer** MUST stay single-line (rendered at root depth with no coordinator ancestry per BUG-024).
- When the ancestry trail is too wide for the available line width, it MUST be **left-truncated** with a leading `…` so the nearest parent (rightmost text) remains visible. The role name on line 2 MUST NEVER truncate due to ancestry overflow.
- `SelectByRoleID` and `SelectByArgusTaskID` MUST find `railRowPinnedBreadcrumb` rows (the cursor target) in addition to non-continuation `railRowRole` rows.

#### Scenario: Deep ancestry chain left-truncates in breadcrumb line

- **WHEN** the breadcrumb ancestry trail (e.g. `grandparent › parent › child › `) exceeds the available line-1 width
- **THEN** the leftmost ancestors MUST be dropped and the trail replaced with `…` + the remaining suffix, keeping the nearest parent visible. The role name on line 2 MUST render in full with no truncation from the ancestry.

### Requirement: Consolidated bottom Archive with sub-groups (BUG-026, supersedes BUG-019)

The system SHALL render ONE consolidated `Archive (N)` section at the bottom of the rail, holding ALL archived Hera tasks — archived freelancers AND archived root coordinators — in a single, navigable expando. This replaces the BUG-019 per-project Archive expandos inside the Freelance section. The Freelance section MUST show ONLY live (non-archived, non-pinned) freelancers; archived freelancers are NEVER shown inline in the Freelance section.

The bottom Archive expando (collapsed by default, `archiveTopLevelOwner = 0`) contains TWO types of sub-groups (both also collapsed by default, fold state persisted via `railViewState.ArchiveGroupExpanded`):

- **`Hera sessions` sub-group** — holds archived root coordinators (those archived from the active coordinator tree).
- **Per-project sub-groups** (one per project name, mirroring the live Freelance grouping) — hold archived freelancers for that project. Each archived freelancer row carries the `iconFreelance` marker (`nf-md-alpha_f_box_outline`, U+F0BFA).

Per-coordinator Archive expandos (archived AGENTS inside a coordinator — e.g. `Archive (3)` under `kbtest`) remain exactly as-is, unchanged by this requirement.

`N` on the bottom Archive header is the total count of all archived items (archived coords + archived freelancers). `N` on each sub-group header is the count within that sub-group.

Sub-groups are selectable (`railRowArchiveGroup`). Space/Enter on a sub-group header toggles its fold. The `l` listall convenience force-expands both the Archive expando AND all sub-groups. An active search filter force-expands the Archive and sub-groups, narrowing each to matching items.

Zone separators (plain horizontal rules, `railRowSectionRule`) appear between the active coordinator tree and the Freelance section, and between the Freelance section and the Archive section, visually delineating the four rail zones: Pinned | active coords | Freelance | Archive.

The rail border title is EMPTY by default (BUG-026: the "Rail" label is redundant, removed). During an active search filter, the title shows only the query string (e.g. `/scout`).

#### Scenario: Archived freelancer renders in the consolidated bottom Archive, not vanished

- **WHEN** the operator presses `a` on a freelancer and its argus task becomes archived
- **THEN** in the default view (without `l`) the rail MUST render the bottom `Archive (N)` section counting that freelancer AND the Freelance section MUST NOT contain that freelancer. Expanding the Archive MUST reveal a per-project sub-group; expanding the sub-group MUST reveal the freelancer as a selectable row.

#### Scenario: Archived freelancer does not double-render

- **WHEN** a freelancer is archived
- **THEN** it MUST NOT appear inline in its Freelance repo group (in either the default or the `l` view) — it renders only inside the bottom Archive's per-project sub-group.

#### Scenario: No per-project Archive expandos in the Freelance section

- **WHEN** the Freelance section renders
- **THEN** it MUST contain ONLY live (non-archived, non-pinned) freelancers grouped by project. There MUST be no `railRowArchiveExpando` or `railRowArchiveGroup` rows inside the Freelance section.

#### Scenario: `a` on an archived freelancer in the Archive section unarchives it

- **WHEN** the operator presses `a` against an archived freelancer row inside the bottom Archive section, whose argus task is `T9`
- **THEN** hera MUST issue the argus unarchive endpoint for `T9` with no hera DB write, so the task returns to its Freelance repo group on the next rebuild.

#### Scenario: Archived root coordinator reachable without `l` via "Hera sessions"

- **WHEN** a root coordinator is archived
- **THEN** in the default view (without `l`) the rail MUST render the bottom `Archive (N)` section. Expanding it MUST reveal a `Hera sessions (N)` sub-group. Expanding that sub-group MUST reveal the archived coordinator.

#### Scenario: `l` force-expands the bottom Archive and all sub-groups

- **WHEN** the operator presses `l`
- **THEN** the bottom `Archive (N)` section MUST be force-expanded along with every per-coordinator Archive expando AND all Archive sub-groups (`Hera sessions`, per-project freelancer groups), AND dead rows MUST be revealed.

### Requirement: Rail status icons reflect the bound task's actual argus state

The status icon on every rail row (coordinator headers, agent/worker rows, sub-coordinator rows, and freelance rows) SHALL be derived from the bound argus task's actual argus-reported state whenever that state is known to the argus state cache, REGARDLESS of the row's archive state (hera `archived_at` set, argus-archived) or its hera binding's liveness (live, ended, or dead). Archive state and binding liveness SHALL modulate only the row's STYLE (dimmed) and PLACEMENT (active list vs Archive expando) — never the status GLYPH: archiving a row, unarchiving it, or its binding ending MUST NOT change which glyph renders while the task's argus status is unchanged.

To keep the state resolvable across those transitions, the row's argus-state lookup MUST resolve through the role's most recent binding's task id when no live binding exists (archiving ends the binding with `end_reason='argus_archived'` while preserving the task id; reconciliation ends bindings with `end_reason='resync_missing'`), and the argus state cache MUST include archived tasks (argus's default task list excludes them).

The idle circle `○` SHALL render as a status glyph only when the task's argus state actually warrants it (in-progress idle); for an archived or dead row whose argus state is UNKNOWN (cache miss / task gone from argus), `○` dimmed remains the fallback.

#### Scenario: Archive round-trip never mutates the status glyph

- **WHEN** an agent row whose argus task is `complete` is archived via `a` and later unarchived via `a`, with the task's argus status unchanged throughout
- **THEN** the row's status icon MUST render `✓` at every step — dimmed while the row displays as archived — and MUST NOT fall to `○` at any point in the round trip

#### Scenario: Archived row with known state renders its true status dimmed

- **WHEN** the rail renders a row bucketed as archived (hera-archived, argus-archived, or dead) whose task state is known to the cache
- **THEN** the row MUST render the glyph for its actual argus status (`?` needs-input, `✓` complete/in_review, `☾` working, `○` idle-in-progress) in a dimmed style

#### Scenario: Ended binding does not lose the state lookup

- **WHEN** a role's only binding has ended (`end_reason='argus_archived'` or `'resync_missing'`) and the task's state is present in the argus state cache
- **THEN** the row's status icon MUST reflect that state exactly as if the binding were live

#### Scenario: Dead-classified row with known complete state shows the check

- **WHEN** a row's task is classified dead by the aliveness checker because its status is `complete`, while the state cache still holds that status
- **THEN** the row MUST render `✓` dimmed, not `○`

#### Scenario: Unknown-state archived row falls back to the dimmed circle

- **WHEN** an archived or dead row's task has no entry in the argus state cache (and the cache is warm)
- **THEN** the row renders `○` dimmed as the unknown-state fallback

### Requirement: Archive operations tolerate argus-pruned tasks

Argus prunes tasks outright (deletes them, not archives them), so a role's recorded `argus_task_id` can point at a task that no longer exists. The system SHALL treat an HTTP 404 (task not found) from argus's archive or unarchive endpoint as a successful no-op for the argus side of the operation: there is nothing to (un)archive argus-side, so the hera-side flip MUST proceed, the operation MUST report success, and the skip SHALL be logged for diagnosis. This tolerance applies to role archive, role unarchive, orchestrator-unarchive's coord-task unarchive, and the direct freelance task toggle. The 404 MUST be detected via a typed status-code check on the argus client's error, never by string-matching the formatted message.

An orchestrator archive cascade MUST NOT abort when an individual role's archive fails. Pruned tasks (404) are skips per the above; any OTHER per-role failure (e.g. argus unreachable) SHALL be collected while the cascade continues through the remaining roles. When one or more roles failed, the orchestrator's own `archived_at` MUST be left clear — the row stays active so the operator can retry, and the retry skips roles already archived — and the operation MUST return a single summary error naming the roles that failed.

Status stepping (`s`/`S`) against a pruned task remains an error — a nonexistent task cannot be stepped — but the message MUST state plainly that the task no longer exists in argus rather than surfacing a raw HTTP error.

#### Scenario: Archiving a role whose task argus pruned succeeds

- **WHEN** the operator presses `a` against an active role whose bound argus task argus has pruned (the archive endpoint returns 404)
- **THEN** the role's hera `archived_at` MUST be set, the argus side MUST be skipped, and the operation MUST report success

#### Scenario: Orchestrator cascade archives through a mix of live and pruned tasks

- **WHEN** the operator presses `a` against an orchestrator whose roles bind a mix of live and pruned argus tasks
- **THEN** every role MUST be archived hera-side, live tasks MUST be archived argus-side, pruned tasks MUST be skipped, and the orchestrator itself MUST be archived with no error

#### Scenario: Non-404 failure does not abort the cascade and leaves the orchestrator retryable

- **WHEN** one role's argus-side archive fails with a non-404 error (e.g. argus unreachable for that call) during an orchestrator cascade
- **THEN** the cascade MUST still attempt every remaining role, the orchestrator's `archived_at` MUST be left clear, and the returned error MUST name the role(s) that failed

#### Scenario: Unarchiving a role whose task argus pruned succeeds

- **WHEN** the operator presses `a` against an archived role whose bound argus task argus has pruned (the unarchive endpoint returns 404)
- **THEN** the role's hera `archived_at` MUST be cleared, the argus side MUST be skipped, and the operation MUST report success

#### Scenario: Stepping status on a pruned task errors with a plain message

- **WHEN** the operator presses `s` or `S` against a row whose argus task argus has pruned
- **THEN** the operation MUST fail with a message stating the task no longer exists in argus, not a raw HTTP 404 error string

### Requirement: Rail status glyphs mirror argus's task-panel table exactly

The status glyph and color rendered for a known argus task state — on every rail row kind (coordinator headers, agent/worker rows, sub-coordinator rows, and freelance rows) and in both active and archived/dimmed variants — SHALL mirror argus's task-panel mapping (argus `internal/tui/theme/theme.go:29-34` + `internal/tui/taskview/tasklist.go:1095-1132`) EXACTLY:

| Argus state | Glyph | Color |
|---|---|---|
| `pending` | `○` U+25CB | gray (`theme.ColorPending`) |
| `complete` | `✓` U+2713 | green (`theme.ColorComplete`) |
| `in_review` | `󰖔` U+F0594 (nf-md-weather_night, moon-with-stars) | blue (`theme.ColorInReview`) |
| `in_progress` + needs-input | `` U+F059 (nf-fa-question_circle) | `theme.ColorNeedsInput` (#faa378) |
| `in_progress` + idle | `` U+F186 (nf-fa-moon_o) | blue (`theme.ColorInReview`) |
| `in_progress` + running (idle false/omitted) | spinner frames U+EE06..U+EE0B, animated at argus's 150 ms cadence | orange (`theme.ColorInProgress`) |

`in_review` MUST be visually distinct from `complete` by GLYPH, not merely by color: the checkmark `✓` MUST NOT render for any state other than `complete`. Within `in_progress`, needs-input takes priority over idle and running, mirroring argus's switch nesting; the needs-input override applies only to `in_progress` (argus's API serves `needs_input` solely for `in_progress`). Argus's TUI-only `idleUnvisited` variant (moon-with-stars for unvisited-idle) is not in the API and MUST NOT be mirrored: all `in_progress`+idle rows render the plain moon `` U+F186. Colors SHALL come from the SDK theme's argus values (`github.com/anutron/argus-sdk/theme`), never re-hardcoded hex.

Running rows SHOULD animate: while at least one visible rail row is in the running state, the view SHALL repaint at the spinner cadence so the spinner advances by wall clock (argus's animation source); each repaint MUST render the frame for the current wall-clock instant. When no running row is visible, the spinner driver MUST NOT schedule repaints.

This table defines the GLYPH for a known state; it composes with (and does not supersede) the rail-truthfulness requirement: archive state and binding liveness modulate only the STYLE (dimmed), never the glyph, and the dimmed `○` fallback remains reserved for archived/dead rows with UNKNOWN state.

#### Scenario: in_review renders the moon-with-stars, never a check

- **WHEN** a rail row's bound task has argus status `in_review`
- **THEN** the row MUST render `󰖔` (U+F0594) in blue (`theme.ColorInReview`) AND MUST NOT render `✓`

#### Scenario: complete is the only state that renders a checkmark

- **WHEN** rail rows render tasks in every known argus state
- **THEN** only the `complete` row renders `✓` (green, `theme.ColorComplete`); every other state renders its own table glyph

#### Scenario: pending renders the open circle

- **WHEN** a rail row's bound task has argus status `pending`
- **THEN** the row MUST render `○` in gray (`theme.ColorPending`)

#### Scenario: in_progress idle renders the plain moon in blue

- **WHEN** a rail row's bound task is `in_progress` with `idle` true and `needs_input` false
- **THEN** the row MUST render `` (U+F186) in blue (`theme.ColorInReview`), not dimmed and not the moon-with-stars

#### Scenario: in_progress running renders the animated spinner in orange

- **WHEN** a rail row's bound task is `in_progress` with `idle` false/omitted and `needs_input` false
- **THEN** the row MUST render the spinner frame for the current wall-clock instant (one of U+EE06..U+EE0B at a 150 ms cadence) in orange (`theme.ColorInProgress`), and successive repaints across frame boundaries MUST advance the frame

#### Scenario: needs-input outranks idle and running within in_progress

- **WHEN** a rail row's bound task is `in_progress` with `needs_input` true (regardless of `idle`)
- **THEN** the row MUST render `` (U+F059) in `theme.ColorNeedsInput` (#faa378)

#### Scenario: needs-input does not override a non-in_progress status

- **WHEN** a rail row's state carries `needs_input` true but a status other than `in_progress` (a shape argus's API never serves)
- **THEN** the row MUST render the glyph for its status, mirroring argus's switch nesting

#### Scenario: archived variants keep the table glyph dimmed

- **WHEN** a rail row bucketed as archived/dead has a known argus state
- **THEN** the row MUST render the same table glyph for that state in the dimmed style (a running archived row renders the spinner frame dimmed)

#### Scenario: spinner driver is quiet without running rows

- **WHEN** no visible rail row is in the running state
- **THEN** the spinner driver MUST NOT schedule spinner repaints

### Requirement: All hera-rendered surfaces share one consistent background

Every hera-rendered surface — the root canvas, the top bar, the body, the rail, the coordinator and agent pane frames (including their empty/placeholder state), the Details pane, the gaps/canvas between or around panes, and every modal/popup overlay (input, two-field, confirm, select, error) — SHALL paint the SAME background color the SDK terminalpane uses for its emulator interior cells: the terminal's default background (`tcell.ColorDefault`, exposed as the view package's single `heraBackground` constant). No hera surface may render the grey-blue backgrounds that previously leaked through — neither tview's stock primitive/contrast defaults nor the argus status-bar dark gray (`theme.ColorStatusBG`). A modal overlay is distinguished from the chrome behind it by its cyan border + title and its highlighted buttons/fields, NOT by a different background fill.

#### Scenario: Chrome surfaces use the single hera background

- **WHEN** the hera-view application is built
- **THEN** the root, top bar, body, rail, both panes, and the Details pane MUST each report `heraBackground`, AND none may report `theme.ColorStatusBG`, AND the global tview default background (primitive and contrast) MUST be repointed to `heraBackground` so any unset primitive falls through to the same black

#### Scenario: Modal overlays use the single hera background, not grey-blue

- **WHEN** any modal/popup overlay (input, two-field, confirm, select, or error) is rendered over the base layout
- **THEN** no cell on the drawn surface may carry tview's stock contrast background or `theme.ColorStatusBG`, AND at least one cell of the overlay MUST carry `heraBackground`

