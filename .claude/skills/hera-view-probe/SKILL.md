---
name: hera-view-probe
description: "See and test the live hera-view TUI (the rail + coord/agent panes served over WebSocket) without screenshots. Use proactively after changing internal/view/* (rail rows, icons, freelance section, pane binding, focus/layout) to validate the rendered surface and keyboard navigation against a running daemon. Also covers spawning test-fixture agents and redeploying the daemon via iris."
metadata:
  author: aaron
  version: "1.0"
---

# Seeing and testing hera-view

`hera-view` is a tview TUI the hera daemon serves over a WebSocket at
`ws://127.0.0.1:7744/view`: a left **rail** (orchestrators + roles + a
Freelance section) plus a **coord** pane and an **agent** pane. You cannot see
a TUI directly, but you can render its live surface to text and drive its
keyboard — so you can validate rail rendering and navigation the same way a
human would, and iterate without asking for screenshots.

The harness is `internal/daemon/live_probe_test.go` — gated Go tests that dial
the live daemon. They are skipped unless `HERA_LIVE_PROBE=1`, so they never run
in CI.

## When to use this (proactively)

After you change anything that affects the rendered surface — rail rows, icons,
counts, the Freelance section, archived/dead hiding, pane binding on selection,
focus traversal, the bottom bar, dual-mode layout — do NOT claim it works from
unit tests alone. Rebuild the running daemon (see "Redeploy" below) and probe
the live surface to confirm it renders and navigates correctly.

## 1. See the rail (read-only)

```bash
HERA_LIVE_PROBE=1 go test ./internal/daemon/ -run LiveViewProbe -count=1 -v -timeout 30s
```

The test dials `/view`, sizes the surface, captures the byte stream, and logs
it reconstructed by `renderANSIToGrid` — a cursor-addressed 2D grid that places
each positioned write in its real cell. Use the grid renderer (not
`renderANSIToText`) whenever panes are streaming live content: the panes emit
many cursor-positioned diffs that fragment a naive linear dump, but land
correctly in the grid.

Read the **left ~36 columns** for the rail. Typical shape:

```
╔═══════════════Rail═══════════════╗
║▾ my-orchestrator (2)             ║
║ 󰖔 worker-a                    3m ║
║ 󰖕 worker-b                    1m ║
║──────────── Freelance ───────────
║▾ SomeRepo (1)                    ║
║ 󰖔 a-vanilla-argus-task       15m ║
╚══════════════════════════════════╝
[RAIL] j/k move  Enter agent  Ctrl-→ coord  ...
```

**Reading artifacts:** the grid overlays successive repaint frames without
clearing every cell, so you may see doubled glyphs (`coordoord`, `(2) (2)`).
That is a probe artifact — the real TUI diffs cleanly. Read through the
doubling; the leftmost run is the true text.

Robust extraction tips (the surface columns shift between runs, so prefer
content matching over fixed column cuts):

```bash
# rail rows by content
... 2>&1 | grep -A56 'LIVE HERA SURFACE' | sed 's/B//g' | grep '║' | cut -c1-40
# is the Coord pane present (3-column) or absent (freelance full-width)?
... 2>&1 | grep -A56 'LIVE HERA SURFACE' | sed 's/B//g' | grep -m1 'Rail═'
# which bottom-bar hints are shown
... | grep 'Ctrl-→'
```

(The `sed 's/B//g'` strips a `B` marker the renderer emits at attribute
boundaries.)

## 2. Drive navigation (keystrokes)

Set `HERA_PROBE_KEYS` to a string of keys; the probe sends each as a raw byte
on a **binary** WebSocket frame (text frames are reserved for resize/focus),
with a gap between them so the selection debounce + pane rebind settle, then
captures the settled surface.

```bash
# press j ten times, then capture
HERA_LIVE_PROBE=1 HERA_PROBE_KEYS=jjjjjjjjjj go test ./internal/daemon/ -run LiveViewProbe -count=1 -v -timeout 50s
```

- `j` / `k` move the rail cursor (the router maps them to Down/Up).
- Exact row indices shift as orchestrators/workers come and go — do NOT assume
  a fixed count. Robust pattern: send MANY `j` to **clamp at the bottom row**,
  then `k` back up a known number of steps to land on a specific row.
