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
| A — Migration + DAOs | `1779783499697319000` | `argus/hera-view-stage-a-migration` | spawned | – | |
| B — PTY proxy | `1779783511038358000` | `argus/hera-view-stage-b-pty-proxy` | spawned | – | |
| C — Plugin view registrar | `1779783521722828000` | `argus/hera-view-stage-c-view` | spawned | – | |
| D — wsscreen | `1779783534043050000` | `argus/hera-view-stage-d-wsscreen` | spawned | – | HIGH RISK — D9 wsscreen, worker was told to halt-and-flag rather than push through if stuck |
| E — WS server route | `1779783591683239000` | `argus/hera-view-stage-e-ws-server` | pending (deps C+D) | – | |
| F — tview layout | `1779783604871053000` | `argus/hera-view-stage-f-tview-layout` | pending (deps A+B+D) | – | |
| G — Focus + keys | `1779783666646818000` | `argus/hera-view-stage-g-focus-keys` | pending (deps F) | – | |
| H — Rail operations | `1779783682291226000` | `argus/hera-view-stage-h-rail-ops` | pending (deps A+F) | – | substrate question on `POST /api/tasks` body shape — worker was told to STUB and flag if it doesn't match D5 assumptions |
| I — Rail events | `1779783693718489000` | `argus/hera-view-stage-i-rail-events` | pending (deps A+F) | – | |
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
    argus/hera-view-stage-a-migration \
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
