## 1. Stage A — Migration + DAOs

- [x] 1.1 Add migration `0003_archived_at.sql` to `internal/db/schema.go` adding nullable `archived_at TEXT` columns to `orchestrators` and `roles`, with an index on each.
- [x] 1.2 Add `ArchiveOrchestrator(id)`, `UnarchiveOrchestrator(id)`, `RenameOrchestrator(id, newName)` to `internal/db/orchestrators.go`.
- [x] 1.3 Add `ArchiveRole(id)`, `UnarchiveRole(id)`, `RenameRole(id, newName)` to `internal/db/roles.go`.
- [x] 1.4 Update default `List*` paths on orchestrators and roles to filter `archived_at IS NULL` by default; expose `IncludeArchived bool` option (or `*Inclusive` method).
- [x] 1.5 Tests: `internal/db/orchestrators_test.go`, `internal/db/roles_test.go` exercise archive / unarchive / rename plus uniqueness across archived/non-archived rows.
- [x] 1.6 Tests: confirm `Get(id)` resolves archived rows; confirm default `List*` filters them out.
- [x] 1.7 Run `go test ./internal/db/... -race -count=1` until green.

## 2. Stage B — PTY proxy package

- [x] 2.1 Create `internal/view/proxy/proxy.go` exposing `NewSubscription`, `Subscribe`, `Close`. One subscription per argus task; fan-out listeners over the same upstream snapshot+SSE.
- [x] 2.2 Create `internal/view/proxy/ring.go` — circular byte buffer with ~256 KiB cap; oldest bytes dropped when full.
- [x] 2.3 Wire the snapshot fetch (`GET /api/tasks/{id}/output`) + SSE consumer (`GET /api/tasks/{id}/stream?since=N`) via the existing `internal/argus/client.go`. Snapshot returns `X-Output-Total`; pass to the SSE consumer as `since`.
- [x] 2.4 Tests: `internal/view/proxy/proxy_test.go` — fake argus HTTP/SSE server, assert snapshot-then-stream sequencing, ring boundedness, multi-listener fan-out.
- [x] 2.5 Run `go test ./internal/view/proxy/... -race -count=1` until green.

## 3. Stage C — Plugin view registration in argus client

- [x] 3.1 Add `internal/argus/views.go` with `RegisterView(title, hotkey, callbackURL)`, `HeartbeatView(id)`, `DeleteView(id)` matching the existing MCP-tool registrar pattern (see `internal/mcp/registrar.go`).
- [x] 3.2 Use the existing scope token for auth; treat 409 on re-register as "already registered, fall back to heartbeat" the same way the MCP registrar does.
- [x] 3.3 Tests: `internal/argus/views_test.go` against a fake argus HTTP server; assert request shape, error handling, idempotency on re-register.
- [x] 3.4 Run `go test ./internal/argus/... -race -count=1` until green.

## 4. Stage D — Custom `tcell.Screen` backed by WebSocket

- [x] 4.1 Create `internal/view/screen/wsscreen.go` — a `tcell.Screen` implementation whose `Show()`/`Sync()` emit a full-surface ANSI byte buffer as a WebSocket binary frame; whose event queue receives `tcell.EventKey` events translated from inbound binary frames.
- [x] 4.2 Translate text-frame control envelopes (`resize`/`focus`/`blur`) into `tcell.EventResize` and focus state changes.
- [x] 4.3 Tests: `internal/view/screen/wsscreen_test.go` — drive Show/Sync against a fake WebSocket connection, assert ANSI bytes well-formed; inject inbound binary frames, assert tcell events delivered.
- [x] 4.4 Run `go test ./internal/view/screen/... -race -count=1` until green.
- [x] 4.5 If implementing the WS-backed Screen proves infeasible in this stage, stop and `hera_send` to coord with the blocker rather than push through — do NOT improvise. Stage J / K remain blocked.

## 5. Stage E — WebSocket server route

