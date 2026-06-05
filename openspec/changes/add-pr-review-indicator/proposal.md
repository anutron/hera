## Why

Hera's rail glyph table diverged from argus after SDK v0.0.6 introduced `IconReview` (clipboard-check) for the in-review state — hera still renders the moon-stars glyph argus no longer uses. Additionally, hera does not yet surface the per-task GitHub PR review state that argus's daemon polls and serves on the task DTO (`pr_state`), so operators have no in-rail signal when a PR is awaiting review or has changes requested.

## What Changes

- **SDK pin bumped to v0.0.6**: consumes `IconReview`, `IconPRAwaiting/Changes/Approved`, `StylePRAwaiting/Changes/Approved`, `ColorPRAwaiting/Changes/Approved` from `github.com/anutron/argus-sdk/theme`.
- **`stateGlyph` in_review mapping updated**: `"in_review"` now returns `theme.IconReview` (clipboard-check, U+0F00BC) instead of `theme.IconMoonStars`. `StyleInReview` is preserved.
- **PR indicator cell added to role rows**: a right-of-status-icon glyph cell renders the PR review state for roles whose argus task has an actionable PR state (`awaiting-review`, `changes-requested`, `approved`). Non-actionable states (none, draft, merged-closed, unknown) produce no cell; the name column reclaims the space. Backed by the existing `ArgusStateCache` — no per-row HTTP calls.
- **BUG-008 fixed**: sibling worker rows at the same depth as a sub-coordinator no longer render 1 column deeper (the leading " " prefix in `drawRoleRow` is removed so all depth-N rows start their icon at the same column, mirroring argus's row layout).

## Capabilities

**Modified Capabilities**:
- `hera-view` – icon vocabulary and rail row rendering requirements change.

## Impact

- `internal/argus/tasks.go`: `Task.PRState` field added.
- `internal/view/taskstate.go`: `ArgusTaskState.PRState`, `ArgusTaskInfo.PRState` fields added; cache poll propagates them.
- `internal/view/rail_list.go`: `stateGlyph` mapping, `roleEntry.PRState`, `drawRoleRow` PR cell + prefix removal.
- `internal/view/app.go`: `applyArgusState` propagates `PRState`.
- `go.mod`: SDK version bump.
