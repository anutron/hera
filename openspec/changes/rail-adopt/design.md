## Context

`hera_join`'s attach-mode (`internal/mcp/handler_join.go::attach`) is the canonical path that turns a live argus task into a managed worker: resolve the orchestrator, reject a duplicate binding to it, create a `worker` role (write-once on mission/constraints/project), create a live binding, and best-effort stamp `meta:hera.role`. Today only the agent itself can invoke it (via the MCP tool). Operator-side adoption from the rail needs the same row-creation, triggered server-side from a key gesture.

## Goals

- Reuse the role+binding creation logic, not duplicate it.
- Keep the gesture RAIL-only and freelancer-only, with visible feedback on every non-applicable case.
- Never block or deadlock the tview event loop.

## Decisions

### D1: A new ops verb owns the operator-side binding creation

`Service.AdoptTaskIntoOrchestrator(ctx, AdoptInput)` performs the adoption. It mirrors `attach()`'s sequence against the `ops.DB` interface:

1. Validate `ArgusTaskID` non-empty (validation error otherwise).
2. Load the orchestrator by id (`GetOrchestratorByID`); `ErrNotFound` → validation error.
3. Guard: if the task already has ANY live binding (`ListLiveBindingsByTask`), reject — a real freelancer has none, so a hit means a race or a mislabeled row. (`hera_join` rejects only same-orchestrator bindings because an agent may legitimately multi-bind; the operator adopting a *freelancer* should see "already managed" instead.)
4. De-collide the role name: starting from the requested name, if an ACTIVE role with that name already exists under the orchestrator (`GetRoleByOrchestratorAndName`), append `-2`, `-3`, … until free. Archived siblings do not block (matching `Roles.Create`).
5. `CreateRole(kind=worker, argus_project=<freelancer repo>)`, then `CreateBinding(role, orchestrator, task, worktree)`.
6. Best-effort `PutTaskMeta(task, "role", "worker")` — a transient argus failure must not undo the binding (mirrors `attach()`).

Why a new ops verb rather than calling the MCP handler: the MCP handler resolves a task from a `cwd` and returns an MCP `Response`; the operator already has the argus task id and orchestrator id from the rail selection, and the view needs a plain `(result, error)`. Both paths now converge on `db.Roles.Create` + `db.Bindings.Create`, so the binding logic lives in the DAO layer exactly once.

### D2: ops.DB / ops.ArgusClient grow the minimum surface

`ops.DB` gains `CreateRole`, `CreateBinding`, and `GetRoleByOrchestratorAndName` (neutral input/return shapes in `ops/types.go`); `ops.ArgusClient` gains `PutTaskMeta`. The production adapters in `ops_adapters.go` translate to `db.Roles.Create` / `db.Bindings.Create` / `db.Roles.GetByOrchestratorAndName` and `argus.Client.PutTaskMeta`. Tests substitute fakes.

### D3: The picker reuses the modal infra; `J` reuses the mutation-bridge concurrency contract

A new `modalAPI.ShowSelect(title, label, items, onSelect(idx), onCancel)` opens a themed `tview.List` (Enter selects, Esc cancels), styled like the other modals and dismissable, restoring prior focus. `mutationBridge.OnAdopt`:

- reads the selection synchronously (UI state, on-loop);
- gates: not a freelancer role → `notApplicable("J: only freelancers can be adopted into a coordinator")`; freelancer with empty argus task id → notice;
- hands the blocking work off-loop via `goUI`: list the active orchestrators, and if none, surface a notice; otherwise open the picker. The picker's `onSelect` (fired on the loop) calls `mutate("adopt", true, …)`, which claims the in-flight flag and runs the binding op on its own goroutine, refreshing the rail on success and showing an error modal on failure.

This is the exact pattern the existing mutation keys use (read sync → `goUI` modal → `mutate`), so the loop is never blocked and a second adopt while one is in flight no-ops with feedback.

### D4: `J` is RAIL-only; the rune forwards in a pane

`handleRail` adds a `case 'J'` that calls `Mutations.OnAdopt()` and consumes the event. `handlePane` is unchanged, so in a pane `J` is encoded as the rune and forwarded to the PTY like any other letter. `j` (lowercase, nav-down) is untouched. `J` was unbound before this change.

### D5: The adopted role records the freelancer's repo as its argus_project

The freelancer's repo (`freelanceProject.Project` / `ArgusTaskInfo.Project`) is carried onto the freelancer `roleEntry` (`Project`) and surfaced on the `railSelection`, so `AdoptTaskIntoOrchestrator` can record it as the new role's `argus_project` (write-once; used later by resurrect and consistent with managed roles). When unknown it is left empty — the binding still succeeds.

## Risks

- A freelancer row's argus task could be bound by a concurrent agent-side `hera_join` between the rail render and the adopt. The D1 step-3 guard catches this and surfaces feedback instead of creating a second binding.
- The picker lists only active orchestrators; if the operator wants to adopt into an archived/dormant coordinator they must resurrect it first. This is intentional — adopting into an archived coordinator would resurrect it implicitly, which is surprising.
