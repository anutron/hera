# Proposal: add-project-select-to-worker-spawn

## Why

The `w` new-worker spawn modal only prompts for a Prompt; the spawned worker
always inherits the coordinator's `argus_project` with no way to choose another
project. Operators need to dispatch a worker into a different argus project from
the same coordinator — the new-coordinator form already offers this via a project
cycler.

## What Changes

- Replace the single-field new-worker modal with a two-field form: a **Project**
  selector (inline cycler, mirroring the new-coordinator form) plus the existing
  **Prompt** textarea.
- Default the Project selector to the coordinator's own `argus_project`.
- Add an optional `Project` override to `ops.SpawnWorkerInput`; when set, the
  argus task and the worker role are created in that project instead of the
  coordinator's. When unset, behavior is unchanged.
- Add a thin read-only `CoordProject(ctx, coordRoleID)` service method to resolve
  the coordinator's project for seeding the cycler's default.

No breaking changes — the project override is optional and the untouched-cycler
path reproduces today's behavior.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- **hera-view** — the `w` new-worker spawn requirement gains a Project selector
  (defaulting to the coordinator's project) and creates the worker in the selected
  project.

## Impact

- `internal/view/modals.go` — new `ShowNewWorkerForm` modal (reuses `inlineCycler`,
  `styledTextArea`, theming, Enter-capture from `ShowNewCoordForm`).
- `internal/view/mutations.go` — `OnNewWorker` loads projects + coord project,
  computes the default index, opens the new modal, and passes the chosen project
  to `SpawnWorker`; `mutationService` interface gains `CoordProject`.
- `internal/view/ops/spawn_worker.go` — `SpawnWorkerInput.Project` field +
  effective-project resolution; new `Service.CoordProject`.
- Tests for the ops resolution and the modal/handler wiring.
