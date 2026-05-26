# Overnight handoff: drive hera-view to a try-tomorrow state

You're picking up an hera 1.0 dogfood session. Aaron is asleep. He's authorized autonomous progress on the hera-view OpenSpec change with permission to err on action — anything you ship can be reverted in the morning.

## Your identity

You are the **coord role** of the `hera-1.0` orchestrator, bound to argus task `1779746718642643000` at this worktree (`/Users/aaron/.argus/worktrees/Hera/find-resume-orchestrator-role`, branch `argus/find-resume-orchestrator-role`). The binding survived /clear; reclaim it on first action:

```
mcp__argus__hera_join(cwd=$PWD)
```

Bare-call form reclaims the existing binding (per the role-as-identity model). Then:

```
mcp__argus__hera_status(cwd=$PWD, status="working")
mcp__argus__hera_inbox(cwd=$PWD)
```

The inbox may have worker reports waiting for you.

## What's already done in this session

- New OpenSpec change folder `openspec/changes/add-hera-view/` scaffolded at commit `4a61c78`.
- `proposal.md` written: motivation, scope, capabilities (new `hera-view`, modified `hera-coordination`), impact, substrate concerns flagged. Read this first.
- `design.md` written: 10 numbered decisions (D1-D10), goals/non-goals, risks, migration plan, ~8 open questions with tentative defaults, acceptance criteria per behavioral section. Read this second — every implementation decision is here.

Also done earlier today:

- Merged 3 worker branches into main + archived `fix-idle-submit-cr` change folder. main at `9de5623` (then `041bab5` after fix-idle-submit-cr archive). The CR/LF inject fix is live in the deployed daemon at `~/.hera/herad` — auto-submit actually works now.
- Two new orphan hera bindings on the books (bindings 11, 12 from the parked-worker experiment + the fixit worker). Don't worry about them; tracked as task #12 follow-up.

## What's left tonight

Three phases. Each phase has its own checkpoint where you can decide to /clear and hand off again if context grows.

### Phase 1 — finish the OpenSpec change folder (~30 minutes)

1. **Write the spec deltas** under `openspec/changes/add-hera-view/specs/`:

   - `specs/hera-view/spec.md` — **ADDED Requirements** only (new capability). Translate every "it should X" criterion in `design.md`'s "Acceptance criteria" section into a `### Requirement: <name>` block with at least one `#### Scenario:` using exact 4-hashtag and `**WHEN**` / `**THEN**` format. Critical syntax: 4 hashtags for Scenario, not 3.
   - `specs/hera-coordination/spec.md` — **MODIFIED Requirements**. The base spec at `openspec/specs/hera-coordination/spec.md` does not currently define rename, archive, or resurrect semantics. Add or modify requirements covering:
     - Orchestrators may be renamed (uniqueness across non-archived orchestrators).
     - Roles may be renamed within their orchestrator (uniqueness within the orchestrator).
     - Orchestrators and roles may be archived (sets `archived_at` timestamp; role data including mission, constraints, argus_project survives).
     - Resurrect: spawning a fresh argus task in a role's stored `argus_project` while the role exists archived rebinds it on `hera_join` and clears `archived_at`.
   - Reference `openspec/changes/archive/2026-05-25-hera-settings/specs/hera-coordination/spec.md` and `openspec/changes/archive/2026-05-26-fix-idle-submit-cr/specs/hera-coordination/spec.md` for shape examples.

2. **Write `tasks.md`** in OpenSpec checkbox format. Use the staging below as a starting point — adjust if it doesn't match your reading of `design.md`.

3. **Validate strict:**

   ```
   openspec validate add-hera-view --strict
   ```

   Fix any errors. Then `openspec validate --all --strict` as a sanity check.

4. **Commit and push.** All three commits go on `argus/find-resume-orchestrator-role`:

   - `Write hera-view delta spec (ADDED)`
   - `Write hera-coordination delta (rename/archive/resurrect)`
   - `Write tasks.md for add-hera-view`

   Or combine into one. Doesn't matter.

   `git push origin HEAD`.