- [x] 5.1 Add `internal/view/server.go` mounting `GET /view` on the existing `:7744` HTTP listener; accept WebSocket upgrade via `github.com/coder/websocket`.
- [~] 5.2 Per connection: construct a Stage-D `wsscreen`, then a `tview.Application` bound to it, then run the app's event loop on a goroutine; on connection close, stop the app and tear down the goroutine. (Per-connection lifecycle — goroutine spawn, ctx-cancel on supersede, defer-close on exit — implemented behind a `SessionFunc` injection point. The wsscreen + tview construction is deferred to Stage J daemon wire-up so this stage doesn't block on Stage D / F.)
- [x] 5.3 Last-writer-wins: maintain a single-active-session reference; on new upgrade, close the prior session before accepting the new one.
- [x] 5.4 Tests: `internal/view/server_test.go` — start the route on an `httptest.Server`, open two consecutive WebSocket clients, assert the first connection is closed when the second connects.
- [x] 5.5 Run `go test ./internal/view/... -race -count=1` until green.

## 6. Stage F — tview app + 3-column layout

- [x] 6.1 Add `internal/view/streampane.go` — a tview Box widget that renders an ANSI byte stream from a channel (port / fork of argus's `streampane.StreamPane`; see `~/Development/Personal/argus/internal/tui/streampane/`).
- [x] 6.2 Add `internal/view/layout.go` — tview Flex composing: top bar (literal `HERA` left-aligned, 1 row) + body (rail width ~22, coord pane and agent pane equal-split, remaining rows) + bottom bar (1 row, focus-state-aware hints).
- [x] 6.3 Add `internal/view/app.go` exposing `BuildApp(db, proxy)` returning a `*tview.Application`. Wire rail data from the orchestrators/roles/bindings DAOs (active-only on first render); wire StreamPane data from Stage B proxy subscriptions.
- [x] 6.4 Tests: `internal/view/app_test.go` — build the app, drive a fake screen, assert layout cells render the expected content (rail header, top-bar text, pane placeholders when no bindings exist).
- [x] 6.5 Defer focus, key routing, and rail-state-aware bottom bar to stages G and H respectively; first cut wires layout only.
- [x] 6.6 Run `go test ./internal/view/... -race -count=1` until green.

## 7. Stage G — Focus + key routing

- [x] 7.1 Add `internal/view/focus.go` — three-state machine (`RAIL`, `COORD`, `AGENT`) with `Advance()` / `Retreat()` / `ToRAIL()` / `JumpToAGENT()` transitions.
- [x] 7.2 Add `internal/view/keys.go` — top-level key handler attached to the tview Application; routes Cmd/Ctrl-←/→, Enter, Ctrl-Q to focus transitions and forwards all other keystrokes in `COORD` / `AGENT` focus to the bound task's input endpoint via `internal/argus/client.go`.
- [x] 7.3 Colored border on the focused element via `SetBorderColor` driven by the focus state.
- [x] 7.4 Mutation keys `n`/`r`/`^d`/`a`/`l`/`?` are intercepted only when focus is `RAIL`; in `COORD` / `AGENT` they fall through to keystroke forwarding (as ordinary characters).
- [x] 7.5 Tests: `internal/view/keys_test.go` — drive the focus state machine; assert mutation keys are no-ops in pane focus; assert focus-traversal keys are not forwarded as bytes.
- [x] 7.6 Run `go test ./internal/view/... -race -count=1` until green.

## 8. Stage H — Rail operations

- [x] 8.1 Extend `internal/argus/client.go` with `CreateTask(project, prompt, meta)` (HTTP POST to `/api/tasks`). If the shape doesn't match what design.md D5 assumes, stub it and `hera_send` to coord with the substrate question; do NOT improvise the wire format.
- [x] 8.2 Add `internal/view/ops/new.go` — modal flow for `n`: prompt for name + mission, validate uniqueness against non-archived orchestrators, spawn argus task via `CreateTask` whose prompt invokes `hera_new_orchestrator`.
- [x] 8.3 Add `internal/view/ops/rename.go` — modal flow for `r`: prompt for new name, validate uniqueness scope (global for orchestrators, per-orchestrator for roles), call DAO `RenameOrchestrator` / `RenameRole`.
- [x] 8.4 Add `internal/view/ops/delete.go` — modal flow for `^d`: confirmation modal listing what will be removed; on confirm: end binding(s), set `archived_at`, `git worktree remove --force` via `os/exec` (daemon is unsandboxed under launchd). Log every `git worktree remove` invocation with the path. If the worktree path is empty or the directory does not exist, log + skip (soft no-op).
- [x] 8.5 Add `internal/view/ops/archive.go` — `a` toggle: set/clear `archived_at` via DAO and call argus's `POST /api/tasks/{id}/archive` for non-archived → archived transitions on the bound argus_task_id, and the unarchive endpoint for archived → non-archived transitions (symmetric toggle; orchestrator-unarchive addresses the coord role's task, workers stay archived hera-side).
- [x] 8.6 Add `internal/view/ops/listall.go` — pure view-state toggle for `l`; no DB writes.
- [x] 8.7 Add `internal/view/ops/help.go` — `?` modal listing all bindings by focus state; dismiss via `q`.
- [x] 8.8 Add `internal/view/ops/resurrect.go` — Enter on archived coord row when Archive section visible: confirm, clear `archived_at` on orchestrator + coord role, spawn argus task via `CreateTask` in the role's stored `argus_project` with a prompt invoking `hera_join(cwd=$PWD)`.
- [x] 8.9 Tests per file: `ops/new_test.go`, `ops/rename_test.go`, `ops/delete_test.go`, `ops/archive_test.go`, `ops/resurrect_test.go`. For `delete_test.go` use a temp git worktree fixture to exercise the `git worktree remove` step.
- [x] 8.10 Run `go test ./internal/view/ops/... -race -count=1` until green.