- Confirm where the cursor landed by the **agent pane's `PWD:`/`GIT:`** line
  (it shows the bound task's worktree), not by counting keystrokes.

What to verify:
- Selecting a **worker** row → 3-column layout (`╭Coord` present), bottom bar
  `Ctrl-→ coord`, coord pane = orchestrator's coord task, agent pane = the worker.
- Selecting a **freelancer** row → full-width agent (`╭Agent` directly after the
  rail, NO `╭Coord`), bottom bar `Ctrl-→ agent`.
- Navigating back from a freelancer to a worker restores the 3-column layout.

## 3. Check what argus serves

```bash
HERA_LIVE_PROBE=1 go test ./internal/daemon/ -run LiveArgusAPIProbe -count=1 -v
```

Reports which task-state fields the running argus daemon serves
(`status`/`idle`/`needs_input`/`archived`). `needs_input` is absent unless the
argus daemon was built from the `plugin-substrate` branch — if it is absent,
the rail's red `?` needs-input icon stays dormant (expected, not a hera bug).

## 4. Spawn test-fixture agents

The rail is empty unless real argus tasks exist. To exercise scenarios, create
passive fixtures via the argus/hera MCP tools (`mcp__argus__task_create`,
`hera_new_orchestrator`, `hera_join`). Give each a prompt that does its hera
setup ONCE and then idles (so it burns minimal tokens):

- **Managed coordinator:** `task_create` an agent whose prompt calls
  `hera_new_orchestrator(cwd=$PWD, name=..., coordinator_role_name="coord")`
  then `hera_status idle`, then waits.
- **Worker:** `task_create` an agent whose prompt calls
  `hera_join(cwd=$PWD, orchestrator="<name>", role_name=..., kind="worker")`.
  The orchestrator must already exist — poll
  `sqlite3 ~/.hera/state.sqlite "SELECT name FROM orchestrators"` until it
  appears before spawning workers (don't use `depends_on`: an idle coordinator
  never reaches `complete`).
- **Nested coord-with-sub-coord:** a worker agent that ALSO calls
  `hera_new_orchestrator` for a child — one task, two bindings (multi-binding).
- **Freelancer:** just `task_create` with NO hera call — any live argus task
  whose id is absent from the `bindings` table renders in the Freelance section.
- **Archived row:** `task_archive(id=..., archived=true)` — it leaves the active
  rail and only shows under Archive (the `l` key).

Inspect the resulting hera graph directly:

```bash
sqlite3 ~/.hera/state.sqlite "SELECT o.name, r.name, r.kind, b.argus_task_id \
  FROM orchestrators o JOIN roles r ON r.orchestrator_id=o.id \
  LEFT JOIN bindings b ON b.role_id=r.id AND b.ended_at IS NULL ORDER BY o.name;"
```

Clean up fixtures when done: `task_archive` (or `task_stop`) each by id.

## 5. Redeploy the running daemon after code changes

The probe hits the **running** daemon, so rebuild it after editing
`internal/view/*`. From an argus sandbox use iris (host-side git/build):

1. Commit, then keep the branch a clean single commit on `origin/main`:
   `iris_fetch(task_id)` → `git reset --hard origin/main` → `git cherry-pick <fix>`.
2. `iris_push(task_id, force_with_lease=true)`.
3. `iris_gh_pr_create` → `iris_gh_pr_merge(strategy=squash)`.
4. `iris_reload(task_id)` — pulls main, `go build`, restarts the launchagent.

The argus daemon itself (`com.drn.argus.daemon`) is NOT iris-managed; only the
hera daemon is reachable this way.

## Gotchas

- The probe needs `~/.hera/api-token` for `LiveArgusAPIProbe` (read from
  `$HOME/.hera/api-token`).
- A wedged prior `/view` session is superseded automatically on connect; if the
  surface is blank, the daemon may be mid-restart — retry after a few seconds.
- Give the daemon's argus-state cache one poll interval (~2s) after spawning or
  archiving tasks before probing, so the rail reflects the change.
