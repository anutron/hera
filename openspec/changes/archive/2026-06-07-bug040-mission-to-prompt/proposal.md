# Proposal: BUG-040 – role free-form fields → single `prompt` (argus parity)

## Problem

A hera role currently carries two free-form text fields: `mission` and `constraints`. Argus tasks have a single `prompt`. This mismatch means:

- Coordinators and workers need to fill two fields where one is sufficient.
- The MCP surface (`hera_new_orchestrator`, `hera_join`, `hera_spawn_worker`) is wider than necessary.
- The Details pane in hera-view renders both "Mission:" and "Constraints:" sections, adding visual noise.
- The DB schema diverges from argus's single-prompt model.

## Solution

Consolidate to a single `prompt` field, mirroring argus:

- Rename `mission` → `prompt` everywhere (DB column, struct fields, MCP params, Details pane label).
- Drop `constraints` entirely (DB column, struct fields, MCP params, Details pane label).
- Write DB migration 0007 that does the rename/drop while preserving existing `mission` values in `prompt`.
- Update the Details pane to show "Prompt:" only.

This is a pure rename/collapse — no behavioral logic changes, only surface renaming.
