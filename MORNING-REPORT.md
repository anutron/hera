# Morning report: add-hera-view overnight run

**Coord:** hera-1.0 / coord (binding 7, argus task 1779746718642643000)
**Worktree:** `/Users/aaron/.argus/worktrees/Hera/find-resume-orchestrator-role`
**Branch:** `argus/find-resume-orchestrator-role`
**Report written at:** 2026-05-26 (overnight pickup)

This report is the morning hand-off from the overnight coord session. It describes what landed, what's parked, the verification protocol, and the merge command (also staged on the argus clipboard).

## TL;DR

- **Phase 1 — spec deltas:** done and pushed. Commit `21fbd77` on `argus/find-resume-orchestrator-role`. `openspec validate --all --strict` passes.
- **Phase 2 — workers spawned:** 11 argus tasks spawned via the depswatcher dependency graph (plan_slug `add-hera-view`). Status per stage is in §Worker results below — that section is the live truth, updated as workers reported in.
- **Phase 3 — integration:** for any stage that completed cleanly, its branch is listed in §Worker results. The morning merge command is in §Merge command and on Aaron's argus clipboard.

## Deviation from HANDOFF-OVERNIGHT.md

Workers were spawned with `base_branch="origin/argus/find-resume-orchestrator-role"` rather than the `origin/main` value the handoff specified. The spec deltas commit (`21fbd77`) is on the coord branch, not on main, so workers branching from main would not see the new spec files they were asked to implement against. The fix is to merge the coord branch first in the morning, *or* merge any one stage branch (which all carry the spec commit transitively) — see §Merge command. Either path lands the specs on main.

## Worker results

Each row will be updated as the worker reports done (or blocked) into the coord inbox. Initial state at handoff is `spawned`. As of report-writing time, only batch 1 has had time to fully start; batch 2-5 are still waiting on their dependency chain via argus's depswatcher.

