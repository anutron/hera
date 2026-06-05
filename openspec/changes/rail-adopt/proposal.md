## Why

A freelancer is a live argus task hera does not manage — no live binding to any role, so it renders in the rail's Freelance section. Today the only way to fold a freelancer into a coordinator is to paste a `hera_join(orchestrator, role_name, kind=worker)` instruction into the agent's PTY and trust it to run. That is slow, error-prone, and asks the operator to leave the rail to drive a binding hera already owns.

The operator should be able to adopt a freelancer FROM THE RAIL, with no agent action required. Hera owns the bindings table — it can create the worker binding directly, exactly as `hera_join`'s attach-mode does, but operator-triggered and server-side.

## What Changes

- Pressing **`J`** while RAIL is focused on a **freelancer** row opens a **target picker** — a themed, focusable, dismissable modal listing the active (non-archived) orchestrators. `Enter` selects; `Esc` cancels.
- Selecting an orchestrator **adopts** the freelancer into it: hera creates a `worker` role (default name derived from the freelancer's argus task name, de-collided against the orchestrator's existing active roles) and a live binding from the freelancer's argus task to that role — the SAME role+binding creation `hera_join`'s attach-mode performs, factored into a single reusable path so the binding logic is not duplicated. The freelancer's argus task is best-effort stamped `meta:hera.role=worker` for parity with `hera_join`.
- After adoption the row leaves the Freelance section and re-renders as a **worker under the chosen coordinator**. The adopted agent is independent — it can later `hera_join(cwd)` to claim its role and receive coordinator messages — but the binding exists immediately, with no agent action required.
- `J` is **RAIL-focus-only** and applies **only to a freelancer selection**. On any non-freelancer row (coordinator, managed agent, orchestrator header, section) it surfaces visible feedback (`J: only freelancers can be adopted into a coordinator`) — never a silent no-op. In a pane `J` forwards to the PTY as the rune.
- Edge cases get visible feedback, never silence: a freelancer with no argus task id (no-op with notice), an argus task already bound to some orchestrator (race — notice), and no active orchestrators to adopt into (notice).
- The binding op runs **off the tview event loop** via the existing async-mutate pattern, so it never blocks or deadlocks the loop.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the rail gains a `J` adopt gesture — on a freelancer it opens an orchestrator picker and creates an operator-side worker binding (mirroring `hera_join` attach-mode); on any other row it surfaces feedback.

## Impact

- `internal/view/ops/adopt.go` (new) + `ops/types.go`: `Service.AdoptTaskIntoOrchestrator` reusing the role+binding creation path; `ops.DB` gains `CreateRole` / `CreateBinding` / `GetRoleByOrchestratorAndName`; `ops.ArgusClient` gains `PutTaskMeta`; `Service.ListActiveOrchestrators` for the picker.
- `internal/view/ops_adapters.go`: `dbAdapter` + `argusAdapter` wire the new ops methods onto the concrete `*db.DB` DAOs (`Roles.Create`, `Bindings.Create`, `Roles.GetByOrchestratorAndName`) and `*argus.Client.PutTaskMeta`.
- `internal/view/keys.go`: `OnAdopt()` on `MutationHandler`; `J` fires it in `handleRail` (RAIL-only; in a pane the rune forwards to the PTY).
- `internal/view/mutations.go`: `mutationBridge.OnAdopt` — freelancer-only gate with feedback, off-loop list + picker modal, off-loop adopt via `mutate`; `mutationService` gains `AdoptTaskIntoOrchestrator` + `ListActiveOrchestrators`.
- `internal/view/modals.go`: `ShowSelect` picker modal (themed `tview.List`, Enter selects, Esc cancels) on `modalAPI` + `*App`.
- `internal/view/app.go` + `rail_list.go`: carry the freelancer's repo (`Project`) onto the role selection so the adopted role records a sensible `argus_project`.
- Tests: `ops/adopt_test.go`, `keys_test.go`, `mutations_test.go`, `modals_test.go`.