## 9. Stage I — Dynamic rail updates

- [x] 9.1 Add `internal/db/events.go` exposing a `Broadcaster` type with `Emit(event)` and `Subscribe() chan Event`. Event types cover orchestrator/role/binding insert/update/delete.
- [x] 9.2 Integrate `Broadcaster.Emit` into the relevant DAO methods (insert / update / delete on orchestrators, roles, bindings, including the new Stage A methods).
- [x] 9.3 Add `internal/view/rail.go` subscribing to the broadcaster from `BuildApp`; debounce refreshes (~100 ms) and re-render the rail in tview's `QueueUpdateDraw`.
- [x] 9.4 Tests: `internal/db/events_test.go` (broadcast fan-out, no blocking on slow subscribers); `internal/view/rail_test.go` (insert orchestrator → rail refreshes within ~150 ms; idle 1s → no DB reads).
- [x] 9.5 Run `go test ./internal/... -race -count=1` until green.

## 10. Stage J — Daemon wire-up

- [x] 10.1 In `internal/daemon/run.go`: at startup, after the existing MCP registrar starts, also start the plugin-view registrar (Stage C) and mount the WebSocket route (Stage E) on the existing `:7744` listener.
- [x] 10.2 At startup, walk live bindings and seed the Stage-B PTY proxy with snapshot + SSE subscriptions for each.
- [x] 10.3 At shutdown, unregister the plugin view (DELETE) and tear down proxy subscriptions cleanly.
- [x] 10.4 Tests: `internal/daemon/run_test.go` asserts the plugin view registers on startup and unregisters on shutdown via a fake argus HTTP server.
- [x] 10.5 Run `go test ./internal/daemon/... -race -count=1` until green.

## 11. Stage K — Smoke test

- [x] 11.1 Add `internal/daemon/view_smoke_test.go` — spawn the daemon in-process pointed at a fake argus HTTP+SSE server; open a real WebSocket against `/view`; send a `resize` envelope; assert outbound binary frames are well-formed ANSI; send an inbound key envelope; assert the daemon routes it to the right task's input endpoint on the fake argus server.
- [x] 11.2 Confirm the smoke test also exercises the last-writer-wins close on a second connection.
- [x] 11.3 Run `go test ./internal/daemon/... -race -count=1` until green.

## 12. Stage L — Freelance rail section + dual-mode layout

**Depends on:** Stage I (rail rendering + dynamic refresh) and Stage G (focus + key routing)

