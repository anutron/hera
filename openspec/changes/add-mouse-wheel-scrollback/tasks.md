# Tasks: add-mouse-wheel-scrollback

**Design doc:** openspec/changes/add-mouse-wheel-scrollback/design.md

## 1. argus-sdk scroll engine (worker task in ARGUS-SDK project)

- [x] 1.1 Spawn the ARGUS-SDK worker with the design's D3 spec (worker MUST `iris_fetch` + `git reset --hard origin/main` first — local clone is stale)
- [x] 1.2 Worker: scroll state on `TerminalPane` — `ScrollBy`/`ScrollOffset`/`ResetScroll`, clamped to `[0, ScrollbackLen]`, with tests (commit 7025b33)
- [x] 1.3 Worker: paint-at-offset rendering (scrollback window above live screen) + `[SCROLL]` top-row badge, with paint tests (commit d52a70c)
- [x] 1.4 Worker: anchor-lock — effective offset grows with newly retained scrollback lines; re-clamps on buffer trim, with tests (commit 9dab935)
- [x] 1.5 Worker: SGR mouse decoder helper (frame → wheel-up/down + 1-based x,y, or not-mouse), with table tests (commit 4f2a3e6)
- [x] 1.6 Merge to main, tag `v0.0.3`, confirm `go list -m github.com/anutron/argus-sdk@v0.0.3` resolves (main @ 89f635c, tag verified, SDK tests green)

## 2. argus wheel forwarding (worker task in ARGUS project — gated on user confirmation)

- [ ] 2.1 Confirm with the user that the argus change may proceed (wheel stays dead end-to-end without it; keyboard path unaffected)
- [ ] 2.2 Spawn the ARGUS worker: `internal/tui/terminalpane` MouseHandler encoding `MouseScrollUp/Down` as `ESC[<64|65;x;yM` (pane-inner-relative, 1-based) onto the existing `inputBack` channel, with tests
- [ ] 2.3 Worker: PR → merge → rebuild/redeploy the running argus

## 3. hera failing tests

**Depends on:** Stage 1

- [x] 3.1 Bump `go.mod` to argus-sdk `v0.0.3`
- [x] 3.2 Write failing tests for every delta scenario: mouse-frame interception (pane focus → no PTY forward; RAIL focus → no parser dispatch; non-wheel swallowed), wheel hit-test routing (pane under cursor, position-beats-focus, dead zones), scroll-mode rendering through `pinnedTerminalPane` (visible history, badge, anchor-lock, clamps, ⇧↑/⇧↓ parity, reset-on-rebind) — `internal/view/wheel_test.go`
- [x] 3.3 Confirm each test fails (Prove-It): build-level failures on the missing `WheelRouter` param, `applyWheel`, `wheelStep`, `PanBy` before implementation

## 4. hera mouse routing + pane delegation

**Depends on:** Stage 3

- [x] 4.1 `raw_input.go`: detect whole-frame SGR mouse sequences before the focus fork (decoder from the SDK); never forward, never parse; wheel → scroll dispatch, others swallowed
- [x] 4.2 `app.go`: event-loop scroll dispatch — `RouteWheel` queues `applyWheel` via `QueueUpdateDraw`, hit-testing `(x−1, y−1)` against rail/coord/agent `InRect`; pane hit → `ScrollBy(±3)`; absent-pane stale rects skipped
- [x] 4.3 `pinned_pane.go`: delete the tracked-only `scrollOffset`; `ScrollBy`/`ScrollOffset`/`ResetScroll` now promote from the SDK engine; rebind constructs a fresh pane so a newly bound task starts live (no explicit ResetScroll needed)
- [x] 4.4 `ScrollFocusedPane`: ⇧↑/⇧↓ drive the SDK engine (1 line/press); D15 limitation comments removed

## 5. rail viewport panning

**Depends on:** Stage 3

- [x] 5.1 `rail_list.go`: track `lastSnapCursor`; snap viewport to cursor only when the cursor moved since the last draw (wheel pans persist across refresh repaints)
- [x] 5.2 `rail_list.go`: `PanBy` clamped to `[0, max(0, rows−innerHeight)]` via `lastHeight`; wired from `applyWheel` (rail hit, ±3/tick)

## 6. validation + ship

**Depends on:** Stage 4, Stage 5

- [ ] 6.1 Full test suite + `openspec validate --all --strict`
- [ ] 6.2 hera-view-probe e2e: inject literal SGR wheel frames over the WS; assert pane history renders, badge shows, rail pans, RAIL keys unaffected (keyboard-only e2e if stage 2 not yet confirmed)
- [ ] 6.3 Commit, push via iris, open PR
