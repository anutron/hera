## Why

Adding a worker to a running orchestrator currently requires being inside the coordinator's session (the coord spawns a child task, or an existing task self-attaches via `hera_join`). There is no operator-facing way to add a worker from the rail. The rail already has the inverse verb — `n` creates a new orchestrator — so the natural complement is a one-key "add a worker under this coordinator" that appears and selects immediately.

## What Changes

- Add a RAIL-focus-only **`w`** key that opens a single-field **Prompt** input modal.
- Resolve the rail selection to a target coordinator (a coordinator row directly, or the coordinator of a selected agent row). Non-coordinator selections (freelancer, separator) get a dismissible "not applicable" notice.
- On confirm, spawn a worker agent in the coordinator's argus project and **programmatically** attach it: hera inserts the worker role + binding and mirrors `meta:hera.role=worker` itself — no MCP call by the worker's LLM is required for the nesting.
- Derive the worker's role name from the prompt (argus-style auto-name), uniqued within the orchestrator; operator can `r`-rename later.
- The worker's argus prompt is a short hera orientation prefix followed by the operator's prompt text.
- The new worker row renders nested under its coordinator within ~100 ms and is auto-selected; focus stays in RAIL.
- The worker branches off the project default ref (same as argus / `n` today). No branch field — honoring a chosen base branch needs an argus-side API change and is out of scope.

## Capabilities

### New Capabilities

<!-- None. This extends the existing rail surface. -->

### Modified Capabilities

- `hera-view`: add the `w` spawn-worker verb to the rail — modal trigger and focus-gating, selection-to-coordinator resolution, programmatic role+binding insertion with worktree-path resolution and `meta:hera.role=worker`, prompt-derived unique role naming, orientation-prefixed worker prompt, and immediate nested render + auto-select. Extends the RAIL mutation-key set and the non-applicable-feedback contract.

## Impact

- **Specs:** `openspec/specs/hera-view/spec.md` (modified via delta).
- **Code:**
  - `internal/view/ops/` — new `SpawnWorker` op (new file, e.g. `spawn_worker.go`); add `GetTask` to the ops `ArgusClient` interface in `service.go`; the production adapter wires it to `*argus.Client.GetTask`.
  - `internal/view/mutations.go` — `OnNewWorker` bridge method + `mutationService` interface entry; auto-select of the created row.
  - `internal/view/keys.go` — `case 'w'` dispatch in RAIL focus.
  - `internal/view/app.go` — auto-select-by-task/role hook used after creation (if not already present).
- **No DB migration** (reuses `roles` / `bindings` and the existing broadcaster).
- **No argus change** (uses the existing scope-token `POST /api/tasks`, `GET /api/tasks/{id}`, and `PUT /api/tasks/{id}/meta`).
