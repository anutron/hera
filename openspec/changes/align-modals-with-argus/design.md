# Design: align-modals-with-argus

## Context

hera's rail spawns work through two input modals: the **new-coordinator** form (`n`) and the **new-worker** form (`w`). Both today select the argus project with a custom `inlineCycler` widget — a single-line `◄ current (n/m) ►` control cycled with Left/Right. argus's own **New Task** modal instead uses a scrollable, type-to-filter project **list** with a `>` cursor and an `(N/M)` counter. The cycler is the primary operator pain point: with more than a handful of projects, cycling one-at-a-time is slow and gives no overview.

The design target for hera's TUI is "the operator should feel they haven't left argus" (see the hera-view design target memory). The two modals diverging from argus's New Task modal breaks that illusion. This change brings hera's modals to field-and-behavior parity with argus's New Task modal:

- list + type-to-filter **Project** selector (replacing the cycler),
- a **Branch** field,
- a **`◄ backend ►` Backend** cycler (argus itself uses a cycler here — kept),
- a multi-line **Prompt** field,
- an `Enter submit / Tab next / Esc cancel` footer.

The new-worker modal today carries only **Project + Prompt**; it grows to the full four fields, reaching parity with the new-coordinator modal (which already has all four, just with cyclers for Project and Backend).

A paused bug, **BUG-004** (pasting into the hera modal is slow / ingested rune-by-rune), rides along: the change makes the modal *paste-ready* and proves it with a headless test. See `## BUG-004: paste path` for why the guaranteed fix is hera-side paste-readiness, not a blanket "paste is now fast" claim.

## Goals

- Replace the `inlineCycler` **Project** selector in both modals with a scrollable, type-to-filter list (`>` cursor, `(N/M)` counter), matching argus's New Task modal.
- Give the new-worker modal the full field set: Project (list), Branch, Backend (cycler), Prompt — wired functionally through hera's existing spawn path.
- Keep the **Backend** selector a `◄ backend ►` cycler (argus does too); only **Project** becomes list-mode.
- Make the modal **paste-ready**: a paste event delivered to the modal lands in the focused field as a single chunk, verified headlessly.
- Preserve every existing modal contract: modal-takes-focus, argus theme, Esc/Enter dismissal, no focus-steal on background repaint, and off-event-loop mutation hand-off.

## Non-Goals

- **No skill-autocomplete field.** argus's New Task modal has one; hera's modals never did and hera's spawn path doesn't thread a skills argument. Out of scope.
- **No extraction to argus-sdk.** We port argus's *design* into hera using hera's own primitives — we do not move `NewTaskForm` into argus-sdk, nor introduce a shared widget. (Considered and rejected — see `## Alternatives considered`.) The accepted tradeoff is that hera's modal can re-drift from argus over time.
- **No transport-layer paste fix in this change.** If the diagnostic shows bracketed-paste markers never reach hera (an argus / argus-sdk transport concern), that fix is captured as a cross-repo follow-up, not delivered here.
- **No `tview.DropDown`.** The cycler was chosen deliberately to avoid `tview.DropDown`'s green popup overlay, un-themed background, and "Enter submits while the list is open" behavior (BUG-035). The replacement is a custom `FormItem`, never `tview.DropDown`.

## Decisions

### Decision: one new custom FormItem, `inlineListSelect`

Build a single new widget — `inlineListSelect` — in `internal/view/modals.go`, implementing the full `tview.FormItem` interface exactly as `inlineCycler` does (`GetLabel`, `SetFormAttributes`, `GetFieldWidth`, `GetFieldHeight`, `SetFinishedFunc`, `SetDisabled`, `Draw`, `InputHandler`), so it drops into the existing `tview.NewForm()` flow with no form-machinery changes. It renders a one-line filter prompt plus a scrollable option list and reports the selected option via `GetCurrentOption()` (same accessor shape as `inlineCycler`, so call sites change minimally).

It is used for **Project** in both modals. **Backend** keeps `inlineCycler`. So one widget is built, not two.

### Decision: list + type-to-filter behavior

