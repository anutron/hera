## Context

Today the only way to add a worker agent to a running hera orchestrator is from *inside* the coordinator's session — the coord's Claude spawns a child argus task (which hera auto-adopts via the `link.created` + `meta:hera.role=worker` rule), or an existing argus task attaches itself with `hera_join(orchestrator=…, kind="worker")`. There is no operator-facing path from the rail itself.

The rail already has the inverse-direction creation verb: **`n`** opens a modal and spawns a *new orchestrator*. But `n` defers all row creation to the `hera_new_orchestrator` MCP handler, which runs only when the spawned coord boots and makes its first MCP call. That deferral is correct for `n` (the orchestrator *name* is the only handle the view holds) but it means the row does not appear until seconds later, and the view cannot select it.

This change adds a sibling verb — **`w`** (new worker) — that spawns a worker under the *selected coordinator*, and makes the new row appear and select immediately.

Relevant current state:

- `internal/view/ops/new.go` — `NewOrchestrator`, the `n` op. Spawns a task; inserts no rows.
- `internal/view/ops/service.go` — the ops `Service`, its `DB` / `ArgusClient` interfaces, `CreateTaskRequest` / `CreatedTask`.
- `internal/view/mutations.go` — `mutationBridge`, the async-off-event-loop mutation runner; `railSelection` (carries `OrchestratorID`, `RoleID`, `CoordRoleID`, `ArgusTaskID`, kind flags).
- `internal/view/keys.go` — RAIL key dispatch (`case 'n': … OnNew()`).
- `internal/db/roles.go` / `bindings.go` — `RolesDAO.Create`, `BindingsDAO.Create`; both `Emit` on the broadcaster that drives the ~100 ms rail refresh.
- `internal/events/adopt.go` — the auto-adopt handler; reads `child.WorktreePath` via `GetTask` to populate the binding (the precedent this change follows for worktree-path resolution).
- `internal/argus/tasks.go` — `Client.CreateTask` (returns `{id,name,status}` only), `GetTask` (returns full `Task` incl. `WorktreePath`), `PutTaskMeta`.

## Goals / Non-Goals

**Goals:**

- A RAIL-focus-only **`w`** key that opens a single-field **Prompt** modal and, on confirm, spawns a worker agent under the selected coordinator.
- The new worker row appears nested under the coordinator within ~100 ms and is auto-selected, with focus remaining in `RAIL`.
- Attachment is **fully programmatic**: hera inserts the role + binding and mirrors `meta:hera.role=worker` itself. The worker's LLM is not required to call any hera MCP tool for the nesting, the parent relationship, or the meta mirror.
- The worker shares the coordinator's argus project and branches off that project's default ref — identical to how argus and `n` create tasks today.

**Non-Goals:**

- **Choosing a base branch.** Hera talks to argus with a *scope* token, and argus's scope-token `POST /api/tasks` accepts only `project` / `name` / `prompt` / `backend` — there is no base-branch parameter on that route (base-branch lives only on the master-only raw route and the MCP `task_create` tool). Honoring a "branch off the coord's branch" field requires an argus-side API change and is deliberately deferred to a separate change. Workers branch off the project default.
- **A name field.** The role name is derived from the prompt (operator can `r`-rename later).
- **A project field.** The worker always lands in the coordinator's argus project. A cross-project worker (worker in a different repo than its coord) is a possible later extension, not this change.
- **Creating sub-coordinators** from the rail. `w` creates a leaf worker (`kind=worker`). Sub-coordinator creation remains the multi-binding path.
- **Resurrecting / re-binding** an archived worker. `w` always creates a fresh role.

## Decisions

### D1: New verb `w`, RAIL-focus-only

`w` joins the existing RAIL-only mutation set (`n r a l ? s S ^d ^r ^p`). Per the established focus contract, when focus is `COORD` or `AGENT` the `w` byte is forwarded verbatim to the bound task's PTY and never opens the modal.

*Alternatives considered:* `c` (create) — rejected as less specific; `N` (shift-`n`) — rejected, shift chord is harder and the pairing is not worth the ergonomic cost. `w` = "worker" is literal and the key is free.

