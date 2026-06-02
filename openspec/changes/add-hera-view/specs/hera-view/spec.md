## ADDED Requirements

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

- **Coordinator mode** (a coordinator row is selected — root or sub): rail + a single full-width **HERA pane** (the coordinator's own PTY). No AGENT pane is composed.
- **Agent mode** (a worker/agent row is selected): rail + **HERA pane** (the agent's coordinator's PTY) + **AGENT pane** (the agent's PTY) — a split body.
- **Freelance mode** (a freelance row is selected): rail + a single full-width **AGENT pane** (the freelancer's PTY). No HERA pane is composed.

The center pane is labeled **HERA** (the coordinator), not "coord". The top bar SHALL contain literal text `HERA` left-aligned. Bottom-bar hints are advertised to argus per the key-surrender contract (argus draws the plugin-mode bar); when rendered standalone the bottom bar is focus-aware.

#### Scenario: Coordinator selection is a full-width HERA pane

- **WHEN** a coordinator row (root or sub) is selected
- **THEN** the body MUST be the rail plus a single full-width HERA pane bound to that coordinator's PTY AND no AGENT pane MUST be present

#### Scenario: Agent selection splits HERA + AGENT

- **WHEN** a worker/agent row is selected
- **THEN** the body MUST be the rail + a HERA pane bound to the agent's coordinator + an AGENT pane bound to the agent

#### Scenario: Freelance mode collapses to rail + full-width agent

- **WHEN** the rail selection moves to a freelance row
- **THEN** the body MUST be the navigation rail plus a single AGENT pane spanning the remaining width AND no HERA pane MUST be present

#### Scenario: Switching selection re-composes the mode

- **WHEN** the rail selection moves between a coordinator, an agent, and a freelancer
- **THEN** the body MUST re-compose to the corresponding mode (full-width HERA / split / full-width AGENT), tearing down the now-absent pane's subscription

#### Scenario: Project-mode rail traversal updates both panes

- **WHEN** rail selection moves to a worker agent whose project's coord differs from the previous selection's project
- **THEN** the COORD pane MUST switch to the new project's coord binding ring buffer AND the AGENT pane MUST switch to the new agent's binding ring buffer

#### Scenario: Freelance mode releases the coord binding

- **WHEN** a freelance row is selected
- **THEN** the COORD pane's proxy subscription MUST be released AND the coord task target MUST be empty so no keystroke is forwarded to a coordinator task

### Requirement: Freelance (unmanaged argus) agents render in a Freelance rail section grouped by repo

A **freelancer** is a live argus task that hera has never bound to any role — a vanilla agent created directly in argus that makes no calls to hera. The system SHALL surface freelancers in the rail so the operator never has to leave hera to notice that an unmanaged agent needs attention.

The system SHALL determine the freelancer set from argus's live task list (the argus state cache): every non-archived argus task whose id is NOT referenced by any hera binding (live or ended) is a freelancer. These SHALL be rendered in a "Freelance" section below all project (orchestrator) rows and above the Archive separator, introduced by a "Freelance" separator that is shown ONLY when at least one freelancer exists (so the operator never lands on an empty section).

Within the Freelance section, freelancers SHALL be grouped by argus project (repo) — "the same way Argus shows them" — under per-repo headers sorted by project name. Each repo header MUST render a collapse chevron (`▾` expanded / `▸` collapsed), the project name, and the count of its live freelance tasks, and MUST toggle expand/collapse when the operator presses Space while that header is selected. Repo groups default to expanded so freelancers are visible by default. Each freelance row MUST render its argus-reported state (status / idle / needs-input) via the same icon rules as managed rows, and its elapsed column MUST show argus's own age string.

Archived argus tasks MUST NOT appear in the Freelance section by default; they MUST appear only when the Archive view is revealed via `l`.

#### Scenario: Unmanaged argus tasks surface as freelancers grouped by repo

- **WHEN** the rail renders and argus reports live tasks that hera has never bound
- **THEN** a "Freelance" section MUST appear below all project rows, with those tasks grouped under per-repo headers sorted by project name

#### Scenario: Hera-managed tasks are excluded from Freelance

- **WHEN** an argus task is referenced by any hera binding (live or ended)
- **THEN** that task MUST NOT appear in the Freelance section (it renders under its orchestrator instead)

#### Scenario: Space toggles a Freelance repo group

- **WHEN** focus is `RAIL`, a Freelance repo header is selected, and the operator presses Space
- **THEN** that repo group MUST expand (or collapse if already expanded), revealing (or hiding) its freelance rows, independently of other repo groups

#### Scenario: No freelancers hides the section

- **WHEN** argus reports no unmanaged, non-archived live tasks
- **THEN** the "Freelance" separator and all repo headers MUST NOT be rendered

#### Scenario: Archived freelancers appear only in Archive

- **WHEN** an unmanaged argus task is archived
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

The system SHALL recognize the RAIL-only key set (`n`, `r`, `a`, `l`, `?`, `s`, `S`, `^d`, `^r`, `^p`) ONLY when focus is `RAIL`. When focus is `COORD` or `AGENT`, every one of these keys — including the destructive/external verbs `^d`, `^r`, and `^p` — MUST be treated as ordinary input and forwarded to the bound task's PTY (per the keystroke-forwarding requirement): a printable key forwards its byte, and `^d`/`^r`/`^p` forward their control bytes (Ctrl-D=0x04, Ctrl-R=0x12, Ctrl-P=0x10) so an agent gets EOF / reverse-search / history-prev normally. None of these keys fires a mutation or is intercepted while focus is in a pane.

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

The system SHALL, on `a` against a non-archived role, set the role's `archived_at` to the current timestamp AND invoke argus's archive endpoint (`POST /api/tasks/{id}/archive`) on the binding's `argus_task_id`. The worktree MUST be preserved. On `a` against a non-archived orchestrator, the same MUST cascade to every role under that orchestrator. On `a` against an already-archived role or orchestrator, `archived_at` MUST be cleared (the role unarchives); unarchiving an orchestrator MUST NOT cascade to roles (roles unarchive individually).

#### Scenario: Archive role calls argus and preserves worktree

- **WHEN** the operator presses `a` against non-archived role `foo/w1` bound to argus task `T1`
- **THEN** hera MUST set the role's `archived_at` to the current timestamp, issue `POST /api/tasks/T1/archive` to argus, AND MUST NOT touch the worktree directory

#### Scenario: Archive orchestrator cascades to roles

- **WHEN** the operator presses `a` against non-archived orchestrator `foo` with roles `coord`, `w1`, `w2`
- **THEN** hera MUST set `archived_at` on `foo`, `coord`, `w1`, and `w2` AND issue an archive call to argus for each role's live binding's argus_task_id

#### Scenario: Unarchive orchestrator does not cascade

- **WHEN** the operator presses `a` against an archived orchestrator `foo` whose roles `coord`, `w1` are also archived
- **THEN** hera MUST clear `archived_at` on `foo` AND MUST leave `archived_at` on `coord` and `w1` set

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

#### Scenario: hotkeys pushed on focus change

- **WHEN** the focus state changes (for example `RAIL` → `COORD`)
- **THEN** hera MUST send a `{"type":"hotkeys",...}` frame whose items reflect the new focus state's bindings

#### Scenario: no internal bottom bar rendered

- **WHEN** the view surface renders in any focus state
- **THEN** it MUST NOT include hera's own bottom-bar row (argus owns the plugin-mode status bar)

### Requirement: Every mutation is gated behind a confirmation or input modal

The system SHALL display a confirmation or input modal for every destructive, creative, or external rail-level mutation (`n`, `r`, `^d`, `^r` prune, `^p` open-PR, and resurrect-on-Enter against an archived coord). The DB writes, worktree/branch destruction, and external HTTP calls MUST NOT occur unless the operator explicitly confirms the modal. Reversible single-key toggles — `a` archive/unarchive and `l` listall — MUST NOT require a modal (`a` is reversible by pressing `a` again; `l` is a pure view-state toggle).

#### Scenario: `^d` shows confirmation before deleting

- **WHEN** the operator presses `^d` in `RAIL` against a role
- **THEN** a confirmation modal MUST appear naming the role and (if applicable) its worktree path AND MUST require an explicit confirm keystroke before any DB write or `git worktree remove` invocation

#### Scenario: `l` listall does not require a modal

- **WHEN** the operator presses `l` in `RAIL`
- **THEN** the Archive section visibility MUST toggle immediately with no modal

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

The status icon (on both coordinator headers and agent rows) reflects argus status using argus's own vocabulary (`?` needs-input, `✓` review/complete, `☾` working, `○` idle); a coordinator header's status icon is driven by its coord task's argus state, and a sub-coordinator row's by its own bound task's state. The coordinator marker glyph (distinct from the transient status icon) flags the row as a coordinator regardless of state; the prototype's `◆` root-coord icon is superseded by this status-icon + marker pairing. Every coordinator with archived direct children MUST render an `Archive (N)` expando below its active agents (collapsed by default); archived root coordinators MUST render under a top-level `Archive` section at the bottom of the rail. `space` MUST toggle the fold of the selected coordinator (root OR sub-coordinator) or Archive section.

The rail's currently selected row MUST be indicated by selected-text styling — its name rendered in argus's selected style (`theme.StyleSelected`, pink `theme.ColorSelected`) — applied consistently across ALL selectable row types (coordinator headers, agent/worker rows, sub-coordinator rows, Freelance repo headers, and Archive expandos). The rail MUST NOT paint any cell background to indicate selection: no row — selected or not — may render a filled (non-default) cell background. This guarantees no stale highlight cell can linger on a previously-selected row when the cursor moves, because no row ever writes a non-default background that a later draw would have to clear. The per-row selection indicator is distinct from, and MUST NOT be confused with, the rail pane's focus border (argus's focus color on the pane edge), which is governed by the focus-model requirement.

#### Scenario: Coordinator row is foldable with a count

- **WHEN** the rail renders a coordinator that has live agents
- **THEN** the row MUST show a chevron and a `(N)` live-child count AND pressing `space` on it MUST toggle whether its children are shown

#### Scenario: Coordinator header carries a status icon and coordinator marker

- **WHEN** the rail renders a coordinator whose coord task argus state is known
- **THEN** the header row MUST render, before the name, a status icon reflecting that argus state (the same `?`/`✓`/`☾`/`○` vocabulary as agent rows) AND the `󰹻` coordinator marker glyph AND MUST NOT render the prototype's `◆`

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

- **WHEN** an agent under a coordinator is archived
- **THEN** it MUST NOT appear among the coordinator's active rows AND it MUST appear inside that coordinator's `Archive (N)` expando, which is collapsed by default

#### Scenario: Archived root coordinators live in the top-level Archive

- **WHEN** a root coordinator is archived
- **THEN** it MUST appear only under the top-level `Archive` section at the bottom of the rail

#### Scenario: Coordinator with no workers renders header-only

- **WHEN** an orchestrator has a live coord but no worker agents
- **THEN** the rail MUST render only its foldable coordinator row with no child agent row, and selecting it MUST compose the full-width HERA pane bound to the coord's PTY

#### Scenario: Selected row is indicated by selected-text styling, not a background fill

- **WHEN** the rail renders with the cursor on a selectable row (a coordinator header or an agent/worker row)
- **THEN** that row's name MUST render in `theme.StyleSelected` (pink `theme.ColorSelected`) AND none of that row's cells may carry a non-default cell background (no `theme.ColorHighlight` fill)

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

The system SHALL support, mirroring argus's keymap now that argus surrenders the full keyboard: `s` / `S` advance / revert the selected agent's argus task status (pending → in_progress → in_review → complete); `^p` opens a pull request for the selected agent's, coordinator's, OR freelancer's task via the host git flow — an agent/worker row resolves to that role's bound task, a coordinator selection (root orchestrator header or a sub-coordinator role) resolves to the coordinator's bound argus task, and a freelance row (an unmanaged argus task with no hera role/binding) resolves to the argus task's own worktree (the same way argus opens a PR for that task); `⇧↑` / `⇧↓` scroll the focused pane's scrollback; `⌘↑` / `⌘↓` (or `^↑`/`^↓`) move the rail selection to the next/previous agent while focus remains inside a pane (re-entering the new selection's primary pane), so the operator can flip through agents without returning to the rail.

#### Scenario: s/S step the selected agent's status

- **WHEN** focus is `RAIL` on an agent and the operator presses `s`
- **THEN** the agent's argus task status MUST advance one step (and `S` MUST revert one step)

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
