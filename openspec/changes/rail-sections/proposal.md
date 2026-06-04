# Rail Pinned section + `P` pin; navigable Hera Archive below Freelance

## Why

Two rail gaps surfaced against argus parity and a live data-loss bug:

- **No way to pin.** argus has a Pinned section at the TOP of its task list and a `P` key that floats a task there (pin and archive mutually exclusive — `SetPinned` clears archived and vice versa). Hera has no equivalent, so an operator can't keep the project/agent they're actively driving pinned to the top while the rest churns.
- **Archived freelancers VANISH with no way back.** When the operator presses `a` on a freelancer (an unmanaged argus task) it gets argus-archived and then disappears from the rail entirely — the Freelance section hides archived rows unless `l` is pressed, and the existing top-level Archive only holds archived ROOT COORDINATORS (and only populates when `l` is on). There is no default-reachable home for an archived freelancer, so an accidental `a` strands it until the operator remembers `l` exists.

## What Changes

- **Pinned section (Story 1).** A `Pinned` section renders at the TOP of the rail, above the orchestrator list, mirroring argus's look + semantics. `P` (focus `RAIL`) toggles the pinned state of the selected coordinator (orchestrator header) or agent (worker/sub-coordinator role). A pinned coordinator floats into the Pinned section with its subtree; a pinned agent floats out of its coordinator into the Pinned section as a standalone row; unpin returns it to its normal place. Pin and archive are MUTUALLY EXCLUSIVE: pinning clears the archived state (hera-side and, when a bound task is argus-archived, argus-side), and archiving clears the pinned state.
- **Pin persistence + the argus gap.** argus exposes NO pin/SetPinned REST endpoint (pin is an argus TUI/model-only concept), so hera persists pin state HERA-SIDE: a new nullable `pinned_at` column on `orchestrators` and `roles`, mirroring `archived_at`. Mutual exclusivity is enforced in the DAO (pin clears `archived_at`; archive clears `pinned_at`). Consequence/gap: a pinned hera coordinator/agent does NOT reflect into argus's own TUI Pinned section, and freelancers (unmanaged argus tasks with no hera row) cannot be pinned from hera (P on a freelancer gives visible feedback naming the gap).
- **Navigable Archive section below Freelance (Story 2).** Archived Hera tasks that previously vanished — archived freelancers especially, plus archived root coordinators — render in the existing bottom-of-rail `Archive (N)` expando, now reachable by normal j/k navigation WITHOUT `l`, collapsed by default (space/Enter toggles), with `a` on a row there unarchiving it back to where it belongs. Archived freelancers render ONLY in this Archive section (never inline in their repo group), so there is no double-render; `l` still works as the show/hide-all convenience that force-expands every Archive expando and reveals dead rows.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: new requirements — a Pinned section at the rail top with a `P` toggle (pin/archive mutually exclusive, persisted hera-side); a navigable Archive section below Freelance holding archived Hera tasks (incl. freelancers) reachable without `l`. Modified requirement — the RAIL-only mutation key set gains `P`.

## Impact

- `internal/db/schema.go`: migration `0005_pinned_at` adds nullable `pinned_at` to `orchestrators` and `roles`.
- `internal/db/types.go`: `Orchestrator.PinnedAt` / `Role.PinnedAt`.
- `internal/db/orchestrators.go`, `internal/db/roles.go`: `Pin`/`Unpin` DAO verbs (pin sets `pinned_at`, clears `archived_at`); `Archive` also clears `pinned_at`; scans read `pinned_at`.
- `internal/view/ops/types.go`, `service.go`, `pin.go`: `Pinned` on ops `Orchestrator`/`Role`; `DB` interface gains `PinOrchestrator`/`UnpinOrchestrator`/`PinRole`/`UnpinRole`; `Service.Pin*`/`Unpin*` (pin clears the argus-archived side via the existing unarchive path).
- `internal/view/ops_adapters.go`: wire the new DAO verbs + carry `Pinned`.
- `internal/view/keys.go`: `P` → `OnPin` (RAIL-only; in a pane `P` forwards its byte).
- `internal/view/mutations.go`: `MutationHandler.OnPin`; `railSelection.Pinned`; `OnPin` toggles via the ops Pin/Unpin verbs.
- `internal/view/rail_list.go`: `orchEntry.Pinned` / `roleEntry.Pinned`; a `railRowPinnedSep`; buildRows renders the Pinned section first (pinned wins over active/archived), floats pinned roles out of their coordinators, and routes archived freelancers into the bottom Archive expando; `SetArchivedFreelance`.
- `internal/view/app.go`: `populateRail` loads orchestrators inclusively (archived root coords feed the bottom Archive without `l`), sets `Pinned`, splits freelancers into active + archived sets, carries `Pinned` into `CurrentRailSelection`.
- Tests: DAO pin/unpin + mutual exclusivity; ops Pin/Unpin; rail Pinned-section rendering + float-out; `P` routing; archived freelancer renders in the bottom Archive (not vanished) and unarchives via `a`; no double-render with `l`.