### D2: Selection resolves to a coordinator

The modal targets the coordinator implied by the current rail selection:

- Coordinator row (root orchestrator header **or** a sub-coordinator role row) → that coordinator's orchestrator.
- Agent/worker row whose parent is a coordinator → that agent's coordinator (the orchestrator the agent's role belongs to).
- Freelance row, Archive separator, or any non-coordinator-attached selection → a dismissible "not applicable" notice (never a silent no-op), matching the existing non-applicable-key contract.

The `railSelection` struct already carries `OrchestratorID` on every managed row, so the resolution is a field read — an agent row and its coordinator share the same `OrchestratorID`. The coord role's `argus_project` is resolved from the DB at op time (the project the worker is created in).

### D3: View inserts rows directly (not the `n` deferral)

`w` does NOT mirror `n`'s "spawn a task whose prompt calls an MCP bootstrap, let the handler create the rows" pattern. Instead the op:

1. Resolves the target orchestrator + coord role's `argus_project`.
2. Derives a role name from the prompt and ensures uniqueness (D5).
3. Calls argus `CreateTask{Project, Prompt, Meta:{role: worker}}` → gets the new task id.
4. Calls argus `GetTask(id)` → reads `worktree_path` (D6).
5. Inserts the **role** (`kind=worker`, `argus_project`, `mission = prompt`, derived name) and the **binding** (`argus_task_id`, `worktree_path`) via the DAOs. Each `Create` emits on the broadcaster, so the rail repopulates within ~100 ms with the worker nested under its coordinator.
6. Returns the new role/task ids so the bridge can auto-select the row.

This is the choice that makes "appears and is selected immediately" true. It is sound *because*, unlike `n`, the view already holds every handle row creation needs (orchestrator id, project, and — after step 3 — the task id). Auto-adopt cannot double-create: the rail-spawned task has no `link.created` parent naming a hera coordinator, so the adopt handler's first gate never fires.

*Alternatives considered:* mirror `n` exactly (spawn → `hera_join(orchestrator,role,kind=worker)` in the prompt; rows created by the MCP handler). Rejected: the row would not exist for several seconds (task boot + first MCP turn), so immediate selection would require a "pending select; bind when the row arrives" mechanism — more complex, worse UX, and it puts the attachment at the mercy of the worker's LLM actually making the call. The user explicitly wants attachment done programmatically by hera, not by the LLM.

### D4: Attachment + meta are programmatic; orientation is a prompt prefix

The role↔orchestrator nesting and the `meta:hera.role=worker` mirror are written by hera (DAO inserts in step 5; meta via the `CreateTask` meta map in step 3). The worker's LLM is never on the critical path for them.

The spawned task's prompt is a short **orientation prefix** + the operator's prompt text. The prefix names the coordinator and tells the worker it may report progress via `hera_send`. This is plain prompt text — it requires no MCP call to take effect, and if the worker ignores it the binding still makes the worker fully managed (the coordinator can `hera_send` to it and hera injects into its PTY). The worker MAY call `hera_join(cwd=$PWD)` to read its inbox, but is not required to for any of the wiring.

