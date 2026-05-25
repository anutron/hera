# Handoff: resume the hera-build coord role and drive remaining roadmap

You are taking over as the **coord** role of the `hera-build` hera orchestrator. The prior coord (this session) shipped hera-settings + a critical decode fix + landed an installer; the dogfood loop is now solid. Your job is to drive the rest of the hera roadmap via parallel worker streams, bouncing to Aaron only when human judgment is required.

## Background

Hera is a Go daemon at `/Users/aaron/Development/Personal/hera` (this worktree is `~/.argus/worktrees/Hera/read-next-md-overnight/`). It coordinates argus tasks with role-as-identity persistence, an MCP message bus, idle-gated auto-inject, and as of today an operator settings UI registered with argus's TUI. The build order per NEXT.md was settings → view → install; settings + install are done, view is next.

## What was done this session

- **hera-settings shipped + archived.** Two-field substrate form (`idle_debounce_seconds` 0-60, `auto_inject_enabled` bool), hot-reload via `Tracker.SetDebounce` + `Injector.SetAutoInjectEnabled`, persistence via existing `config` SQLite table, daemon `LoadPersistedSettings` at startup. Built by parallel worker fleet (W1-W4) over the hera dogfood loop. Base spec at `openspec/specs/hera-coordination/spec.md` now carries 5 new + 3 modified requirements. Archive at `openspec/changes/archive/2026-05-25-hera-settings/`.
- **v1 decode bug fixed** (`commit fb04c27`): `argus.PostTaskInput`'s response field `bytes` was declared `int`; argus emits a JSON string. Introduced `flexInt` local type with a tolerant UnmarshalJSON. Regression test in `internal/argus/client_test.go:TestClient_PostTaskInput_BytesAsString` reproduces the exact in-the-wild error. The fix unblocked `hera_send` for every caller; smoke-confirmed end-to-end with auto-execute (`delivery_mode: idle_submit` arrived as a new turn in coord's PTY).
- **Settings-section wire schema aligned with argus** (`commit ecee1b6` by a sub-agent worker that read argus's source at `/Users/aaron/Development/Personal/argus/`): added `Title` to `SettingsSectionDefinition`, renamed `SettingField.Name`→`Key`, added `Label`, fixed unregister path from `/sections/{name}` → `/sections/{scope}/{title}`, fixed `SettingsSectionResponse` shape. Fixed multiple wire-format mismatches in one pass after a series of crash-loop reinstalls.
- **LaunchAgent installer landed on main** (separate worker, on main as of today). Setup is `./setup.sh --yes`. Bootstraps via launchctl with `KeepAlive: SuccessfulExit=false` so the daemon respawns on crash but stays down on graceful exit.

## Current state

- **Branch:** `argus/read-next-md-overnight`, 12 commits ahead of `main`, force-pushed to origin, all tests green (`go test ./... -race -count=3`).
- **Daemon:** live, registered with argus, settings-section rendering in argus TUI, all six MCP tools (`hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`) + the settings_save callback are reachable.
- **What works:** every hera tool returns success. Auto-execute path confirmed (smoke worker's `hera_send` to coord landed as a new turn in coord's PTY with `delivery_mode: idle_submit`).
- **What doesn't:** argus's settings-callback proxy doesn't forward the registered `auth_header` to hera's `/mcp/settings_save` route, so the operator UI renders but **save callbacks 401**. Filed in FOLLOWUPS.md. Substrate-side fix needed before settings UI is end-to-end usable.

## Resume the coord role (first step after a `/clear`)

The hera_* MCP tools become available again on a fresh session. Your first call:

```
hera_new_orchestrator(
  cwd=$PWD,                       # this worktree
  name="hera-build",
  coordinator_role_name="coord",
  mission="Drive remaining hera roadmap (view, polish, substrate followups)",
  constraints="Bias toward spawning sub-agents per Aaron's directive. Spec-first via OpenSpec for any new behavior change."
)
```

Per the spec ("Existing orchestrator with no live coordinator binding resumed"), this rebinds the existing `coord` role under `hera-build` to your new argus task. Response will carry `created: false`. All prior history (orchestrator row, prior bindings, messages addressed to coord) is preserved.

Right after the resume, **check `hera_inbox`** — prior-coord may have left a note-to-self for you on the message bus (Aaron's new pattern — dogfooding cross-incarnation messaging).

## Key decisions

- **Bias toward sub-agents** (Aaron's directive mid-session). Spawn workers via `mcp__argus__task_create` for any meaningful work; do not edit the worktree directly when avoidable. Coord's job is orchestration + integration + ack-and-bounce, not implementation.
- **Spec-first via OpenSpec** for any behavioral change. Use `/spec-writer` to draft artifacts. Get user approval via `/plannotator-specs` BEFORE workers start implementation. CLAUDE.md is explicit on this.
- **Workers self-join via `hera_join(kind=worker)`** rather than meta-pre-set. `task_create` has no meta param, and `hera_join` mirrors `meta:hera.role` on attach anyway. Auto-adopt is the other path but requires meta to be set out-of-band.
- **Decode fix uses `flexInt`** — tolerant decoder that accepts int or string-encoded int. Pattern for similar wire-drift in the future.

## Remaining work (the DAG)

### Parallel work-streams (can launch immediately)

**A. hera-view design pass.** Research + draft design.md for the plugin view. Output: a brainstorm-ready change folder `openspec/changes/hera-view/` with proposal + design + open questions. **No code.** Aaron-reviewed before any impl spawn. Open questions per NEXT.md:

- TUI library: bubbletea vs tview vs lipgloss vs raw ANSI (my v1 lean was bubbletea for 2026-era Go TUIs).
- PTY proxy pattern: argus exposes SSE for output + POST for input per task; hera's plugin-view contract is WebSocket-based. Splicing + reverse-routing keystrokes.
- Key bindings (argus reserves only `Esc`).
- "Each pane is just claude" decision worth re-confirming with fresh eyes.

**B. setup.sh stale-binary fix.** Step 1 (`Build`) of `setup.sh` checks `[[ -x bin/hera ]]` and skips `go build` if the binary exists, so source changes silently don't propagate. Make `go build` always run (Go's own cache handles "nothing to rebuild" fast) OR compare source vs binary mtime. Small change, single file.

**C. Idle-gate followup downgrade/closeout.** A worker flagged "auto-execute doesn't seem to be working" mid-session. The smoke worker proved auto-execute fires correctly with `delivery_mode: idle_submit` on a real cross-agent send. The original report was almost certainly the decode error masquerading. Downgrade or close the FOLLOWUPS entry. Possibly add a guard test.

**D. Argus auth-header drop substrate ticket.** File with drn via the open substrate coord task `1779491424986011000` (via `mcp__argus__task_message_send`). Provide: the symptom (settings-save 401), the root cause (callback proxy doesn't forward `auth_header`), the proposed fix (argus reads `auth_header` from the section row and forwards it on the proxy POST). Out of our control until fixed; track and re-test once drn ships it.

### Blocking work (gated on A's approval)

**E. hera-view implementation.** Worker fleet for the chosen TUI library + PTY proxy + key bindings. Will likely break into 3-4 parallel streams once the design is approved.

### Lower priority (backlog, not part of this session's DAG)

- ~25 low-priority test polish items (already in FOLLOWUPS, low value, tackle when convenient).
- Atomic role+binding insert across DAOs (v1.1 architecture).
- Cursor advancement on handler failure (v1.1 architecture).
- **`hera_note_to_self` mechanism** (concept surfaced this session). Today the only way for one coord incarnation to leave a message for the next is to `hera_send` to your own role and not ack — but that also injects the message into your current PTY, and the don't-ack contract is fragile. A clean codification: a new tool `hera_note_to_self(cwd, body)` OR a `note_to_self: true` flag on `hera_send` that persists to the messages table addressed to caller's role, skips the inject step, leaves `read_at` unset until a future incarnation's `hera_inbox` surfaces it. ~50 LOC + spec amendment. Defer until after hera-view ships; potentially fold into a hera-bus-v2 if other bus changes (broadcast, priority, expiry) are wanted at the same time.

## Important context (gotchas)

- **The hera MCP tools drop from this session if argus restarts.** Your session's MCP client connection to argus doesn't auto-reconnect. Spawned workers get fresh connections. If you lose hera_*, use spawned workers as proxies.
- **Decode fix shipped means the v1 dogfood `hera_send` bug is gone.** Tool calls return clean JSON. Cross-agent sends arrive as new turns when recipient is idle.
- **Settings-save round-trip is BROKEN** until argus fixes auth-header forwarding. Settings show in UI but won't persist via the TUI. Workaround: direct `ConfigDAO.Set` + daemon restart.
- **Aaron is "in" the session** — he wants to be bounced to when judgment is needed, not when routine work is happening. Use `AskUserQuestion` only for decisions, not status updates.
- **Worker prompts should explicitly tell workers about the decode fix being shipped** so they don't get confused by the FOLLOWUPS entry.
- **Argus's MCP server CAN restart underneath you.** If a worker reports tool calls failing mid-stream, suspect argus restart, not hera.

## Important file paths

- `NEXT.md` — the original roadmap. Read first if unfamiliar.
- `FOLLOWUPS.md` — known bugs + deferrals. The "Dogfood loop reliability" section is now mostly resolved (decode fix shipped); leave the entries as documentation.
- `openspec/specs/hera-coordination/spec.md` — the base spec, source of truth for hera's behavior.
- `openspec/changes/archive/2026-05-25-hera-settings/` — the just-shipped change for reference patterns.
- `internal/argus/settings.go` — current wire schema for settings-section. If you hit more wire mismatches, argus source is at `/Users/aaron/Development/Personal/argus/`.
- `internal/argus/tasks.go` — the decode-fix lives here (`flexInt` type).
- `internal/daemon/run.go` — Start/Stop wiring. Stage 7 integration landed here.
- `setup.sh` — installer. Stale-binary bug is in step 1.

## Files to read first

1. `NEXT.md`
2. `FOLLOWUPS.md`
3. `openspec/specs/hera-coordination/spec.md` (the base spec)
4. Recent commits: `git log --oneline main..HEAD`
5. This file (HANDOFF.md) — that's me.

## Where the prior coord parked things

- This worktree (`~/.argus/worktrees/Hera/read-next-md-overnight/`) is argus task `1779727800185385000`. When it ends, your task replaces it as coord.
- Substrate coord task: `1779491424986011000` (drn's substrate coordinator — use `mcp__argus__task_message_send` to that ID for argus-side questions).
- Tests pass locally: `go test ./... -race -count=3`.
- Branch is on origin. No PR opened (Aaron may want to open one separately or keep this branch as a working branch).

Go.