### Phase 2 — fan out workers per stage (overnight)

The implementation breaks into 11 stages with this dependency graph:

```
A: Migration + DAOs (internal/db/)                  [no deps]
B: PTY proxy package (internal/view/proxy/)         [no deps]
C: Plugin view registration (internal/argus/views.go) [no deps]
D: Custom tcell.Screen for WS (internal/view/screen/) [no deps]
E: WebSocket server route (internal/view/server.go) [deps: C, D]
F: tview app + 3-column layout (internal/view/app.go) [deps: A, B, D]
G: Focus + key routing (internal/view/keys.go)      [deps: F]
H: Rail operations (internal/view/ops/)             [deps: A, F]
I: Dynamic rail updates (internal/view/rail.go)     [deps: A, F]
J: Daemon wire-up (internal/daemon/run.go)          [deps: A, B, C, D, E, F, G, H, I]
K: Smoke test (internal/daemon/view_smoke_test.go)  [deps: J]
```

Spawn workers via `mcp__argus__task_create` with `base_branch="origin/main"` and `depends_on=[ids of prereq tasks]`. Argus's depswatcher will start each task's session as its dependencies complete.

**Worker prompt template** (adjust per stage):

```
You are worker '<stage-slug>' under hera orchestrator 'hera-1.0'.

FIRST CALL:
  mcp__argus__hera_join(cwd=$PWD, kind="worker", orchestrator="hera-1.0", role_name="<stage-slug>", mission="<stage mission>")

THEN: mcp__argus__hera_status(cwd=$PWD, status="working")

CONTEXT: read these first.
- openspec/changes/add-hera-view/proposal.md
- openspec/changes/add-hera-view/design.md (your stage's decisions: D<N>, D<M>)
- openspec/changes/add-hera-view/specs/hera-view/spec.md (your stage's requirements)
- openspec/changes/add-hera-view/tasks.md (your stage's checkboxes — implement those, then tick them)

YOUR STAGE: <stage letter and name>
SCOPE:
  - <bullet>
  - <bullet>

OUT OF SCOPE:
  - <bullet>

PROCESS:
  1. Read referenced design decisions thoroughly.
  2. TDD per skills/test-driven-development: write failing tests first per the relevant requirement scenarios.
  3. Implement to pass tests. Run `go test ./<your-package>/... -race -count=1` until green.
  4. Run `go test ./... -race -count=1` (full suite) to check no regressions.
  5. `git add -A && git commit -m "<imperative>" && git push -u origin HEAD`.
  6. Tick the relevant tasks.md checkboxes in a SECOND commit (so the change-folder update is separable from the code).
  7. `mcp__argus__hera_send(cwd=$PWD, body="<stage-slug> done. Branch: <name>. Commit: <sha>. Tests: <pass|fail with details>.")
  8. `mcp__argus__hera_status(cwd=$PWD, status="done")`.
  9. `mcp__argus__task_complete(cwd=$PWD)` — this signals downstream workers in the depends_on chain.

If blocked: hera_send + hera_status(blocked) + task_message_send back to coord asking for help.

Spec-first: do NOT change behavior outside what your stage's requirements cover. If you need a spec change, hera_send to coord and wait.