| Stage | Task ID | Branch | Status | Commit | Notes |
|-------|---------|--------|--------|--------|-------|
| A — Migration + DAOs | `1779783499697319000` | `hera-view-stage-a-migration-dao` (note: no `argus/` prefix — worker pushed under the role-slug, not the argus-task name) | **done** | `4f0abae` (+ tick `e27dcdf`) | Migration 0003_archived_at: nullable archived_at on orchestrators+roles, indexes, partial UNIQUE indexes scoped to active rows. DAOs: Archive/Unarchive/Rename on both. List + GetByName default to active-only; ListInclusive variants added. Worker also tightened GetByName/GetByOrchestratorAndName to active-only (spec only requires for List*; tightening preserves "newly created" check in hera_new_orchestrator once archived rows exist). |
| B — PTY proxy | `1779783511038358000` | `argus/hera-view-stage-b-pty-proxy` | **done** | `a260a1f` (+ tick `e4f37ab`) | Per-task Subscription with snapshot+SSE upstream loop; fan-out to N listeners over single upstream; ring buffer 256 KiB; bounded reconnect backoff (250ms→10s); snapshot-reconnect dedup via localTotal cursor; drop-on-full listener channels. Atomic Subscribe pairs ring snapshot with chunks channel (no gap, no overlap). `go test` + `go vet` clean. |
| C — Plugin view registrar | `1779783521722828000` | `argus/hera-view-stage-c-view` | **done** | `44c73f5` (+ tick `4d9137d`) | Tests green. Worker flagged: argus's plugin-view substrate has no separate heartbeat endpoint — `RegisterView` itself is the heartbeat shape (idempotent on 409 via GET-and-match). `HeartbeatView` is a passive liveness check (returns `ErrPluginViewMissing` if gone). `DeleteView` idempotent on 404. Stage J should call `RegisterView` every 5 min directly. |
| D — wsscreen | `1779783534043050000` | `argus/hera-view-stage-d-wsscreen` | **done** | `5a9ea9b` (+ tick `3bfa8a5`) | High-risk piece landed clean. Wraps `tcell.NewTerminfoScreenFromTtyTerminfo("xterm-256color")` via a custom Tty; Show/Sync flush as a single binary WebSocket frame. Inbound binary → tcell.EventKey via the tcell parser; text envelopes → EventResize / EventFocus. Conn is an interface for testability. Deps added: `github.com/gdamore/tcell/v2 v2.13.10`, `github.com/coder/websocket v1.8.14`. Tests green. Unblocks E + K. |
| E — WS server route | `1779783591683239000` | `argus/hera-view-stage-e-ws-server` | **done** | `2d65867` (WIP salvage) → `b814c85` (worker fix) + tick `95d4f7e` | Slow finish — I prematurely "salvaged" E from its worktree because `task_list` showed `complete` with stale timing while the worker was still running. The worker came back, saw my WIP commit, fixed the race in it (`b814c85`), and ticked the tasks. Final state: tests green at `-count=20 -race`. Root cause of the test failure I caught: dual-close path on supersede — graceful `Close(StatusGoingAway)` racing with `defer CloseNow` stalled teardown ~2s under -race. Fix: drop the graceful close-frame, use `CloseNow` exclusively (argus's connector treats any close the same). Task §5.2 marked `[~]` partial — per-connection lifecycle is wired behind a `SessionFunc` injection point; the wsscreen + tview.Application construction itself is deferred to Stage J. |
| F — tview layout | `1779783604871053000` | `argus/hera-view-stage-f-tview-layout` | **done** | `840d893` (+ tick `e0b1bfe`) | Adds tview (first consumer in hera) — `github.com/rivo/tview v0.42.0`. Defines `PaneSource` interface in `internal/view/types.go` for Stage J to wire (`SubscribeTask(taskID) → (snapshot, ch, unsub)`). `StreamPane.Replace(snapshot, ch)` for rail-nav pane swaps. Rail uses `tview.TreeView` with `roleReference{OrchestratorID, RoleID, RoleKind, ArgusTaskID}` payloads for Stage G/H. Live bindings marked `* `. archived_at filtering deferred to Stage A merge (default `List()` returns all rows on this branch). `BuildApp` returns `*App` not raw `*tview.Application`; `App.Close()` releases subscriptions + stops StreamPane goroutines. Tests green. |
| G — Focus + keys | `1779783666646818000` | `argus/hera-view-stage-g-focus-keys` | pending (deps F) | – | |
| H — Rail operations | `1779783682291226000` | `argus/hera-view-stage-h-rail-ops` | **done** | `10043c2` (+ tick `5f33c55`) | Substrate question resolved: D5's `POST /api/tasks` shape matches argus's `CreateTaskReq` exactly (Name, Prompt, Project, Backend) — no stub needed. Adds `internal/argus/tasks.go` (CreateTask + meta PUTs, ArchiveTask, UnarchiveTask, CreateProject). `internal/view/ops/` is a new package with narrow **injected interfaces** (DB, ArgusClient, WorktreeRemover, Logger) — Stage J writes the adapter. Per-file: new.go (spawn argus task w/ `hera_new_orchestrator` prompt), rename.go (per-scope uniqueness), delete.go (binding-end + archive + git worktree remove with audit log + soft no-op on missing dir), archive.go (toggle + argus archive POST; orch archive cascades, unarchive does not), listall.go (in-memory toggle), help.go (static HelpContent), resurrect.go (clear archived_at + spawn task in stored argus_project w/ hera_join prompt). **`delete_test.go` uses a REAL temp git worktree fixture** to exercise `git worktree remove --force` end-to-end. Tests + vet green. |
| I — Rail events | `1779783693718489000` | `argus/hera-view-stage-i-rail-events` | **done** | `8d5e4a6` (+ tick `74eee4b`) | Broadcaster {Emit, Subscribe, Close} on `*db.DB`. Non-blocking drop-on-full for slow subscribers. Emits on Orchestrators.Create, Roles.Create, Bindings.Create, Bindings.End. Idempotent Creates don't emit (tested). `internal/view/rail.go` adds `RailRefresher` with ~100ms debounce — tview-agnostic; Stage J wraps callback in `QueueUpdateDraw`. **Integration gap:** Stage A's Archive/Unarchive/Rename DAO methods do NOT emit yet (this branch didn't see A's code). Stage J merge needs to add `db.Events.Emit(...)` calls to those methods. |
| J — Daemon wire-up | `1779783745520579000` | `argus/hera-view-stage-j-daemon` | pending (deps A,B,C,D,E,F,G,H,I) | – | |
| K — Smoke test | `1779783759162447000` | `argus/hera-view-stage-k-smoke-test` | pending (deps J) | – | |

To recover the current truth in the morning, run:

```sh
argus task list --plan add-hera-view  # or via the MCP tool: mcp__argus__task_list with plan_slug="add-hera-view"
mcp__argus__hera_inbox(cwd=$PWD)      # any worker done reports / blockers still unread
```

## Verification protocol (Aaron, morning)

1. **Survey worker results.** Read `task_list --plan add-hera-view` and the coord inbox; identify any stage that reported `blocked` or that's still `in_progress`. For any blocker, the worker's hera_send body should name the blocker; see §Confidence-low spots below for the candidates I anticipated.

2. **Validate the change folder.** From this worktree:
   ```
   openspec validate add-hera-view --strict
   openspec validate --all --strict
   ```

3. **Merge the spec branch + stage branches** (see §Merge command).

4. **Rebuild + reload the daemon:**
   ```
   cd ~/Development/Personal/hera
   go build -o bin/hera ./cmd/hera
   launchctl kickstart -k gui/$(id -u)/com.anutron.hera
   tail -f ~/.hera/launchd.log
   ```

5. **Open hera-view in argus.** The plugin view should appear in argus's hotkey list. Default tentative hotkey is `Ctrl-H` (see §Open questions below — may have shifted). With the view open:
   - Three panes render (rail left, COORD middle, AGENT right).
   - Top bar shows literal `HERA`.
   - Bottom bar shows focus-aware hints.
   - Cmd/Ctrl-→ advances focus through `RAIL → COORD → AGENT`; Cmd/Ctrl-← retreats. `Enter` from `RAIL` jumps to `AGENT`. `Ctrl-Q` returns to `RAIL`. Focused element has a colored border.
   - `?` in `RAIL` focus opens the help modal (close with `q`).
   - `l` toggles archive section visibility.
   - `n`, `r`, `^d`, `a` open their respective modals only when `RAIL`-focused.

6. **Smoke test the rail-only operations** by creating a throwaway orchestrator with `n`, renaming it with `r`, archiving with `a`, then deleting with `^d`. Walking the cascade-delete confirm is the riskiest UX path.

7. **Only after live verification:**
   ```
   openspec archive add-hera-view
   ```
   This merges the deltas into the base specs.

## Merge command

```sh
cd ~/Development/Personal/hera && \
  git fetch origin && \
  git checkout main && \
  git pull --ff-only origin main && \
  git merge --no-ff origin/argus/find-resume-orchestrator-role -m "Merge add-hera-view spec deltas + tasks" && \
  for b in \
    hera-view-stage-a-migration-dao \
    argus/hera-view-stage-b-pty-proxy \
    argus/hera-view-stage-c-view \
    argus/hera-view-stage-d-wsscreen \
    argus/hera-view-stage-e-ws-server \
    argus/hera-view-stage-f-tview-layout \
    argus/hera-view-stage-g-focus-keys \
    argus/hera-view-stage-h-rail-ops \
    argus/hera-view-stage-i-rail-events \
    argus/hera-view-stage-j-daemon \
    argus/hera-view-stage-k-smoke-test; do \
    if git rev-parse --verify "origin/$b" >/dev/null 2>&1; then \
      git merge --no-ff "origin/$b" -m "Merge $b (add-hera-view stage)" || { echo "Conflict on $b — resolve and continue"; break; }; \
    else \
      echo "Skipping $b (no remote branch — worker did not complete)"; \
    fi; \
  done && \
  git push origin main && \
  go build -o bin/hera ./cmd/hera && \
  launchctl kickstart -k gui/$(id -u)/com.anutron.hera
```

This command:
- Merges the spec-deltas branch first (so `main` has the specs even if some stages didn't land).
- Iterates each stage branch and merges if its remote exists; skips with a clear log line if the worker didn't complete.
- Builds and reloads the daemon at the end so the new view is live.

The same text is staged on Aaron's argus clipboard for one-tap copy.

## Open questions to decide before final archive

These were flagged in `design.md` and I did not decide unilaterally. Pick one before archiving:

- **Plugin-view hotkey.** Tentative `Ctrl-H`. Risks colliding with bash backspace synonym in argus's own task list. Alternative: `Ctrl-X H` chord, or a non-letter hotkey. Worker C registers whatever string the design says — if you want a different hotkey, edit the registrar before rebuild.
- **Help modal close key.** Tentative `q` (since Esc is argus-reserved). Alternative: `?` as a toggle.
- **Top-bar contents.** Tentative literal `HERA` only. Alternative: `HERA   /   <focus-path>` right-aligned for context.
- **Confirm-delete UX.** Tentative `y/N` confirm. For cascade-delete-of-coord, type-the-name-to-confirm may be safer.
- **`^d` on orchestrator: actually delete worktrees, or just archive aggressively?** Design.md D5 says delete via `git worktree remove --force`; that's destructive. Stage H is wired to delete. Re-read the audit-log story before live-firing on a real orchestrator.
- **Bottom-bar overflow at narrow widths.** Truncate vs two-line.

## Coord reading-task_list-too-aggressively (false-alarm post-mortem)

I incorrectly concluded that workers E and F had called `task_complete` without committing/pushing. What actually happened: argus's `task_list` showed `Elapsed: 0s` with `Status: complete` while those workers were still running — I interpreted "0s elapsed" as "completed 0 seconds ago" and confirmed by seeing no remote branch and dirty worktrees. I salvaged E's code from the live worktree, pushed it as WIP (commit `2d65867`), and flagged it. Then the worker came back, saw my WIP, fixed the race I'd noticed, and pushed `b814c85` cleanly. F similarly was racing my check and pushed correctly on its own a moment later.

Net result: nothing was actually lost. But the salvaged WIP commit on E's branch is benign (the worker's fix built on top). Worth noting only because the merge will pull in both the WIP and the fix as consecutive commits, which is slightly noisy but correct.

Lesson for coord prompt-writing in future runs: don't trust `task_list` `Elapsed: 0s` as "just completed" — treat any worker without a `hera_send` done report as still in-flight regardless of argus status.

## Integration gaps to close during merge

The stages were sliced with no shared base after the spec deltas, so several seams require small additions at merge time:

1. **Stage A's Archive/Unarchive/Rename DAOs do NOT emit broadcaster events.** Stage I added `db.Events.Emit(...)` calls to Create/End paths only — A's branch didn't see I's code, and vice versa. Add `Emit(Event{Entity: "orchestrator", Op: OpUpdate, ID: id})` (and the role equivalent) at the end of each Archive/Unarchive/Rename method during merge. Without this, the rail won't refresh when archive/rename operations succeed.

2. **Stage F's rail does NOT filter `archived_at`.** Stage F said: "archived_at filtering deferred to Stage A merge (default `List()` returns all rows on this branch)." After A merges, F's `BuildApp` rail-building code needs to switch from `List()` (now active-only) to `ListInclusive()` plus an in-memory partition (for the `l` toggle behavior). This may be cleaner as a small follow-up commit after the merge.

3. **Stage E carries a benign WIP commit before the worker's actual fix.** Branch history is `2d65867` (my premature WIP salvage, has the failing test) → `b814c85` (worker's race fix, makes tests green). The merge will pull both in as consecutive commits. Squash-merge cleanly if cosmetically annoying.