*Alternatives considered:* verbatim prompt (no prefix) — rejected as the default because a worker spawned cold has no way to learn it can talk back; the prefix is cheap and reversible (the operator's text dominates).

### D5: Role name derived from the prompt, uniqued within the orchestrator

Following argus's own task auto-naming (`sanitizeName` — first ~40 chars, slugified), hera derives the worker role name from the prompt. The name must be unique among the orchestrator's **non-archived** roles (the DAO's `(orchestrator_id, name)` active-uniqueness rule); on collision the op suffixes `-2`, `-3`, … until free. A prompt that sanitizes to the empty string falls back to a generic stem (e.g. `worker`) before suffixing. The operator can `r`-rename afterward; the role name is independent of the argus task name (which argus may itself rename asynchronously via Haiku).

*Alternatives considered:* require the operator to type a name — rejected per the operator's request to follow argus's auto-name behavior and keep the modal to one field.

### D6: Binding worktree path resolved via `GetTask`, set at insert time

`bindings` rows are write-once on `worktree_path` (only `Create` sets it; there is no updater), and `^d` / `^p` need it. argus creates the task's worktree synchronously inside `POST /api/tasks` before returning `201`, so a `GetTask(id)` immediately after create returns a populated `worktree_path`. The op reads it there and passes it into `BindingsDAO.Create` — exactly the shape the adopt handler already uses (`child.WorktreePath`). This requires adding `GetTask` to the ops-layer `ArgusClient` interface (the concrete `*argus.Client` already implements it).

*Alternatives considered:* insert the binding with an empty path and backfill later from a periodic reconcile — rejected: no such updater exists, write-once is simpler to reason about, and the worktree is reliably present by the time create returns.

### D7: Off-event-loop, modal-gated, like every other mutation

`w` reuses the `mutationBridge`: the key handler captures the selection synchronously, opens the input modal on the UI loop, and runs the spawn (argus calls + DAO inserts) on a background goroutine that bounces the result (auto-select, or an error modal) back through the event-loop queue. The in-flight guard prevents a second concurrent mutation. The modal uses the argus theme and is Enter/Esc-dismissable, per the existing modal requirement.

## Risks / Trade-offs

- **Empty `worktree_path` if `GetTask` fails or races** → The op treats a create-succeeded-but-worktree-unknown state as a soft degradation: it still inserts the binding (with whatever path it got, possibly empty) and logs a warning; `^d` already soft-no-ops on an empty/missing worktree, and `^p` surfaces a "no resolvable worktree" notice. The worker is still managed and usable. Worktree-path resolution failure must not abort the spawn after the argus task already exists (that would orphan a live task with no hera row).
- **Partial failure after `CreateTask` succeeds but the role/binding insert fails** → leaves a live argus task with no hera row (effectively a freelancer in the coord's project). Mitigation: order the inserts role-then-binding and log loudly; the orphan surfaces in the Freelance section so it is visible and recoverable (the operator can `^d` it or the coord can adopt it). We do not attempt to delete the argus task on insert failure (deletion is itself fallible and could compound the inconsistency).
- **Name-derivation collision storms** (many workers from near-identical prompts) → bounded suffix search; acceptable, and rename is always available.
- **`w` shadows a future need for the key in RAIL** → low; the RAIL keyset is deliberate and `w` is unused.
- **Spawned worker ignores the orientation prefix** → acceptable by design; attachment does not depend on it.

## Migration Plan

Additive, no schema change (reuses `roles` / `bindings` / broadcaster). No migration. Rollback = revert the change; existing orchestrators/roles/bindings are untouched. The new `ArgusClient.GetTask` method on the ops interface is additive.

## Open Questions

None outstanding. Branch selection is explicitly deferred (Non-Goals) pending an argus-side `POST /api/tasks` base-branch parameter.

## Acceptance criteria

Grouped by the design section that owns the behavior; these map 1:1 to scenarios in the delta spec.

**Trigger & focus (D1):**

- it should open the spawn-worker prompt modal when `w` is pressed while focus is RAIL on a coordinator row
- it should forward the `w` byte to the bound task's PTY (and not open the modal) when focus is COORD or AGENT

**Selection resolution (D2):**

- it should resolve an agent-row selection to that agent's coordinator and spawn the worker under it
- it should give a dismissible "not applicable" notice on a freelance row or a separator/expando row, issuing no argus or DB call

**Spawn + programmatic attach (D3, D4, D6):**

- it should reject an empty prompt with a validation error and spawn no task
- it should POST a task in the coordinator's argus project carrying `meta:hera.role=worker`
- it should insert a worker role under the coordinator's orchestrator and a binding to the created task, with the task's worktree path
- it should set the worker's prompt to an orientation prefix followed by the operator's text

**Naming (D5):**

- it should derive the role name from the prompt and suffix it (`-2`, `-3`, …) when it collides with a non-archived sibling role

**Immediacy (D3, D7):**

- it should render the new worker nested under its coordinator within ~100 ms of creation
- it should auto-select the new worker row and leave focus in RAIL
- it should run the spawn off the event loop and surface any failure as an error modal without freezing the UI