- [ ] 12.1 Write failing tests from the new/changed scenarios (Prove-It). `rail_list_test.go`: global Freelance section collected below projects; collapsed-by-default `▸`; Space toggles when header selected; header shows live count; header hidden when zero live freelance; workers stay nested; archived freelance appear only inside Archive. `app_test.go`/`layout_test.go`: freelance mode = rail + full-width agent with no coord; returning to a coord/worker row restores 3 columns; coord subscription released + empty `CoordTaskID()` in freelance mode. `focus_test.go`/`keys_test.go`: freelance mode skips COORD on Cmd/Ctrl-→ and Cmd/Ctrl-←. Confirm each fails first.
- [ ] 12.2 `internal/view/rail_list.go`: add a `freelance []*roleEntry` collection, `freelanceCollapsed bool` (default true), and a `railRowFreelanceSep` row kind. Update `buildRows` to emit the Freelance header (only when ≥1 live freelance) below active orchestrators and above the Archive separator, expand its rows when not collapsed, and place archived freelance inside the Archive section. Extend `ToggleCollapse`/Space handling and the count rendering for the new section.
- [ ] 12.3 `internal/view/app.go` `populateRail`: partition non-coordinator roles by kind — route `freelance` roles into the rail's freelance collection (tagged with `OrchestratorID` for elapsed-time), keep `worker` roles in `entry.Roles`. Ensure the Stage-I dynamic refresh rebuilds the freelance section.
- [ ] 12.4 `internal/view/focus.go`: teach the focus machine a `coordPresent` (two-pane) flag so `Advance()` from RAIL jumps to AGENT and `Retreat()` from AGENT returns to RAIL when no coord pane is present. `internal/view/keys.go`: honor the flag for the arrow ladder (Enter already lands on AGENT).
- [ ] 12.5 `internal/view/layout.go` + `app.go` `refreshBody`: compose freelance mode (rail + full-width agent) vs project mode (rail + coord + agent) from the current selection's kind; on entry to freelance mode tear down the coord pane bridge/subscription and set `coordTask` to "". Drop the `Ctrl-→ coord` hint from the bottom bar in freelance mode.
- [ ] 12.6 Wire `applyRailSelection` (and the initial-selection path) to switch modes and (re)bind the full-width agent pane to the selected freelance role's argus task; set the focus machine's `coordPresent` flag accordingly.
- [ ] 12.7 Run `go test ./internal/view/... -race -count=1` until green.

## 13. Stage M — adopt the argus key-surrender contract

**Depends on:** Stage G (focus + key routing) and Stage L (focus modes)