4. **Stage J needs to write the ops-package adapter.** Stage H deliberately used narrow injected interfaces (DB, ArgusClient, WorktreeRemover, Logger) so it doesn't depend on Stage A's concrete DAOs. Stage J's daemon wire-up needs an adapter type that satisfies those interfaces from the real `*db.DB` + `*argus.Client`. This is the explicit hand-off point between H and J.

## Confidence-low spots flagged for inspection

If any of these surfaced as a worker blocker, the report row above will say "blocked" — drill into the worker's hera_send body for details. Anticipated:

- **Stage D — custom `tcell.Screen` backed by WebSocket frames.** D9 was the highest-risk item before spawn. Worker was instructed to halt-and-flag rather than push through. If D blocked, E + J + K are also blocked downstream.
- **Stage H — argus REST `POST /api/tasks` body shape.** The design assumed a shape; the existing `internal/argus/client.go` did not have CreateTask. Worker was told to stub with TODO if the substrate shape doesn't match. If H stubbed, the `n new` and resurrect operations will be no-ops until a substrate-clarity ticket lands.
- **Stage H — `git worktree remove --force` from the daemon.** Should work — daemon is unsandboxed under launchd. The smoke test (Stage K) was supposed to exercise this deliberately.
- **Stage B — pre-loading SSE for every task across every orchestrator at daemon startup.** Design assumed ≤15 tasks. If session has many more, escalate.

## Substrate / follow-up notes (separate from this change)

These are not part of add-hera-view but were noted in the coord's mission constraints when the binding was reclaimed:

- **SSE-event drop bug**: hera missed `task.archived` events for tasks `1779727800185385000` and the w1-w4 worker fleet. Root cause: `internal/db/tasks.go` only emits `task.archived` from the partial-column `SetArchived` path, not from the `Update` path used by HTTP and MCP archive entrypoints. Filed in coord's constraints; tracked as a follow-up.
- **Two orphan hera bindings** (11, 12) from the parked-worker experiment plus the fixit worker, listed in HANDOFF-OVERNIGHT.md. Not in scope of this change.

## How this report was produced

I'm Claude (Opus 4.7) running as the resumed coord. Workers were spawned via subagents to keep my context window lean (one subagent per batch). A persistent Monitor was set up to emit a notification per new unread coord-inbox message; the table above was updated each time a worker reported in. Final commit + push of this report came right before I set `hera_status` to `done` and ended the session.