- Up/Down move the `>` cursor within the *filtered* options; the cursor clamps (no wrap is required — argus does not wrap its list) and the visible window scrolls to keep the cursor in view.
- Typing a printable rune appends to the filter string and re-filters (case-insensitive substring match over the option labels); the cursor resets to the first filtered row.
- Backspace deletes the last filter rune; clearing the filter restores the full list.
- `(N/M)` shows `(cursor-position / filtered-count)`; the filter string is shown inline so the operator sees what they typed.
- Enter locks the current selection and advances focus via `finishedFunc(KeyTab)` — it does **not** submit the form (same contract as `inlineCycler`). Tab/Backtab advance/retreat focus; Esc cancels via `finishedFunc(KeyEscape)`.
- Empty option list degrades to a single visible `(no projects configured)` entry that maps to `""` on confirm (the ops layer falls back to the coordinator's project) — preserving the current spec's empty-list contract.

### Decision: multi-row field height + taller modals

`inlineListSelect.GetFieldHeight()` returns a fixed visible height (filter row + a bounded number of list rows). Both modal heights grow to accommodate it. `centeredModal` already takes an explicit height; the constants in `ShowNewCoordForm` / `ShowNewWorkerForm` are recomputed for the new field heights (the existing code already documents its height arithmetic, so we follow that pattern).

### Decision: Enter-routing switch learns the new widget

Both forms install a `form.SetInputCapture` that, on Enter, inspects the focused form item: `*inlineCycler` and `*styledTextArea` get special handling, everything else submits. A `case *inlineListSelect:` is added so Enter on the list locks+advances (returns the event to the widget's own `InputHandler`) rather than submitting. Printable runes are not intercepted by the form capture (it only handles Enter), so they reach the focused list's `InputHandler` for filtering — no extra wiring needed.

### Decision: wire Branch + Backend through the worker spawn path

The plumbing already exists end to end:

- `argus.CreateTaskInput` carries `Branch` and `Backend` (`internal/argus/tasks.go`).
- `ops.CreateTaskRequest` carries `Branch` and `Backend` (`internal/view/ops/service.go`).
- `ops.ListBackends` is exposed and already wired into the view bridge (used by the coord form).
- `hera_spawn_worker` already accepts `Branch` / `Backend`.

So "wire them functionally" is mostly surfacing fields the spawn path can already consume: `ShowNewWorkerForm` gains Branch + Backend inputs and threads them into the worker `CreateTaskRequest`. The worker no longer hard-defaults to the project's default ref; it branches off the chosen Branch (empty = project default) and uses the chosen Backend (empty = project default).

### Decision: BUG-004 handled as paste-readiness, diagnosed not assumed

The change guarantees hera-side paste-readiness (the modal delivers a paste event to the focused field as one chunk) and verifies it headlessly. Whether end-to-end paste is fast depends on the transport delivering bracketed-paste markers, which is outside hera's modal code. See `## BUG-004: paste path`.

## BUG-004: paste path

**Why this is not a simple modal fix.** hera's view does not run on a real terminal. The daemon serves the whole TUI over a WebSocket; argus's plugin-view client is the terminal. The screen is built by `pluginview.New` as a **real tcell terminfo screen** over a WS-backed tty (`tcell.NewTerminfoScreenFromTtyTerminfo`). tcell's own input parser runs on the inbound byte stream and emits `*tcell.EventPaste` for bracketed paste **iff** (a) paste is enabled (`tApp.EnablePaste(true)` at `internal/view/app.go:238` — it is) and (b) the byte stream actually contains the `ESC[200~` … `ESC[201~` markers.

hera's modal text fields (`styledInputField`, `styledTextArea`) wrap tview's `InputField` / `TextArea` and override only `SetFormAttributes` — their native `PasteHandler` is intact. So if an `EventPaste` reaches the focused modal field, it already pastes in one operation. The rune-by-rune symptom therefore indicates the bracketed-paste markers are **not arriving** over the argus → WS → hera path — a transport concern in argus / argus-sdk, not hera's modal code.

**What this change delivers (guaranteed, hera-side):**

- The new modal is paste-ready: with a `tcell.EventPaste` delivered to an open modal, the focused Prompt field receives the entire pasted string in a single operation (no per-rune redraw). This is verifiable headlessly via a `tcell` SimulationScreen + `PostEvent(EventPaste)` and asserting the field's contents.
- The new `inlineListSelect` does not regress this: a paste delivered while a text field is focused still reaches that field (the list widget only consumes input when it holds focus).

**What this change does NOT deliver (and why):**

- A guarantee that real-terminal paste is fast end to end. If the diagnostic confirms hera never receives `EventPaste` (markers stripped upstream), the true fix is to make the argus plugin-view client forward bracketed-paste markers (and/or enable bracketed paste on its upstream terminal). That is cross-repo work against argus / argus-sdk and is captured in `## Cross-repo follow-ups`, executed by an agent spawned in those projects — a hera agent cannot modify them.

## Cross-repo follow-ups

- **argus / argus-sdk bracketed-paste forwarding (conditional).** If the paste diagnostic shows hera does not receive `tcell.EventPaste` for a paste, file a follow-up against the argus plugin-view client / argus-sdk pluginview transport to forward `ESC[200~`/`ESC[201~` markers end to end. Once delivered, hera's already-paste-ready modal closes BUG-004 with no further hera change. Must be executed by an agent spawned in the argus / argus-sdk project.
- **Per-project default backend.** Both spawn modals initialize the Backend cycler to the first configured backend, not the effective project's configured default, because `ListBackends` returns a flat global list with no per-project default marker (the new-coordinator form already shipped with this simplification). A future follow-up could plumb a per-project default backend (e.g. via argus `GET /api/projects`) and thread its index into both `ShowNewCoordForm` and `ShowNewWorkerForm` so the cycler opens on the project's real default. Touches the argus client / API, so the argus side is executed by an agent spawned in the argus project.

## Risks / Trade-offs

- **Re-drift from argus (accepted).** Porting the design rather than sharing a widget means hera's modal can drift from argus over time — the exact failure mode that produced this change. Accepted in exchange for a one-repo, low-risk change. The durable fix (extract to argus-sdk) is recorded under `## Alternatives considered` for a future change.
- **BUG-035 regression risk.** Re-introducing a list selector risks the dropdown bugs the cycler was built to avoid. Mitigated by building a custom `FormItem` (never `tview.DropDown`) and by an explicit Enter-routing case that keeps Enter from submitting while the list is focused.
- **Focus-contract regression.** A new multi-row FormItem must integrate with the form's Tab cycle and the modal focus contract (spec: modal takes focus, survives background repaints, Esc/Enter dismiss). Mitigated by implementing the same `FormItem` interface as `inlineCycler` and reusing the existing `themeFormStyle` / `captureFocus` / `closeModal` machinery unchanged.
- **BUG-004 expectation management.** Stakeholders may expect "paste is now fast." The guaranteed deliverable is paste-readiness + a headless proof; end-to-end speed may require the cross-repo follow-up. Called out explicitly so the change is not mis-sold.

## Migration Plan

No data, schema, or protocol migration. UI-only. Reversible by reverting the modal changes. No persisted state changes shape.

## Alternatives considered

- **Port into hera (CHOSEN).** Re-implement argus's modal design in `internal/view/modals.go` with hera's own primitives. ~light, one repo, low risk. Downside: re-drifts from argus.
- **Extract `NewTaskForm` to argus-sdk.** Move argus's form into argus-sdk as a self-contained widget behind abstract interfaces (`ProjectInfo`, abstract skills), consumed by both argus and hera. True single-source parity, eliminates drift — the durable answer to the "feel like argus" target. Rejected for *this* change as the heaviest option: upfront interface design, argus-sdk versioning, and changes across all three repos (argus work needs an agent spawned in argus). Recorded as the preferred future direction.
- **Hybrid: extract only the project-list-select widget.** Share just the pain-point widget via argus-sdk, port the rest. Rejected to keep this change single-repo; the widget is the part most worth sharing, so a future extraction can start here.

## Acceptance criteria

Project selector (both modals):

- it should render the Project selector as a scrollable list with a `>` cursor and an `(N/M)` counter (not a `◄ ►` cycler)
- it should move the `>` cursor down/up within the list on Down/Up without submitting the form
- it should filter the visible options to those matching the typed filter text (case-insensitive substring)
- it should reset the cursor to the first match when the filter changes
- it should restore the full option list when the filter is cleared
- it should lock the highlighted option and advance focus to the next field on Enter (not submit the form)
- it should show a `(no projects configured)` entry that maps to the coordinator's project on confirm when the project list is empty

new-worker modal field set:

- it should open with Project (list), Branch, Backend (cycler), and Prompt fields
- it should default the Branch field empty (empty = project default ref) and the Backend cycler to the first configured backend
- it should issue `POST /api/tasks` carrying the chosen Branch and Backend when the operator sets them
- it should branch the worker off the project's default ref when the Branch field is left empty

Backend selector:

- it should render Backend as a `◄ backend ►` cycler in both modals

Paste-readiness:

- it should deliver a paste event to the focused Prompt field as a single chunk (the field contains the whole pasted string in one operation, no per-rune ingestion)

Preserved contracts:

- it should take keyboard focus when the modal opens and restore prior focus on dismissal
- it should dismiss on Esc (cancel) and submit on Enter from a text field
- it should not lose focus to a background rail repopulate while open
