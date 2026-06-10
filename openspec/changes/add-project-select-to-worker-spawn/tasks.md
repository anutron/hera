# Tasks: add-project-select-to-worker-spawn

**Design doc:** `openspec/changes/add-project-select-to-worker-spawn/design.md`

## 1. Tests

- [ ] 1.1 In `internal/view/ops`, write a failing test proving `SpawnWorker` creates the argus task + worker role in `in.Project` when it is set (different from the coord's project), asserting both `CreateTaskRequest.Project` and the inserted role's `ArgusProject`.
- [ ] 1.2 Write a failing test proving `SpawnWorker` falls back to `coordRole.ArgusProject` for both the task and the role when `in.Project` is empty (today's behavior).
- [ ] 1.3 Write a failing test proving the empty-`argus_project` guard fires when the *effective* project is empty (coord project empty AND no override).
- [ ] 1.4 Write a failing test for `Service.CoordProject(ctx, coordRoleID)` returning the coord role's `argus_project` (and erroring on an unknown role id).
- [ ] 1.5 In `internal/view`, write a failing handler test proving `OnNewWorker` passes the modal-selected project through to `SpawnWorker` and seeds the default to the coord's project index (extend the existing `mutationService` fake + modal fake to capture `ShowNewWorkerForm` args and the `SpawnWorkerInput.Project`).
- [ ] 1.6 Confirm every `it should X` acceptance criterion in `design.md` maps to a failing test (Prove-It Pattern).

## 2. Ops: project override + CoordProject

**Depends on:** Stage 1

- [ ] 2.1 Add `Project string` to `ops.SpawnWorkerInput` with a doc comment (optional override; empty = inherit coord's project).
- [ ] 2.2 In `SpawnWorker`, compute `effectiveProject := strings.TrimSpace(in.Project)`; if empty, use `coordRole.ArgusProject`. Apply the empty-project guard to the effective value.
- [ ] 2.3 Use `effectiveProject` for `CreateTaskRequest.Project` and `CreateRoleInput.ArgusProject` (replacing the two `coordRole.ArgusProject` references). Keep `coordRole.Name` for the orientation prefix.
- [ ] 2.4 Add `Service.CoordProject(ctx, coordRoleID) (string, error)` that loads the role via `s.DB.GetRoleByID` and returns its `ArgusProject`.
- [ ] 2.5 Run the Stage 1 ops tests to green.

## 3. Modal: ShowNewWorkerForm

**Depends on:** Stage 1

- [ ] 3.1 Add `ShowNewWorkerForm(title string, projects []string, defaultProjectIdx int, onSubmit func(project, prompt string), onCancel func())` to `internal/view/modals.go`, mirroring `ShowNewCoordForm`: a `inlineCycler` Project field (initialized to `defaultProjectIdx`, `(no projects configured)` when empty → maps to empty on submit) + a `styledTextArea` Prompt field.
- [ ] 3.2 Reuse the `ShowNewCoordForm` Enter-capture rules: Enter on the cycler advances focus (no submit); plain Enter on the textarea submits; Shift/Ctrl+Enter inserts a newline. Theme + center the modal; size it to the two fields.
- [ ] 3.3 Add the `modalAPI` interface method for `ShowNewWorkerForm` and register a `pageNewWorker` page id; wire it on `*App`.

## 4. Handler: OnNewWorker wiring

**Depends on:** Stage 2, Stage 3

- [ ] 4.1 Add `CoordProject(ctx, coordRoleID) (string, error)` to the `mutationService` interface.
- [ ] 4.2 In `OnNewWorker`, after resolving `coordRoleID`, in the `goUI` block: call `b.svc.ListProjects` and `b.svc.CoordProject(coordRoleID)`; on error surface an error modal and abort the open. Compute `defaultProjectIdx` as the index of the coord project in the list (fallback 0).
- [ ] 4.3 Replace the `ShowTextAreaInput` call with `ShowNewWorkerForm`, passing projects + default index. In the submit callback keep the empty-prompt rejection notice, then call `SpawnWorker` with `Project` set to the selected project (empty when `(no projects configured)`), preserving the auto-select-on-success behavior.
- [ ] 4.4 Run the Stage 1 handler test to green.

## 5. Verification

**Depends on:** Stage 2, Stage 3, Stage 4

- [ ] 5.1 `go build ./...` and `go vet ./...` clean.
- [ ] 5.2 Full `go test ./internal/view/...` green.
- [ ] 5.3 `openspec validate add-project-select-to-worker-spawn --strict` passes.
