# Next – picking up after hera v1

Read this first if you're starting a fresh session on hera. The committed files capture most of the design and history; this page collects the things that lived only in the v1-build conversation, plus the operating model for the dogfood loop.

## Current state (2026-05-25)

- **v1 shipped.** Branch `argus/ludwig-argus-coordinator` was fast-forwarded into `main` at `36a4e64`, then one more commit on top: `f186007 Update stale .gitignore entry`. Local and origin `main` are in sync.
- **Repo renamed** ludwig → hera on disk (`~/Development/Personal/hera`) and on GitHub (`anutron/hera`). The argus-owned branch slug `argus/ludwig-argus-coordinator` and the argus worktree path `~/.argus/worktrees/Ludwig/ludwig-argus-coordinator` stay as-is – they're argus's task slug from when this work spawned.
- **`./bin/hera` is built** and copied to `~/bin/hera` via `setup.sh` (idempotent – re-run any time).
- **`~/.hera/` is set up** with mode 0700, scope token at `api-token` with mode 0600.
- **OpenSpec base specs** at `openspec/specs/hera-coordination/spec.md` and `openspec/specs/hera-substrate-link/spec.md` (the latter lands when the W3 integrator archives `add-argus-reconnect`). The hera-v1 change folder is archived at `openspec/changes/archive/2026-05-25-hera-v1/`.
- **`hera-settings` shipped** (last session). Two operator knobs – `idle_debounce_seconds` and `auto_inject_enabled` – registered as a form-type settings-section with argus, persisted via the existing `config` table, hot-reload via `Tracker.SetDebounce` + `Injector.SetAutoInjectEnabled`. Archived at `openspec/changes/archive/2026-05-25-hera-settings/`. Settings-save callback is currently blocked end-to-end because argus's TUI callback proxy drops the registered `auth_header` – see FOLLOWUPS for the substrate ticket. Operators can work around by writing directly to the `config` SQLite table.
- **`hera-install` shipped** (last session). LaunchAgent install is now managed by `setup.sh` step 5 – see the `add-launchagent-install` change folder / archived spec. The previous "installed by vanilla Claude outside this repo" plumbing is superseded.
- **`hera-substrate-link` shipping this session** (currently in W3 integration). Hera discovers argus's REST port via the daemon socket's `Daemon.Ports` RPC at startup, watches `~/.argus/daemon.pid` mtime + socket ping for restarts, force re-registers tools and settings on detection, gates `hera_*` MCP tool calls with a degraded-state preamble during recovery, and surfaces link state on `hera_status`. To-be-archived as `openspec/changes/archive/2026-05-25-add-argus-reconnect/` when the W3 integrator completes – update this path post-archive. A corresponding argus-side change (commit `adaf4a1` on branch `argus/add-daemon-ports-rpc`) added the `Daemon.Ports` RPC.

## Locked design decisions (do not re-litigate)

These were settled across the v1 build, morning review, spec-audit, and ralph-review passes. They're already reflected in the base spec, but listed here so the next session doesn't waste cycles re-thinking them.

