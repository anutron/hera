# hera-view interaction test plan (probe-driven)

Goal: drive the **live** hera TUI with the probe and confirm it matches `docs/prototypes/rail-nav.html` — both **visually** (rendered surface) and **functionally** (keystrokes actually do things). Walk the list top-to-bottom, record a finding per item, and fire `/fixit` for each failure as you go. The prototype is the source of truth.

## How to run a probe

```
HERA_LIVE_PROBE=1 [HERA_PROBE_KEYS=…] [HERA_PROBE_RAW='…'] \
  go test ./internal/daemon/ -run LiveViewProbe -count=1 -v -timeout 60s 2>&1 | sed 's/B//g'
```

Output has two blocks: `=== LIVE HERA SURFACE ===` (grid-reconstructed screen — read the left ~36 cols for the rail, the rest for panes) and `=== HERA CONTROL FRAMES ===` (the `hotkeys`/`release`/`help` text frames hera sends — how we verify chrome, since the probe bypasses argus).

- **`HERA_PROBE_KEYS`** — each byte is one key, sent in order: `j` `k` (move), `a` `s` `S` `n` `r` (runes), ` ` (space = fold), `?` (help).
- **`HERA_PROBE_RAW`** — a `;;`-separated sequence of Go-quoted byte strings, each sent as one frame. Encodings:
  - Enter `\r` · Esc `\x1b` · space ` `
  - focus `^→` `\x1b[1;5C` · `^←` `\x1b[1;5D` · Cmd-emulated (Ctrl+Alt) `\x1b[1;7C` / `\x1b[1;7D`
  - in-pane nav `^↑` `\x1b[1;5A` · `^↓` `\x1b[1;5B` · scroll `⇧↑` `\x1b[1;2A` · `⇧↓` `\x1b[1;2B`
  - `^Q` `\x11` · `^d` `\x04` · `^r` `\x12` · `^p` `\x10` · printable e.g. `Z`
  - Example (functional): `HERA_PROBE_RAW='\r;;Z'` = Enter into the pane, then type `Z`.

## Setup & loop

- **Dogfood** the running hera daemon onto this branch so every probe hits the latest build: `mcp__argus__iris_set_dogfood`. Re-probe after each fix.
- **Fixtures** are live (qa-* coords/agents incl. nested + archived, `freelance-fixture-alpha`) so the rail shows varied states. Row indices shift — to land on a known row, prefer: rely on the **initial selection** (first live agent), or clamp at the bottom (many `j`) then `k` up a known count, and confirm position by the agent pane's `PWD:`/`GIT:` line.
- **Per item:** run the probe → compare to the prototype → mark `PASS`/`FAIL` + a one-line finding. On `FAIL`, fire `/fixit "<concise repro + expected>"` (it fixes + merges in a worktree). Don't stop on the first failure — collect all, fire fixits (they run in parallel), re-dogfood, re-probe.

## Caveats (expected, not bugs)

