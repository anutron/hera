# Tasks: align-modals-with-argus

**Design doc:** `openspec/changes/align-modals-with-argus/design.md`

## 1. Tests

- [ ] 1.1 Write failing tests for `inlineListSelect`: renders `>` cursor + `(N/M)`; Down/Up move cursor without submit; typing filters (case-insensitive substring); cursor resets to first match on filter change; Backspace edits filter; clearing filter restores full list; Enter locks selection + calls `finishedFunc(KeyTab)`; Esc calls `finishedFunc(KeyEscape)`; empty options → `(no projects configured)` mapping to `""` via `GetCurrentOption()`
- [ ] 1.2 Write failing test that `ShowNewWorkerForm` opens with Project (list), Branch, Backend (cycler), Prompt fields
- [ ] 1.3 Write failing tests that the worker spawn payload carries the chosen Branch + Backend, and that an empty Branch leaves branch unset (project default ref)
- [ ] 1.4 Write failing paste-readiness test: drive the App on a `tcell` SimulationScreen, open a spawn modal with the Prompt focused, `PostEvent(tcell.NewEventPaste(...))` (or the SimulationScreen paste injection), assert the Prompt field contains the whole pasted string in one operation
- [ ] 1.5 Write failing test that Backend renders as a cycler in both modals (regression guard against making Backend a list)
- [ ] 1.6 Confirm every `it should X` criterion in `design.md` maps to a failing test (Prove-It Pattern)

## 2. `inlineListSelect` widget

**Depends on:** Stage 1

- [ ] 2.1 Implement `inlineListSelect` in `internal/view/modals.go` as a `tview.FormItem` (mirror `inlineCycler`'s interface: `GetLabel`, `SetFormAttributes`, `GetFieldWidth`, `GetFieldHeight` (multi-row), `SetFinishedFunc`, `SetDisabled`, `Draw`, `InputHandler`, `GetCurrentOption`)
- [ ] 2.2 `Draw`: render the filter string, a scrollable option window with `>` cursor and themed focused/blurred styles, and the `(N/M)` counter; scroll to keep cursor visible
- [ ] 2.3 `InputHandler`: Up/Down clamp-move cursor; printable rune appends to filter + re-filters (case-insensitive substring) + resets cursor to first match; Backspace edits filter; Enter → `finishedFunc(KeyTab)`; Tab/Backtab → `finishedFunc`; Esc → `finishedFunc(KeyEscape)`
- [ ] 2.4 Empty-options degrade to the `(no projects configured)` sentinel that `GetCurrentOption()` maps to `""`
- [ ] 2.5 Make stage-1 widget tests pass

## 3. New-coordinator modal: list-mode Project

**Depends on:** Stage 2

- [ ] 3.1 In `ShowNewCoordForm`, replace the Project `inlineCycler` with `inlineListSelect`; keep Backend as `inlineCycler`
- [ ] 3.2 Add `case *inlineListSelect:` to the form's Enter-routing `SetInputCapture` switch so Enter on the list locks+advances (does not submit)
- [ ] 3.3 Recompute `newCoordModalHeight` for the taller Project field; keep `themeFormStyle` / `captureFocus` / `closeModal` unchanged

## 4. New-worker modal: full field set + functional wiring

**Depends on:** Stage 2

- [ ] 4.1 In `ShowNewWorkerForm`, replace the Project `inlineCycler` with `inlineListSelect` and add a Branch `styledInputField` (default empty) and a Backend `inlineCycler`; extend the signature to accept the backend option list + defaults and to return Branch + Backend
- [ ] 4.2 Add `case *inlineListSelect:` to this form's Enter-routing switch; recompute `newWorkerModalHeight`
- [ ] 4.3 In the worker-spawn call path (`internal/view/mutations.go` / the new-worker bridge), load the backend list (as the coord form already does) and thread the chosen Branch + Backend into `ops.CreateTaskRequest`
- [ ] 4.4 Make stage-1 field-set and spawn-payload tests pass

## 5. Paste-readiness

**Depends on:** Stage 3, Stage 4

- [ ] 5.1 Ensure the focused text field applies a delivered `tcell` paste event as a single chunk (styled wrappers keep tview's native `PasteHandler`; the list widget does not intercept paste unless focused); make the stage-1 paste-readiness test pass
- [ ] 5.2 Run the paste diagnostic: determine whether hera receives `tcell.EventPaste` for a real paste over the argus transport. If it does, BUG-004 is closed hera-side. If it does NOT (markers stripped upstream), record a cross-repo follow-up against argus / argus-sdk per `design.md` "Cross-repo follow-ups" (execute via an agent spawned in that project) and note BUG-004's end-to-end fix as deferred — do NOT claim end-to-end paste is fixed

## 6. Verify

**Depends on:** Stage 5

- [ ] 6.1 `make build` passes
- [ ] 6.2 `make test` passes
- [ ] 6.3 `make vet` passes
- [ ] 6.4 `make lint` passes
- [ ] 6.5 `openspec validate align-modals-with-argus --strict` passes