- **Six MCP tools**, no more, no less in v1: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`. Every one takes `cwd` as a required input.
- **Role-as-identity model.** Orchestrators have no `argus_project` column; roles do, write-once. Roles outlive argus task lifecycles via the bindings table.
- **`hera_new_orchestrator` is the canonical "be an orchestrator" entry point.** `hera_join(kind=coordinator)` was a stopgap that's been removed; the join verb only accepts worker / freelance now.
- **No coord → user routing.** Coordinators talk to the human through their own Claude pane. Coordinator senders of `hera_send` without an explicit `to` are rejected.
- **Two-second idle debounce.** Auto-injection (body + `\n`) fires only when `session.idle` has been the active state for ≥2 seconds. Tunable via `Config.IdleDebounce` but not yet via the settings UI (that's hera-settings).
- **Meta mirror is best-effort.** `meta:hera.role=<kind>` on binding create and `meta:hera.thread_status=<status>` on `hera_status` – failure does not undo the local state. Spec amended to reflect this explicitly.
- **Build order for follow-ups: settings → view.** Settings first (smallest substrate footprint), view second (biggest UX payoff). Install (`add-launchagent-install`) shipped via setup.sh.

## What shipped this session: `hera-substrate-link`

Hera silently broke every time argus restarted: argus picks its REST port dynamically (7742, 7743, 7744, ...) via `bindWithRetry`, and hera was hardcoded to `:7743`. After the latest argus restart the port landed on 7745 and every `hera_*` MCP tool 404'd because `TaskForCwd` was hitting the wrong port. There was also no restart detection mid-session.

The fix is a small isolated resilience layer in `internal/argus/`. No new product behavior – only new resilience around existing behavior. New capability spec at `openspec/specs/hera-substrate-link/spec.md` (lands when W3 archives the change).

Four major surfaces added:

- **Socket port discovery.** New `internal/argus/socket.go` JSON-RPC client over `~/.argus/daemon.sock` exposing `Daemon.Ports()` and `Daemon.Ping()`. Daemon bootstrap queries at startup and hard-exits if it fails.
- **Pid-mtime watcher.** New `internal/argus/Watcher` polls `~/.argus/daemon.pid` mtime every 1 second + pings the socket. On mtime change OR ping failure, fires a recovery routine that re-runs discovery and atomically swaps the client's baseURL.
- **Force re-register methods** on `mcp.Registrar` and `settings.Registrar` so the watcher drives immediate re-registration instead of waiting for the 5-minute heartbeat. The existing heartbeat 404 path also calls the same recovery routine as a passive fallback.
- **Degraded-state MCP gate.** While recovery is in progress, `hera_*` calls return a structured error `argus link recovering, retry in a moment` instead of failing the connection. An `argus_link` field (`healthy | recovering | down`) on `hera_status` surfaces link state without reading logs.

The argus-side companion (commit `adaf4a1` on branch `argus/add-daemon-ports-rpc`) added the `Daemon.Ports` RPC. Awaiting merge into argus master + a daemon restart.

## What needs design BEFORE `hera-view`

With `hera-settings` and `hera-substrate-link` both shipped, `hera-view` is the only remaining v1.x roadmap item. It's the biggest UX payoff but has open design questions that the v1 brainstorm sketched but didn't pin. Don't try to scaffold it without resolving these first.

**Shape (decided, but worth verifying with fresh eyes):**

- Three columns: narrow left rail + two equal-width panes
- Left rail = projects (orchestrators) with their agents (non-coordinator roles) underneath. Coordinator is not navigable; it's implicit per project
- Middle pane = the coordinator's Claude PTY for the selected project
- Right pane = the selected agent's Claude PTY
- Navigation: ctrl-Q (or j/k) moves through agents in the rail; crossing project boundary switches projects
- Focus: cmd/ctrl-← shifts to coord pane; cmd/ctrl-→ shifts to agent pane

**Open questions:**

- **TUI library.** bubbletea, tview, lipgloss, or raw ANSI? Greenfield project; my brainstorm-time lean was bubbletea (most idiomatic for greenfield Go TUIs in 2026 + plays well with the WebSocket-fed ANSI byte stream). Worth re-considering.
- **Embedded-terminal proxy pattern.** Argus exposes `GET /api/tasks/{id}/stream` (SSE) for PTY output and `POST /api/tasks/{id}/input` for keystrokes. The substrate's plugin-view contract is WebSocket-based: hera streams ANSI bytes for its entire surface, argus routes keystrokes back. So hera must: (a) open SSE to each visible task's output, (b) splice the bytes into the right pane region of its own surface, (c) forward keystrokes from the focused pane back to that task's input endpoint. Cost: real, but tractable. Confirm with the coordinator agent before committing.
- **Reserved keys.** Argus reserves only `Esc` (exits the plugin view). Everything else – ctrl-c, tab, function keys, modifier combos – forwards to hera. So hera owns the entire keyboard while focused; can pick its own bindings freely.
- **What appears in each pane other than the embedded PTY?** Per Aaron's brainstorm decision: "each pane is just claude." No custom rendering, no header bar, no feed. Just the live terminal of the bound task. (A richer dashboard surface – decisions, questions, threads – was discussed but deferred until after the embedded-terminal MVP works.)

**Substrate behaviors required (verified by coordinator's smoke harness):**

- Plugin views CRUD: `POST/GET/DELETE /api/plugins/views`
- WebSocket contract on the registered callback URL
- PTY output SSE: `GET /api/tasks/{id}/stream`
- PTY input POST: `POST /api/tasks/{id}/input`
- All work end-to-end against the current argus binary.

**Brainstorm prompt for hera-view:** "Brainstorm hera-view. Shape is roughly settled (rail + two embedded Claude PTYs). Open questions: TUI library choice, PTY proxy implementation pattern, key bindings. Need a design pass before scaffolding the change folder."

## `hera-install` future work

`hera-install` shipped (see "Current state"). Items that would justify reopening it:

- **Linux/systemd parity.** Current implementation is macOS-only with a non-Darwin skip. Adding a `linux-systemd` requirement to the `hera-install` capability is the next reasonable extension.
- **Programmatic install verbs.** If users ever need to install/uninstall the LaunchAgent from a script without running setup.sh end-to-end, a `hera install` / `hera uninstall` pair of subcommands would be the right shape.

## Backlog (post-1.0 polish)

- **Hera-aware skills.** Today, when a sub-agent is spawned via `/fixit` (or any other skill that backgrounds an agent), the spawning prompt has to explicitly tell the worker to call `hera_join`. A hera-aware integration would have those skills auto-detect they're running in a hera-coordinated project and self-join the orchestrator without prompt plumbing. Three ways to land it, from lowest-touch to highest:
  - (a) Project-level `CLAUDE.md` snippet teaching general skills to look for hera (e.g., "if `~/.hera/api-token` exists and you're a sub-agent spawned by argus task_create, call `hera_join` before doing anything"). Zero changes to upstream skills.
  - (b) Modify individual skills (`/fixit`, etc.) to detect hera and self-join. Cleaner but touches many skills and they aren't all in our control.
  - (c) Ship a thin wrapper skill (`/hera-fixit`, `/hera-debug`, …) that wraps the upstream skill with a `hera_join` preamble. Adds a layer of indirection but lets us roll out one skill at a time.

  Bookmarked 2026-05-25 after Aaron noticed /fixit's worker had to be hand-told to join. Picking the right path is itself worth a quick brainstorm — defer until after `hera-view` ships.

- **Host-ops argus plugin** (separate concern, captured in memory). A future argus plugin that lets sandboxed Claude sessions perform trusted host-side ops (git merge, build, push) without manual paste. Substrate-scope, not hera-scope. Parked.

## Dogfood operating model

Once hera is running:

1. **Bootstrap an orchestrator.** From any argus task with MCP access to hera, call:

   ```
   hera_new_orchestrator(cwd=$PWD, name="<project-name>", coordinator_role_name="coord", mission="...")
   ```

   This creates the orchestrator + a coordinator role + a binding tying the calling argus task to that role.

2. **Spawn workers via inbox-dispatch.** The canonical pattern:

   1. **Coord pre-sends the full build prompt** via `hera_send(to="<role-name>", body="<entire worker prompt>")`. Since the role has no live binding yet, hera queues the message per the "messages queued when recipient has no live binding" requirement.
   2. **Coord spawns a thin argus task** via `mcp__argus__task_create` with a minimal prompt: `"You are <role-name>. Call hera_join(kind=worker, orchestrator='<coord's orchestrator>', role_name='<role-name>') then hera_inbox(cwd=$PWD) to read your build instructions."` Argus task body stays small.
   3. **Worker joins**, claims the binding for that role, and reads the queued message via `hera_inbox` – that's the full build prompt. Workers acknowledge via `hera_mark_read` and execute. Status updates flow back via `hera_send` (default-routes to coord).

   This dogfoods the inbox+routing layer on every worker spawn, keeps the actual build prompt in hera's message store (searchable, replayable, survives worker re-incarnation), and avoids stuffing multi-page prompts into argus's task body. The meta-on-task_create path is NOT used – it would require a substrate primitive that does not exist today.

3. **Inter-agent messages.** `hera_send` from worker default-routes to coordinator. From coordinator, supply explicit `to=<worker-role-name>`.

4. **Resume across incarnations.** When a worker task is archived, its binding ends but the role survives. Re-incarnate by spawning a new argus task in the role's `argus_project`, then `hera_join(cwd)` to claim it.

5. **Coord discipline for spawning workers.** Always pass `meta:hera.role=worker` and `meta:hera.mission="..."` so hera auto-adopts the new task as a worker under the calling coord's orchestrator. Without the meta, the worker has no hera identity until it explicitly `hera_join`s – and forgetting either step means the worker cannot use `hera_send` to report status, falling back to argus's `task_message_send`. The argus fallback works but bypasses hera entirely (no dogfood). Today's session demonstrated this failure mode: the coord forgot to set meta on three workers and they all fell back to argus's message bus.

## Process learnings from this session

- **Latent resilience holes can become blockers without warning.** The hardcoded `:7743` baseURL was fine until the next argus restart moved the REST port. Resilience-layer work that lives in "for later" can flip to "blocker now" with no transition.
- **Degraded dogfood is a real signal.** Workers reaching the coord via argus's `task_message_send` instead of `hera_send` means the canonical hera bus is unavailable. When that happens, fix the bus before the fallback path hides the real bug.
- **Argus's task-message PTY notification is not auto-submitted on idle.** Aaron had to hit enter to surface `[argus] new message from task X`. Hera's `Injector` auto-submits hera_send payloads on idle, but that's a hera-side behavior. Open question: should argus's own notification also auto-submit? Filed as an observation – needs more data before pinning.
- **Spec-first held up under time pressure.** The design conversation locked in ~5 user turns, scaffold worker produced clean artifacts on first pass, validation was green, three execution workers landed parallel work with no scope conflicts.
- **Parallel substrate + hera changes work when the wire contract is locked first.** Today argus added `Daemon.Ports` while hera added the caller. Both shipped without integration testing against each other, against a written interface both committed to upfront. Cleaner than serializing and worth the upfront contract discipline.

## Where everything lives

| What | File |
|---|---|
| What shipped in v1 | `openspec/specs/hera-coordination/spec.md` (base spec) |
| Why the v1 design landed this way | `openspec/changes/archive/2026-05-25-hera-v1/design.md` |
| The OpenSpec implementation plan v1 followed | `openspec/changes/archive/2026-05-25-hera-v1/tasks.md` |
| Day-by-day narrative + morning review + audit pass | `OVERNIGHT_LOG.md` |
| Items deferred to v1.1 with rationale + escalation triggers | `FOLLOWUPS.md` |
| Install + usage | `README.md` |
| One-shot setup | `setup.sh` |

## Suggested first prompt for the next session

Start the new session in `~/Development/Personal/hera` on a fresh branch (e.g., `argus/hera-view` if argus-managed, or `hera-view` if you're on a vanilla Claude topic branch):

> Read NEXT.md, OVERNIGHT_LOG.md, and FOLLOWUPS.md in this repo. The next major v1.x roadmap item is `hera-view`. Drive the brainstorm pass per the "What needs design BEFORE `hera-view`" section above – capture TUI library choice, embedded-PTY proxy pattern, and key bindings – then scaffold the OpenSpec change folder.

## Open thread with the argus coordinator agent

The coordinator agent (argus task id `1779491424986011000`) holds substrate context – every PR, every divergence from PLAN.md, smoke-harness results. It's reachable from any argus task via `mcp__argus__task_message_send`. If hera-settings or hera-view hit a substrate gap, send a question there before scoping a workaround.

The last message exchange was the dogfood-kickoff readiness check (acked); the coordinator's next expectation is a check-in when hera's first orchestrator is alive or when we hit a substrate behavior that needs input.

Today's session added one more substrate ask: argus's `Daemon.Ports` RPC (shipped on branch `argus/add-daemon-ports-rpc`, commit `adaf4a1`, awaiting merge into argus master + a daemon restart). The substrate coord task is still the canonical channel for substrate questions.