Worker discipline (per Aaron's directive):
- Spawn nothing — you ARE the worker.
- Don't archive yourself when done; coord does the lifecycle. Just task_complete.
```

Spawn order:

1. **First batch** (no deps, parallel): A, B, C, D. Get their task IDs from `task_create` responses.
2. **Second batch** (deps on first): E (deps C+D), F (deps A+B+D).
3. **Third batch**: G (deps F), H (deps A+F), I (deps A+F).
4. **Fourth batch**: J (deps A,B,C,D,E,F,G,H,I).
5. **Fifth batch**: K (deps J).

`task_create` returns the new task ID; you pass these as `depends_on=[<id>, <id>]` for downstream tasks. Track the IDs in a scratch note as you go.

### Phase 3 — integrate and stage final merge (when workers report done)

As workers `hera_send` their done reports to your inbox, ack them (`hera_mark_read`) and verify by reading the commit (`git -C ~/Development/Personal/hera show <sha>`).

When all 11 stages report done:

1. Run `openspec validate add-hera-view --strict` one more time on this worktree.
2. Stage on Aaron's clipboard the final merge command (use `mcp__argus__argus_clipboard_set`):

   ```
   cd ~/Development/Personal/hera && \
     git fetch origin && \
     for b in argus/<stage-A-slug> argus/<stage-B-slug> ... argus/<stage-K-slug>; do \
       git merge --no-ff "origin/$b" -m "Merge $b (add-hera-view stage)" || break; \
     done && \
     git push origin main && \
     go build -o bin/hera ./cmd/hera && \
     launchctl kickstart -k gui/$(id -u)/com.anutron.hera
   ```

   (Use the actual stage branch names you used.) Note this does NOT archive the OpenSpec change yet — Aaron should live-test in the morning before archiving.

3. Write a `MORNING-REPORT.md` at the repo root summarizing:

   - What landed (branches + commits per stage)
   - Anything that failed or you parked
   - Open questions where your confidence is low
   - The verification protocol (rebuild + open hera plugin view in argus → see panes light up)
   - The merge command (also on clipboard)

   Commit + push the morning report.

4. Set hera_status(status="done"). Stop.

## Key paths

- This worktree: `/Users/aaron/.argus/worktrees/Hera/find-resume-orchestrator-role` (branch `argus/find-resume-orchestrator-role`)
- Canonical repo: `~/Development/Personal/hera` (on main, sandbox-blocked for writes — stage commands for Aaron)
- Argus source for substrate context: `~/Development/Personal/argus`
- The change folder you're producing: `openspec/changes/add-hera-view/`
- Base specs: `openspec/specs/hera-coordination/spec.md`, `openspec/specs/hera-substrate-link/spec.md`, `openspec/specs/hera-install/spec.md`
- Daemon log: `~/.hera/launchd.log`
- Daemon binary: `~/.hera/herad → ~/Development/Personal/hera/bin/hera`

## Locked decisions (do not re-litigate)

From `design.md`:

- **TUI library:** tview + tcell + lipgloss (matches argus stack).
- **Server location:** in-daemon, route `/view` on the existing `:7744` MCP HTTP listener.
- **PTY proxy:** pre-load snapshot + SSE for every live binding at startup; ring buffers ~256KiB each; instant pane swap on rail navigation.
- **Focus model:** RAIL / COORD / AGENT three states; Cmd/Ctrl-←/→ ladder; Enter from rail jumps to agent; Ctrl-Q returns to rail; colored border on focus.
- **Mutation keys (`n`/`r`/`^d`/`a`/`l`/`?`):** RAIL-focus only; in pane focus they forward as literal characters.
- **Top bar:** literal "HERA" left-aligned (with focused-path right-aligned as a stretch goal).
- **Bottom bar:** context-aware shortcut hints per focus state.
- **Rail content:** every orchestrator's every role, both active and (when `l` toggled) archived; project boundaries traversable with rail navigation; selection cascades to both panes.
- **Dynamic updates:** in-process pub/sub from the DAOs broadcasts binding/role/orchestrator changes; rail debounces refresh ~100ms.
- **Resize:** do NOT resize source task PTYs to match our pane sizes; clip if pane is smaller, blank-pad if larger.

## Open questions to flag in morning report (don't decide unilaterally)

- Default hotkey for registering the plugin view with argus (`Ctrl-H` is my tentative; may collide with bash backspace synonym).
- Help-modal close key (`q` tentative; `Esc` is argus-reserved).
- Confirm-delete UX shape (simple y/N vs type-the-name).
- Top-bar contents — just "HERA" or "HERA   /   hera-1.0 / coord"?
- Should `^d` orchestrator cascade-delete actually delete worktrees, or only archive them aggressively? (`design.md` D5 says delete; might be too destructive without a stronger confirmation.)
- Bottom-bar overflow at narrow widths — truncate vs two lines.

## Confidence-low spots to flag if you hit them

- **Custom `tcell.Screen` backed by WebSocket** (D9 in design.md). Cribbing from `charm/wish`'s SSH-backed Screen is the planned path. If the implementation worker (stage D) reports difficulty, leave it parked and report rather than push through.
- **Argus REST `POST /api/tasks` shape** (D5, used by `n new`). Hera's existing `internal/argus/client.go` doesn't have a `CreateTask` method today. If the worker (stage H) finds the HTTP shape doesn't match what we assumed, file a substrate-clarity note and stub the operation with "TODO: substrate" — don't fake it.
- **`git worktree remove --force` from the daemon** (D5, used by `^d del`). Should work — daemon runs unsandboxed under launchd. But test it deliberately in a smoke test.
- **Pre-loading SSE for every task across every orchestrator at daemon startup** (D3). If you find typical sessions have >50 tasks, escalate — design assumes ≤15.

## Worker prompt seed text (per stage)

If you want to copy/paste, here are short stage prompts to drop into the template above. Read the design.md decisions referenced in each.

**Stage A — Migration + DAOs**

```
Mission: Add archived_at to orchestrators + roles. Add Archive/Unarchive/Rename DAO methods. Update List* to default to non-archived.

Files:
  - internal/db/schema.go (migration 0003_archived_at.sql)
  - internal/db/orchestrators.go (Archive, Unarchive, Rename, ListInclusive)
  - internal/db/roles.go (same)
  - internal/db/orchestrators_test.go, roles_test.go (TDD assertions)

Design refs: D5 (rename/archive semantics), D10 (migration shape).
Spec ref: hera-coordination MODIFIED Requirements.
```

**Stage B — PTY proxy package**

```
Mission: Per-task snapshot + SSE consumer with ring buffer + listener fan-out. No view logic; pure data plumbing.

Files:
  - internal/view/proxy/proxy.go (NewSubscription, Subscribe, Close)
  - internal/view/proxy/ring.go (ring buffer, ~256KiB cap)
  - internal/view/proxy/proxy_test.go

Design refs: D3 (pre-load + ring + fan-out).
Wire to argus client (already exists at internal/argus/client.go) for GET /api/tasks/{id}/output and the existing SSE event-stream code.
```

**Stage C — Plugin view registration**

```
Mission: argus client methods to register/heartbeat/delete a plugin view, mirroring the MCP-tool registrar pattern.

Files:
  - internal/argus/views.go (new — RegisterView, DeleteView types + methods)
  - internal/argus/views_test.go

Design refs: D8.
Pattern reference: internal/mcp/registrar.go (the 5min heartbeat shape).
```

**Stage D — Custom tcell.Screen for WS**

```
Mission: A tcell.Screen implementation whose Show/Sync emit ANSI bytes over a WebSocket connection; whose event queue receives EventKey from inbound binary frames.

Files:
  - internal/view/screen/wsscreen.go
  - internal/view/screen/wsscreen_test.go

Design refs: D9 — high-risk piece.
Crib from: github.com/charmbracelet/wish (SSH-backed Screen has the same shape) — if you find a usable open-source pattern, vendor or copy. If you can't, stop and flag in the report rather than push through.
```

**Stage E — WebSocket server route**

```
Mission: Add GET /view to the existing HTTP listener on :7744. Accept WebSocket upgrade. Per connection: spawn a tview Application bound to a Stage-D Screen. On close: shut down.

Files:
  - internal/view/server.go
  - internal/view/server_test.go

Design refs: D2, D9.
```

**Stage F — tview app + 3-column layout**

```
Mission: The visible surface. tview Flex composing top bar + 3-column body + bottom bar. Body has rail (List or TreeView) + coord StreamPane + agent StreamPane.

Files:
  - internal/view/app.go (BuildApp returning *tview.Application)
  - internal/view/layout.go (Flex composition)
  - internal/view/streampane.go (port or fork of argus's streampane — see ~/Development/Personal/argus/internal/tui/streampane/)

Design refs: D1, D7 (resize).
Use the proxy subscriptions from stage B as the data source for streampanes. Don't wire focus yet — that's stage G.
```

**Stage G — Focus + key routing**

```
Mission: Three-state focus machine. Cmd/Ctrl-←/→ ladder. Enter rail→agent. Ctrl-Q to rail. Pane focus forwards keys to bound PTY via argus POST /api/tasks/{id}/input.

Files:
  - internal/view/keys.go (key handler attached to tview Application)
  - internal/view/focus.go (state machine)
  - internal/view/keys_test.go

Design refs: D4.
Don't wire mutation keys yet — that's stage H.
```

**Stage H — Rail operations**

```
Mission: Implement n / r / ^d / a / l / ? as RAIL-focus-only key handlers. Modals via tview Modal primitive.

Files:
  - internal/view/ops/new.go (n: prompt + create orchestrator + spawn coord task via argus REST)
  - internal/view/ops/rename.go (r: prompt + DAO update)
  - internal/view/ops/delete.go (^d: confirm + git worktree remove + DAO archive)
  - internal/view/ops/archive.go (a: toggle + argus task_archive)
  - internal/view/ops/listall.go (l: pure view-state toggle)
  - internal/view/ops/help.go (?: render help modal)
  - tests per file

Design refs: D5 — read carefully, behavior is detailed.
For `n new`: extend internal/argus/client.go with CreateTask (HTTP shape — see open question above).
For `^d del`: use os/exec to run git worktree remove --force; daemon has the privileges. Add audit log.
```

**Stage I — Dynamic rail updates**

```
Mission: In-process pub/sub. DAOs broadcast on insert/update/delete of orchestrators/roles/bindings. Rail subscribes and refreshes (debounced ~100ms).

Files:
  - internal/db/events.go (Broadcaster type, Subscribe channel)
  - internal/view/rail.go (rail subscriber + render)
  - integration with stage A's DAO methods (they call Broadcaster.Emit)

Design refs: D6.
```

**Stage J — Daemon wire-up**

```
Mission: internal/daemon/run.go starts the plugin-view registrar (C) + the WebSocket server route (E) alongside the existing MCP registrar + HTTP listener. Wire the proxy subscriptions to the bindings table on startup.

Files:
  - internal/daemon/run.go (additions)
  - internal/daemon/run_test.go (assert view registers + unregisters)

Design refs: D2, D8.
```

**Stage K — Smoke test**

```
Mission: End-to-end smoke test that spawns the daemon, opens a fake WebSocket plugin-view client, sends resize+focus envelopes, asserts ANSI byte output is well-formed, sends a fake keystroke, asserts it routes to the right task input endpoint.

Files:
  - internal/daemon/view_smoke_test.go

Plus: a 1-line entry in tasks.md confirming the smoke test exists.
```

## When in doubt

- If a worker is blocked > 30 minutes, send them a clarifying hera_send.
- If multiple workers report similar blockers, halt that stage and write the issue into MORNING-REPORT.md.
- If you hit a substrate question (argus's HTTP shape doesn't match expectation), DO NOT improvise — stub with TODO, flag in MORNING-REPORT, move on.
- If you can't do something through the worker pattern (e.g., the canonical worktree is needed), stage a command on Aaron's clipboard and note it in MORNING-REPORT.
- If you hit your own context wall: write your own checkpoint to a NEW handoff file, stop, and let Aaron resume in the morning.

Go.
