# Tasks

## 1. Spec

- [x] 1.1 Delta: Pinned section + `P` toggle (ADDED); navigable Archive below Freelance (ADDED); RAIL-only key set gains `P` (MODIFIED). `openspec validate --all --strict` green.

## 2. DB layer (persistence + mutual exclusivity)

- [x] 2.1 Migration `0005_pinned_at`: nullable `pinned_at` on `orchestrators` and `roles`
- [x] 2.2 `Orchestrator.PinnedAt` / `Role.PinnedAt`; scans read `pinned_at`
- [x] 2.3 DAO `Pin`/`Unpin` (pin sets `pinned_at` + clears `archived_at`); `Archive` also clears `pinned_at`
- [x] 2.4 Tests: Pin sets pinned_at + clears archived_at; Archive clears pinned_at; Unpin clears pinned_at; idempotency

## 3. ops layer

- [x] 3.1 `ops.Orchestrator.Pinned` / `ops.Role.Pinned`; adapter carries them
- [x] 3.2 `ops.DB` interface + adapter: `PinOrchestrator`/`UnpinOrchestrator`/`PinRole`/`UnpinRole`
- [x] 3.3 `Service.PinOrchestrator`/`UnpinOrchestrator`/`PinRole`/`UnpinRole` (pin clears argus-archived via the unarchive path)
- [x] 3.4 Tests: ops Pin clears archived + unarchives bound task; Unpin

## 4. view: keys + mutations

- [x] 4.1 `MutationHandler.OnPin`; `keys.go` `P` → `OnPin` (RAIL-only)
- [x] 4.2 `railSelection.Pinned`; `CurrentRailSelection` carries it; `OnPin` toggles via Pin/Unpin; freelancer → notApplicable
- [x] 4.3 Tests: `P` in RAIL fires OnPin; `P` in pane forwards byte; OnPin pin/unpin dispatch; freelancer feedback

## 5. view: rail rendering (Pinned + Archive sections)

- [x] 5.1 `orchEntry.Pinned` / `roleEntry.Pinned`; `railRowPinnedSep`
- [x] 5.2 buildRows: Pinned section first (pinned orchs + floated pinned roles); pinned excluded from active/archived + `(N)` counts
- [x] 5.3 `SetArchivedFreelance`; bottom Archive expando holds archived orchs + archived freelancers; archived freelancers excluded from inline Freelance groups
- [x] 5.4 `app.go`: populateRail loads orchestrators inclusively, sets Pinned, splits freelancers active/archived
- [x] 5.5 Tests: pinned section render + float-out; archived freelancer in bottom Archive (not vanished) + unarchive via `a`; no double-render with `l`; archived root coord reachable without `l`

## 6. Verify

- [x] 6.1 No regressions: collapse-empty-coords, rail-truthfulness, mixed-coord cue, freelance dedupe, marker, spinner
- [x] 6.2 `go test ./... -race -count=1` green
- [x] 6.3 `openspec validate --all --strict` green