- The direct probe **bypasses argus**, so the **bottom bar is NOT in the surface** (argus draws it from hera's pushed `hotkeys`). Verify the bottom bar via the **CONTROL FRAMES** block, and the *visual* bottom bar only in real argus (item M).
- **Scroll `⇧↑/↓` is known not-visibly-rendered** (argus-sdk `terminalpane@v0.0.2` has no scrollback paint hook). Expect keys intercepted (rail doesn't move, not forwarded to PTY) but no visible scroll — record as known-gap, don't fixit unless the keys leak.

---

## A. Rail structure (visual)

- [ ] **A1** No-keys probe. Coordinators render as foldable rows: chevron `▾`/`▸`, `◆` root icon, live `(N)` count. Agents nest indented beneath. _No `coord`/`freelance` pills._
- [ ] **A2** Folders-first: under a coordinator, sub-coordinator rows sort above leaf-worker rows.
- [ ] **A3** Nested multi-binding: a worker that is also a coordinator (`qa-nested-sub`) renders as a foldable coord row with its own child (`qa-nested-leaf`) nested under it.
- [ ] **A4** Status icons: `?` needs-input, `✓` review/complete, `☾` working, `○` idle — match the agent's argus state.
- [ ] **A5** Per-coordinator `Archive (N)` expando renders (dashed, collapsed) below a coordinator's active agents when it has archived children (`qa-workers` → worker-archived).
- [ ] **A6** Top-level `Archive` section renders at the very bottom (below Freelance) and holds archived root coordinators.
- [ ] **A7** Freelance section (grouped by repo) renders above the top-level Archive; `freelance-fixture-alpha` under its repo group.
- [ ] **A8** Selected row is highlighted; when focus is in a pane the rail is visibly de-emphasized (or selection still legible) — compare to prototype's dim.

## B. Navigation & folding

- [ ] **B1** `HERA_PROBE_KEYS=jjjj` moves the selection down 4 selectable rows; `k` moves up. Clamps at ends.
- [ ] **B2** `HERA_PROBE_KEYS=' '` (space) on a coordinator folds it (`▾`→`▸`, children hidden); again unfolds.
- [ ] **B3** Space on an `Archive (N)` row expands it to show archived children; again collapses.
- [ ] **B4** Space on the Freelance section toggles it.

## C. Body modes (visual)

- [ ] **C1** A **coordinator** selection → rail + single **full-width HERA** pane, no AGENT pane; center pane titled `HERA`. (Navigate to a root/sub coord row.)
- [ ] **C2** An **agent** selection → rail + **HERA + AGENT** split; left pane `HERA` (the agent's coordinator), right pane the agent.
- [ ] **C3** A **freelancer** selection → rail + single **full-width AGENT** pane, no HERA pane.
- [ ] **C4** Switching coord→agent→freelancer re-composes the body each time (pane count changes; absent pane gone).

## D. Enter-into-pane & focus ladder

- [ ] **D1** Enter on a **coordinator** (`HERA_PROBE_RAW='\r'` from a coord selection) → focus the HERA pane (focused border / `● keyboard`). CONTROL FRAMES show a `hotkeys` push for the new focus.
- [ ] **D2** Enter on an **agent** → focus the AGENT pane.
- [ ] **D3** Enter on a **freelancer** → focus the AGENT pane.
- [ ] **D4** Enter on a header/`Archive`/`Freelance` row folds it (does NOT enter a pane).
- [ ] **D5** `^→`/`^←` traverse only present panes: coord = rail↔HERA (a 2nd `^→` does NOT reach a non-existent AGENT); agent = rail↔HERA↔AGENT; freelancer = rail↔AGENT. (`HERA_PROBE_RAW='\x1b[1;5C;;\x1b[1;5C'`.)
- [ ] **D6** `^Q` (`\x11`) from a pane returns focus to RAIL.
- [ ] **D7** Cmd-emulated arrow (`\x1b[1;7C`) also traverses (iTerm2 Cmd→Ctrl+Alt remap path).

## E. FUNCTIONAL — keystrokes reach the PTY  ⚠️ (the reported bug)

- [ ] **E1** **Keys reach the AGENT pane.** From an agent selection: `HERA_PROBE_RAW='\r;;Z'` (Enter into AGENT, type `Z`). The `Z` MUST appear in the agent pane's input line (the agent echoes it). _Reported failing: "Cmd-→ focuses the agent but no keystrokes reach it."_ If `Z` is absent → keystrokes not forwarded to `/api/tasks/{id}/input`.
- [ ] **E2** **Keys reach the HERA pane.** From a coordinator selection: `HERA_PROBE_RAW='\r;;Z'` → `Z` appears in the HERA (coord) pane.
- [ ] **E3** **Traverse then type goes to the new pane.** Agent selection: `HERA_PROBE_RAW='\x1b[1;5C;;Q'` (^→ to HERA, type `Q`) → `Q` lands in the HERA pane, not the agent.
- [ ] **E4** **Esc inside a pane → PTY, not leave.** `HERA_PROBE_RAW='\r;;\x1b'` (enter pane, Esc) → the view stays (no `release` frame); Esc reaches the PTY. (CONTROL FRAMES must NOT contain `release`.)

## F. Chrome via control frames (functional) + top bar (visual)

- [ ] **F1** On connect, CONTROL FRAMES include a `hotkeys` envelope; items carry `key`/`label`/`bar` and reflect RAIL bindings (`n` new, `r` rename, `^d` del, `?` help, `a` archive, `s/S`, `^r`, `^p`).
- [ ] **F2** On focus change (Enter into a pane), a NEW `hotkeys` envelope is pushed reflecting the pane context (`keys → HERA/agent PTY`, `^← rail`, `^Q rail`).
- [ ] **F3** `?` from RAIL (`HERA_PROBE_KEYS='?'`) emits a `help` control frame (NOT an in-surface modal).
- [ ] **F4** Esc from RAIL (`HERA_PROBE_RAW='\x1b'`) emits a `release` control frame.
- [ ] **F5** Top bar (surface): `HERA` top-left. (Bottom bar is argus-rendered — verify in item M, not here.)

## G. Keyset actions (functional + visual)

- [ ] **G1** `a` (`HERA_PROBE_KEYS='a'`) on an active agent → it moves into its coordinator's `Archive` expando (re-probe shows it gone from active, present in archive). `a` again unarchives.
- [ ] **G2** `^d` (`\x04`) on an agent → a destructive confirm overlay naming the target appears (does NOT delete yet). (Confirm-then-verify is destructive — do on a throwaway fixture only.)
- [ ] **G3** `^r` (`\x12`) → a prune confirm listing completed agents appears; lists only completed.
- [ ] **G4** `s` / `S` on an agent → its status (and rail icon) advances/reverts (`☾`→`✓`).
- [ ] **G5** `^p` (`\x10`) on an agent → PR flow triggers (gh pr create shell-out; verify it attempts, e.g. via a confirm/log — don't actually merge).
- [ ] **G6** `⇧↑/↓` (`\x1b[1;2A`/`B`) in a pane — keys intercepted (rail doesn't move, not echoed to PTY). _Visible scroll is a known SDK gap (caveat) — record, don't fixit unless keys leak._
- [ ] **G7** `^↑/↓` / `⌘↑↓` (`\x1b[1;5A`/`B`) in a pane → selection moves to next/prev agent AND focus stays in a pane bound to the new selection (rail does not regain focus).

## M. Manual-in-argus (Aaron / human pass — not probe-able)

- [ ] **M1** Bottom bar in real argus shows hera's focus-aware hotkeys + reserved `^Q^Q argus`.
- [ ] **M2** `?` pops argus's help overlay from hera's dictionary.
- [ ] **M3** Esc from RAIL returns to argus; `^Q^Q` failsafe works.
- [ ] **M4** Overall feel matches the browser across coord/agent/freelancer + the full keyset.

---

### Findings log

(record `PASS`/`FAIL` + note + fixit ref per item as you go)

#### 2026-05-31 — operator-directed rail polish pass (commit 34932f4, dogfooded on `dev`)

This pass was operator-steered (not the full top-to-bottom walk): the rail was retargeted to mirror argus's task panel rather than the prototype's `◆`. Fixes were authored + tested + deployed inline (not via `/fixit`) at Aaron's direction.

- **A1 — coordinator row glyphs: FAIL → FIXED.** Live rail showed coordinator headers as `▾ <name> (N)` with **no status icon and no `◆`**. Per Aaron, the rail mirrors argus's task panel: headers now render `<status-icon> <chevron> 󰹻 <name> (N)` (argus order, icon-first). `󰹻` (U+F0E7B) is a fixed coordinator marker; the status icon (moon/✓/?) is driven by the coord task's argus state; the prototype's `◆` is superseded. Probe-confirmed: 󰹻 on all 15 coord headers; idle coords show a blank moon-outline (expected). Status-icon paths (`?`/`✓`) covered by `TestRailList_CoordHeaderStatusIconAndMarker`.
- **A8 (focus) — rail yellow / panes white: FAIL → FIXED (needs visual confirm).** Focus border was tview yellow on the rail and the SDK terminalpane's hardcoded white on the panes. Now both use argus's `theme.ColorTitle` (cyan) on focus and `theme.ColorBorder` when unfocused; `pinnedTerminalPane.Draw` repaints the pane border cyan on `HasFocus()`. Not probe-verifiable (grid strips color) — **pending Aaron's eyeball in real argus** (folds into M4). Covered by `TestApp_OnFocusChanged_PaintsArgusCyanBorders` + `TestPinnedTerminalPane_RepaintsCyanBorderOnFocus`.
- **Spec:** add-hera-view delta updated (coordinator-header icon+marker; argus focus color) and `openspec validate --strict` passes.

#### 2026-05-31 — operator-reported bug batch (serial `/fixit`s, each spec + tests, dogfooded on `dev`)

Operator hit these in real argus; fixed via serial fixit subagents (full nesting chosen over interim). Final stack dogfooded at `2768ff1`.

- **E1 — keystrokes reach the PTY: PASS (not a bug).** Operator confirmed typing into a LIVE agent works; the probe's apparent failure was the qa fixtures being *completed* sessions (argus 404 "no active session" — dead PTY). Real fix shipped anyway: `handlePane` no longer swallows `PostTaskInput` errors — it logs them (commit 73446f5). Spec scenario + tests. The 404 chase used the new log line (`forward keystroke … HTTP 404`) to prove the path is correct.
- **Input lag — FAIL → FIXED.** `handlePane` forwarded synchronously (a blocking HTTP round-trip per keystroke on the tview event loop). Now an ordered async `PaneForwarder` (buffered channel + drain goroutine, coalesces consecutive same-target bytes into one POST, drop-oldest on overflow) (commit 73050c8). Spec + race-clean tests. _Operator to confirm the feel._
- **A2/A3 — sub-coordinator nesting: FAIL → FIXED.** hera's flat model never linked multi-bindings, so `qa-nested-parent`'s `sub-coord` (= `qa-nested-child`'s coord) rendered as two disconnected top-level rows. Now a recursive depth-aware rail: `resolveSubCoordinators` links worker.ArgusTaskID == orch.CoordTaskID, renders the sub-coord as a foldable coord row with its children nested, de-dups the child orch (commit 4ac0317). Probe-confirmed: `qa-nested-parent → ✓▾󰹻 sub-coord (1) → ✓ leaf-worker`. Spec drift-reconciled + scenarios + tests.
- **C1 — coordinator selection full-width: FAIL → FIXED.** Rode along on the nesting change: a sub-coord selection composes full-width HERA bound to its **own** task (not the parent's coord).
- **A5/Archive indent — FAIL → FIXED.** Depth-aware indentation (from the nesting work) now sits archived children one level deeper than their `Archive (N)` expando header.
- **A8/selection grey — FAIL → FIXED.** The `ColorHighlight` full-row fill (`fillBackground`) left stale grey blocks across rows under argus's cell-diff compositing. Removed entirely; selection now shown via pink `theme.StyleSelected` text consistently across all row types (commit 2768ff1). Spec scenario + tests.

**Open risks (flagged, accepted):** startup auto-selection of a sub-coord still picks its worker side, not coord; multi-binding resolution is first-wins per coord task.

**Still unwalked:** B (nav/folding), C2–C4 (body modes), D (Enter/focus ladder), E2–E4, F (control-frame chrome), G (keyset actions); M1–M4 (manual). Resume top-to-bottom when ready.