- [x] 13.1 Write failing tests from the new scenarios (Prove-It). `keys_test.go`: Esc in RAIL triggers a `release` control frame and is NOT forwarded; Esc in COORD/AGENT is forwarded to the PTY and does NOT release; `?` in RAIL triggers a `help` frame and is NOT forwarded; `?` in a pane is forwarded. `control_test.go`: the sender marshals `release`/`hotkeys`/`help` envelopes to the exact contract JSON. `app_test.go`/`layout_test.go`: a focus-state change pushes a focus-appropriate `hotkeys` frame; the rendered surface no longer includes a bottom-bar row. Confirm each fails first.
- [x] 13.2 Add a `viewControl` sender (`internal/view/control.go`) that writes `release` / `hotkeys` / `help` envelopes as TEXT frames on the session `*websocket.Conn` (coder/websocket serializes writers, so this coexists safely with the SDK's binary surface writes). Thread the conn from `NewSessionFunc` into the `App`.
- [x] 13.3 `internal/view/keys.go` + `app.go`: in `RAIL` focus, route Esc → `release` and `?` → `help` (intercept, do not forward). Leave Esc/`?` forwarding intact in `COORD`/`AGENT`. Keep single Ctrl-Q → RAIL internal; do NOT bind double-Ctrl-Q (argus's failsafe owns it).
- [x] 13.4 `app.go` `OnFocusChanged`: push a focus-aware `hotkeys` dictionary (RAIL / COORD / AGENT bindings, `bar:true` on the operator-facing keys) on connect and on every focus change. Build the dictionaries from the same source of truth the retired `bottomBarText` used.
- [x] 13.5 `layout.go` + `app.go` `refreshBody`: remove hera's bottom-bar row from the Flex (argus renders the plugin-mode status bar). Retire `bottomBarText`; switch `OnHelp` from the in-surface modal to a `help` frame.
- [x] 13.6 Run `go test ./internal/view/... ./internal/daemon/... -race -count=1` until green.

## 14. Stage N — Rail model: coordinators as foldable rows + Archive expandos (mirror prototype)

**Depends on:** Stage I (rail rendering). Canonical reference: `docs/prototypes/rail-nav.html` (`visibleRows`, `renderRail`).

- [x] 14.1 Write failing tests from the rail requirements (Prove-It): coordinators render as foldable rows with chevron + live `(N)` count; agents nest under their coordinator; a worker that is also a coordinator renders foldable with its own children; folders-first ordering; no kind pills; status-driven icon; per-coordinator `Archive (N)` expando (collapsed default) holds archived children; top-level `Archive` holds archived root coordinators; `space` toggles coord/Archive folds.
- [x] 14.2 `internal/view/rail_list.go` + `app.go` `populateRail`: build the row tree from orchestrators/roles/bindings to match the prototype — render coordinators (root + sub via multi-binding) as foldable rows, nest agents, folders-first, drop pills, status icons. Reuse the freelance-by-repo section (Stage L).
- [x] 14.3 Add per-coordinator + top-level Archive expandos: partition archived roles/tasks into the owning coordinator's Archive (and archived root coords into the top-level Archive); render dashed `Archive (N)` rows; wire `space`/fold state per owner. Decide `l` listall's fate (retire or keep as show-all).
- [x] 14.4 Probe-validate: spawn/observe the open test coords+agents; `HERA_LIVE_PROBE=1 go test ./internal/daemon/ -run LiveViewProbe` and diff the rendered rail tree against `rail-nav.html` (icons, counts, folders-first, archive folds). Iterate until they match.

## 15. Stage O — Three-mode body + Enter-into-pane + present-pane focus ladder

**Depends on:** Stage N and Stage G (focus + key routing).

- [x] 15.1 Write failing tests: coordinator selection → full-width HERA pane (no agent); agent selection → HERA+AGENT split; freelancer → full-width AGENT; switching re-composes + tears down the absent pane; Enter enters the primary pane (coord→COORD, agent→AGENT, freelancer→AGENT); Enter on a header/expando folds; `^→`/`^←` traverse only present panes.
- [x] 15.2 `internal/view/focus.go`: generalize `coordPresent` into present-pane awareness (`coordPresent` + `agentPresent`); `Advance`/`Retreat` step only through present states.
- [x] 15.3 `internal/view/layout.go` + `app.go` `refreshBody`: three compositions (full-width HERA / split / full-width AGENT); center pane titled `HERA`; tear down the absent pane's subscription on mode switch.
- [x] 15.4 `app.go` `applyRailSelection` + `OnRailSelectEnter`: select → set mode + bind panes; Enter → enter primary pane (return FocusCOORD for coords, FocusAGENT for agents/freelancers); header/expando rows fold.
- [x] 15.5 Probe-validate the three modes + Enter + traversal against the prototype (drive keys with `HERA_PROBE_KEYS`/`HERA_PROBE_RAW`). Iterate until parity. (Live probe on the deployed daemon confirmed: freelancer → rail + full-width AGENT, no HERA; worker → rail + HERA + AGENT split with the center pane titled HERA. Rail tree + body-mode recomposition match the prototype.)

## 16. Stage P — Extended keyset: delete, prune, status, open-PR

**Depends on:** Stage N. (`a` archive lands with Stage N's archive model.)

- [x] 16.1 Write failing tests: `^d` opens a destructive confirm and, on confirm, destroys task+worktree+branch (warns on child agents); `^r` opens a confirm listing completed agents and prunes only those; `s`/`S` advance/revert the selected agent's argus status; `^p` triggers a PR for the selected task. Confirm no destructive op without confirmation.
- [x] 16.2 `internal/view/keys.go` + `app.go` + `internal/view/mutations.go`: wire `^d` (delete — extend the existing delete flow to destroy the argus task/worktree/branch), `^r` (prune-completed), `s`/`S` (status step via argus task-status endpoint), `^p` (open PR via the host/iris flow). Add the argus client methods needed. NOTE: argus `POST /api/maintenance/prune-completed` is master-gated and rejects hera's scope token, so `^r` instead prunes hera's managed completed set itself via per-task `DELETE /api/tasks/{id}` (which cleans each worktree + branch server-side). `^p` shells out to `gh pr create` from the worktree via os/exec (no argus PR endpoint exists).
- [ ] 16.3 Probe-validate: confirm dialogs render and the rail updates after archive/delete/prune against the prototype's overlays. Iterate.

## 16b. Stage P-fix — rail mutation keys usable (deadlock + addressability + feedback)

**Depends on:** Stage P. Live-repro: any rail mutation (`s` on a live worker, previously `a`) permanently froze the session — tview v0.42.0 `QueueUpdate` blocks until the queued func runs, so a `QueueUpdateDraw` issued FROM the event loop (the key handlers run there) deadlocks the loop on itself.

- [x] 16b.1 Write failing seam tests: a mutation handler returns before its svc call/refresh fires (recording fakes gated on channels); freelancer `a`/`s`/`S` route to the new task-direct ops verbs; header `s`/`S` step the coord role; non-applicable keys surface a feedback modal; a second mutation while one is in flight no-ops with feedback.
- [x] 16b.2 `internal/view/ops`: add task-direct verbs `ToggleArchiveTask` and `StepTaskStatus` (bypass the hera-binding lookup; reuse `s.Argus`); refactor role-based `stepStatus` to delegate.
- [x] 16b.3 `internal/view/mutations.go`: run every mutation path off the event loop — handlers capture the selection synchronously, hand off to a goroutine, and bounce all UI work (repop, modals) through the event-loop queue from that goroutine; modal confirm/submit callbacks follow the same pattern; in-flight guard; `notApplicable` feedback helper. `app.go`: carry the freelancer's argus archived state on `railSelection`; sync `l`'s toggle into `App.showArchived` so the Archive section actually reveals.
- [x] 16b.4 Run `go test ./internal/view/... ./internal/daemon/ -race -count=1` green; `openspec validate add-hera-view --strict` green.

## 17. Stage Q — Pane scroll + in-pane agent navigation

**Depends on:** Stage O.

- [x] 17.1 Write failing tests: `⇧↑`/`⇧↓` scroll the focused pane's scrollback without moving the rail selection; `⌘↑`/`⌘↓` (and `^↑`/`^↓`) move the rail selection to the next/prev agent while focus stays in a pane (re-enter the new selection's primary pane).
- [x] 17.2 `internal/view/keys.go` + pane/terminalpane wiring: implement scrollback scroll on the focused pane; implement in-pane selection nav that preserves pane focus. NOTE: in-pane nav fully works (skips headers/expandos + coord-less coords). `⇧↑/↓` keys are intercepted (never moved-rail / never forwarded to PTY) and a clamped scroll offset is tracked, BUT visible scrollback rendering is blocked upstream: argus-sdk `terminalpane@v0.0.2` keeps its emulator unexported and ships "no scrollback rendering". True visible scroll needs an argus-sdk paint hook surfacing `charmbracelet/x/vt` `ScrollbackCellAt`/`ScrollbackLen` — tracked as a follow-up; the scroll scenario is behaviorally wired but not visibly shipped.
- [ ] 17.3 Probe-validate scroll + in-pane nav against the prototype.

## 17b. Stage R — Live pane repaint on PTY output (decouple redraw from input)

**Depends on:** Stage O + the raw-keystroke-forwarding change. Once pane keystrokes are forwarded as raw bytes BEFORE tcell, the incidental keystroke-driven redraw is gone, so PTY output (echoed keys AND autonomous agent output) no longer repaints live until an unrelated event forces a draw.

- [x] 17b.1 Write failing test at the bridge→redraw seam: a chunk arriving on the bound task's upstream channel MUST invoke the pane's redraw callback (`internal/view/pane_bridge_test.go`).
- [x] 17b.2 `internal/view/pane_bridge.go` + `app.go`: wire the SDK terminalpane's `OnNeedRedraw` hook (fires once per non-empty ingested chunk) to `tview.Application.QueueUpdateDraw` via a redraw callback threaded into `newBoundPane`. Mirrors argus's plugin-pane wiring (`internal/tui/plugin_views.go`). The blocking `QueueUpdateDraw` back-pressures the consumer goroutine so a chatty task coalesces bursts without an explicit debounce. nil-redraw-safe for detached panes / tests / the pre-app-loop window.

## 17c. Stage S — Latest-binding fallback for role ops + dismissable themed modals

**Depends on:** Stage P (16b). Live-repro (keyset acceptance T3): `s` on an ARCHIVED role row errored "role 63 has no live binding" — archiving a task ENDS its binding (`end_reason='argus_archived'`) while keeping the `argus_task_id`, so the live-only lookup fails for EVERY archived row (and silently defeated the symmetric unarchive from 16b for exactly the rows that need it). Worse, the error modal could not be dismissed: every background rail repopulate runs `OnFocusChanged`, whose unconditional `SetFocus(rail)` stole tview focus from the open modal, so Enter routed to the rail tree behind the overlay — the operator had to `^Q^Q` out. The default tview modal styling (lavender contrast background) also clashed with the argus theme.

- [x] 17c.1 Write failing tests: db `GetLatestByRole` (live preferred, ended fallback, most-recent-of-several, none → `ErrNotFound`); ops `stepStatus` + `ToggleArchiveRole`-unarchive on ended-binding roles (fake argus client; live preferred over ended; clear "no argus task recorded" error when nothing is resolvable); view modal tests — `ShowError` moves tview focus to the modal, Enter dismisses + restores focus, Esc dismisses, `OnFocusChanged` while a modal is active does NOT steal focus (regression for the T3 trap), confirm Esc/Enter default to No + `y`/`n` runes decide, all modal surfaces render the argus theme (no tview contrast-lavender cells).
- [x] 17c.2 `internal/db/bindings.go`: add `GetLatestByRole` (most recent binding by `started_at`, id tiebreak, regardless of `ended_at`). `internal/view/ops`: add `GetLatestBindingByRole` to the `DB` port + `resolveBinding` helper (live → latest fallback); use it in `stepStatus` and `unarchiveBoundArgusTask`; `ops_adapters.go` wires the DAO.
- [x] 17c.3 `internal/view/modals.go` + `app.go`: every modal captures prior focus on open and restores it on close; `OnFocusChanged` withholds `SetFocus` while a modal is active (borders still repaint); confirm modals default-focus No per the `(y/N)` convention and accept `y`/`n` runes; all modals (error/confirm/input/form) styled with the argus theme (dark `ColorStatusBG` background, `ColorTitle` border/title, `ColorError` error text, themed buttons/fields).
- [x] 17c.4 Drive-by race fix surfaced by the new tests: `newBoundPane` assigned `tp.OnNeedRedraw` AFTER the bridge pump had started, racing the SDK terminalpane's consume goroutine (reads the hook per chunk) — pre-existing since Stage R, fired intermittently under `-race` in any `BuildApp` test. `pane_bridge.go` now splits construction (`newPaneBridge`) from pump start (`startPump`) so the hook assignment happens-before the first chunk send; chunk emission order is unchanged.
- [x] 17c.5 `go test ./... -race -count=1` green; `openspec validate add-hera-view --strict` green.

## 18. Validation + archive

- [x] 18.1 Run `openspec validate add-hera-view --strict`. Fix any issues.
- [x] 18.2 `go test ./... -race -count=1` green (excluding the known pre-existing `internal/mcp` test build break, tracked separately).
- [x] 18.3 Probe parity pass: with the open test coords/agents/freelancers, capture the live rail + each body mode via the probe and compare side-by-side against `docs/prototypes/rail-nav.html` — rail tree (icons/counts/folders-first/archive), three body modes, Enter-into-pane, focus ladder, full keyset, top/bottom chrome. Iterate until "as close as a TUI can get". Deploy via iris (`iris_push` → PR → squash-merge → `iris_reload`) between iterations. (Deployed via iris PR #12 squash-merged to main → `iris_reload` (main @ 2cc838f). Live probe self-validated the rail tree and the three body modes against the prototype. Remaining feel-check — Enter-into-pane, the full keyset under real argus key-surrender, chrome — is the human pass in 18.4.)
- [ ] 18.4 Manual smoke (Aaron): open hera's plugin view in argus and confirm the feel matches the browser — coord full-width / agent split / freelancer full-width, Enter-into-pane, `^→/^←`, `⌘↑/↓`, `⇧↑/↓`, `a`/`^d`/`^r`/`s`/`S`/`^p`, Esc→argus / `^Q^Q` failsafe, argus bottom bar shows hera's hotkeys.
- [ ] 18.5 After Aaron live-verifies, run `openspec archive add-hera-view`.

## 19. Fixits: color-independent selection marker + effective-state archive toggle

**Depends on:** Stage 18.3 live probing. Two live findings. (1) The rail's selected row was indicated ONLY by `theme.StyleSelected` text — invisible in any monochrome context (the live-probe grid renderer strips styling; screen readers; reduced-color terminals), so blind navigation landed mutation keys on the wrong rows. (2) A role can be hera-active + argus-archived (mixed flags from historical asymmetric toggles); the rail displays it as archived (`roleArchived`) but `ToggleArchiveRole` picked direction from `role.Archived` alone, so `a` on an Archive-expando row stamped a FRESH `archived_at` instead of clearing both sides.

- [x] 19.1 Spec deltas: selection marker glyph (`›`) in a reserved gutter on the selected row, every selectable row kind, space elsewhere (no column shift); `a` direction follows the EFFECTIVE rendered archived state (hera ∨ argus ∨ dead) via explicit verbs — mixed-flag and dead-row scenarios added. `openspec validate add-hera-view --strict` green.
- [x] 19.2 Failing tests first: `rail_list_test.go` marker-on-every-selectable-row-kind + marker-moves-without-column-shift (colorless `readScreen` dump assertions); `mutations_test.go` mixed-flag/dead/hera-archived rows dispatch `UnarchiveRole` (never re-archive), orchestrator explicit verbs; `ops/archive_test.go` explicit-verb coverage incl. the mixed-flag no-fresh-`archived_at` regression.
- [x] 19.3 `rail_list.go`: 2-col selection-marker gutter at the start of every row (`›` + `theme.StyleSelected` on the cursor row, space elsewhere); all row painters shifted right by the gutter.
- [x] 19.4 `ops/archive.go`: explicit `ArchiveRole`/`UnarchiveRole`/`ArchiveOrchestrator`/`UnarchiveOrchestrator` verbs; `ToggleArchive{Role,Orchestrator}` kept as thin flag-derived wrappers (documented as unsafe for mixed-flag rows). `mutations.go` `OnArchive` computes the effective state from the selection (`Archived ∨ ArgusArchived ∨ Dead`, mirroring `roleArchived`) and dispatches the explicit verb; `railSelection` carries `Dead`; `app.go` populates it. Freelance path unchanged (already effective-state-driven via `sel.ArgusArchived`).
- [x] 19.5 `go test ./... -race -count=1` green (view marker/mutation/ops suites + no regressions in async-mutate, modal focus/theme, archived visibility, symmetric unarchive, raw input, coalescer).

## 20. Fixit: archive branch resolves latest binding (symmetric with unarchive)

**Depends on:** 17c (resolveBinding) + 19 (explicit verbs). Live-repro (keyset acceptance T6): `a` on an ACTIVE role whose binding was previously ended (`end_reason='argus_archived'` — true of every role that was ever archived) stamped hera's `archived_at` but SILENTLY SKIPPED the argus archive: `ArchiveRole` still resolved via the live-only lookup and treated `ErrNotFound` as "nothing to archive". Verified flags after a live press: hera archived=1, argus archived=0 — the argus task wrongly stays active (argus's own UI shows it unarchived; future bindings/reconciles disagree). 17c wired `resolveBinding` into `stepStatus` and the UNARCHIVE direction only.

- [x] 20.1 Spec delta: the binding-resolution rule (live preferred, latest fallback) applies to BOTH archive directions; archive-with-ended-binding scenario added; cascade scenario notes it inherits the role-level resolution. `openspec validate add-hera-view --strict` green.
- [x] 20.2 Failing tests first: `ops/archive_test.go` — `ArchiveRole` on an ended-binding role MUST POST argus archive via the fallback (live preferred over ended guarded); `ArchiveOrchestrator` cascade inherits the fallback.
- [x] 20.3 `ops/archive.go` `ArchiveRole`: `GetLiveBindingByRole` → `resolveBinding`; skip the argus call only when NO binding ever recorded a task id. Cascade path inherits (it calls `ArchiveRole`).
- [x] 20.4 `go test ./... -race -count=1` green.
