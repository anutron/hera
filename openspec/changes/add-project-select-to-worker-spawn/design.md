# Design: add-project-select-to-worker-spawn

## Context

Pressing `w` while focus is `RAIL` opens the new-worker modal. Today that modal
(`ShowTextAreaInput`, `internal/view/modals.go:287`) is a single Prompt textarea.
On confirm, `OnNewWorker` (`internal/view/mutations.go:565`) calls
`SpawnWorker` (`internal/view/ops/spawn_worker.go:43`), which loads the
coordinator role and unconditionally uses `coordRole.ArgusProject` as the project
for both the argus task and the new worker role. The operator has no way to spawn
a worker into a different argus project than the coordinator's.

The new-coordinator form (`ShowNewCoordForm`, `modals.go:597`) already solves the
identical UX problem: it presents a themed `inlineCycler` for Project, fed by
`b.svc.ListProjects()`. This change mirrors that pattern in the worker flow.

## Goals

- Let the operator choose which argus project a spawned worker runs in.
- Default the selection to the coordinator's own project (cursor starts there).
- Leave the no-change path (operator does not touch the cycler) behaving exactly
  as today: worker spawns in the coordinator's project.

## Non-Goals

- No change to branch/base-ref selection (workers still branch off the chosen
  project's default ref — the spec already states "no base branch is selected").
- No change to the orientation prefix, which still names the **coordinator**, not
  the project.
- No change to the `n` new-coordinator form.
- No multi-project / fan-out spawn — exactly one project per worker.

## Decisions

### D1 — Combined modal, mirroring the new-coordinator form

Replace the single-field `ShowTextAreaInput` call in `OnNewWorker` with a new
two-field modal `ShowNewWorkerForm(title, projects []string, defaultProjectIdx int, onSubmit, onCancel)`:

- **Project** — `inlineCycler` (arrow keys cycle, Enter advances focus, does not
  submit), initialized to `defaultProjectIdx`.
- **Prompt** — `styledTextArea` (plain Enter submits, Shift/Ctrl+Enter inserts a
  newline), identical to today's field.

Rationale: the `inlineCycler` + textarea + Enter-capture machinery already exists
and is battle-tested in `ShowNewCoordForm` (BUG-035, BUG-011). A two-step
"prompt, then pick project" flow (`ShowSelect` after confirm) would be more code
paths and a worse operator experience. The combined form is the established
in-repo idiom.

### D2 — Default the cycler to the coordinator's project

The cycler's initial index is the position of the coordinator's `argus_project`
within the `ListProjects` result. `OnNewWorker` resolves the coord role id
(already does, for `SpawnWorker`); it now also needs that role's project string
at modal-open time to compute the default index.

`railSelection` does not carry the coordinator's project for coord/agent rows
(its `Project` field is freelance-only). Rather than thread project through every
rail-population site, add a thin read-only service method:

```go
CoordProject(ctx context.Context, coordRoleID int64) (string, error)
```

It loads the role via the existing `s.DB.GetRoleByID` and returns
`role.ArgusProject`. `OnNewWorker` calls it alongside `ListProjects`, finds the
matching index (falling back to 0 if the project is absent from the list), and
passes both to the modal.

### D3 — Optional `Project` override on `SpawnWorkerInput`

Add `Project string` to `ops.SpawnWorkerInput`. In `SpawnWorker`:

- Resolve the effective project as `in.Project` if non-empty (trimmed), else
  `coordRole.ArgusProject` (preserving today's behavior and the empty-project
  guard).
- Use the effective project for **both** the `CreateTaskRequest.Project` and the
  worker role's `ArgusProject` field, so the rail records the worker under the
  project it actually runs in.

The coordinator role is still loaded (the orientation prefix needs
`coordRole.Name`), and the empty-`argus_project` guard still applies to the
*effective* project so a degenerate state can't create a project-less task.

## Alternatives considered

- **Two-step `ShowSelect` after the prompt** — rejected (D1): more modal
  round-trips, no precedent, worse UX than the new-coord form.
- **Carry coord project on `railSelection`** — rejected (D2): touches multiple
  rail-population code paths for a value one service call resolves cleanly.
- **Always pass the chosen project, drop the empty-fallback** — rejected: keeping
  `Project` optional means the ops contract stays backward-compatible and other
  callers / tests that omit it are unaffected.

## Discovery findings

- `inlineCycler` (`modals.go:418`) is a full `tview.FormItem` with wrap-around
  cycling and a `finishedFunc(KeyTab)` focus-advance on Enter — reusable as-is.
- `ShowNewCoordForm` shows the exact theming, Enter-capture, and
  `(no projects configured)` empty-list handling to replicate.
- `SpawnWorker` already centralizes project resolution in one place
  (`coordRole.ArgusProject`), so the override is a one-line branch.
- The base requirement "`w` spawns a worker under the selected coordinator"
  (`openspec/specs/hera-view/spec.md:888`) states the modal is single-field and
  the project is "the coordinator role's `argus_project`" — both clauses are
  modified by this change.

## Risks / Trade-offs

- **Empty project list** — if `ListProjects` returns nothing, the cycler shows
  `(no projects configured)` and submit maps it to empty, falling back to the
  coordinator's project in the ops layer. Same degrade path as `ShowNewCoordForm`.
- **Coord project absent from `ListProjects`** — default index falls back to 0;
  the operator can still cycle. Low likelihood (the coord's project is a
  configured argus project) and non-destructive.

## Acceptance criteria

### Modal (D1, D2)

- it should open a two-field modal (Project cycler + Prompt textarea) when `w` is
  confirmed-eligible, instead of the single Prompt field.
- it should initialize the Project cycler to the coordinator's own project.
- it should still reject an empty/whitespace prompt with a visible notice and no
  argus/DB call.

### Spawn (D3)

- it should create the argus task in the **selected** project when the operator
  cycles to a different project.
- it should create the argus task in the **coordinator's** project when the
  operator leaves the cycler untouched.
- it should record the new worker role's `argus_project` as the selected project.
