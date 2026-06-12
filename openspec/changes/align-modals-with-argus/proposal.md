## Why

hera's new-coordinator and new-worker modals select the argus project with a single-line `◄ ►` cycler, which is slow and gives no overview once a coordinator spans more than a few projects. argus's New Task modal uses a scrollable, type-to-filter project list — and the design target for hera's TUI is "the operator should feel they haven't left argus." This change brings hera's modals to field-and-behavior parity with argus's New Task modal and makes the modal paste-ready (the hera-side half of BUG-004).

## What Changes

- Add a custom `inlineListSelect` FormItem: a scrollable, type-to-filter option list with a `>` cursor and `(N/M)` counter, themed and keyboard-driven like argus's New Task project list. Never `tview.DropDown` (avoids BUG-035).
- Replace the `inlineCycler` **Project** selector with `inlineListSelect` in both the new-coordinator and new-worker modals.
- Keep **Backend** as a `◄ backend ►` cycler (argus does too); only Project becomes list-mode.
- Grow the **new-worker** modal from Project + Prompt to the full four fields: Project (list), Branch, Backend (cycler), Prompt — reaching parity with the new-coordinator modal.
- Wire Branch + Backend functionally through the worker spawn path: the worker branches off the chosen Branch (empty = project default ref) and uses the chosen Backend (empty = project default), via the existing `CreateTaskRequest` plumbing.
- Make the modal paste-ready: a paste event delivered to an open modal lands in the focused field as a single chunk, verified headlessly (hera-side half of **BUG-004**).
- Preserve all existing modal contracts (focus capture/restore, argus theme, Esc/Enter dismissal, no focus-steal on background repaint, off-event-loop mutation hand-off).

## Capabilities

### New Capabilities

_(none)_

### Modified Capabilities

- `hera-view`: The `w` spawn-worker modal's Project selector changes from a cycler ("allow the operator to cycle to any other project") to a scrollable type-to-filter list, and the modal gains Branch + Backend fields wired into the spawn so the worker branches off the chosen base ref / backend (previously fixed to the project default ref, no base branch). The `n` new-coordinator modal's Project selector likewise becomes list-mode. A modal paste-readiness requirement is added.

## Impact

- `internal/view/modals.go`: new `inlineListSelect` FormItem; `ShowNewCoordForm` swaps its Project cycler for the list; `ShowNewWorkerForm` swaps its Project cycler for the list and gains Branch + Backend fields; Enter-routing `SetInputCapture` switch learns `*inlineListSelect`; modal height constants recomputed.
- `internal/view/mutations.go` (and the new-worker call path / bridge): thread Branch + Backend from the worker form into `ops.CreateTaskRequest`; load the backend list for the worker form (as the coord form already does).
- `internal/view/modals_test.go`, `internal/view/mutations_test.go`: new widget behavior, field-set, spawn-payload, and paste-readiness tests (the last via a `tcell` SimulationScreen + `EventPaste`).
- No schema, MCP-tool, or wire-protocol changes.
- Conditional cross-repo follow-up (argus / argus-sdk) if the paste diagnostic shows bracketed-paste markers never reach hera — not part of this change.
