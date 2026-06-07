# Design: BUG-040

## Scope

Rename/drop only — no behavioral logic changes.

## DB migration

Migration 0007 uses the SQLite table-recreation pattern (same as 0003):

1. `CREATE TABLE roles_new` with `prompt TEXT NOT NULL DEFAULT ''` (no `constraints`)
2. `INSERT INTO roles_new ... SELECT ... mission AS prompt ... FROM roles` (preserves existing data)
3. `DROP TABLE roles`
4. `ALTER TABLE roles_new RENAME TO roles`
5. Recreate indexes (same as post-0003)

The `initialSchema` (migration 0001) is left unchanged — it is historical. Migration 0007 runs after it and produces the correct final shape.

## MCP surface changes

- `hera_new_orchestrator`: `mission?` + `constraints?` → `prompt?`
- `hera_join` (attach mode): `mission?` + `constraints?` → `prompt?`
- `hera_spawn_worker`: remove `mission?` (already had `prompt`)
- Output structs: remove `constraints`, rename `mission` → `prompt`

## View changes

- `coordDetails` struct: `Mission string` + `Constraints string` → `Prompt string`
- Details pane Draw: replace the two-section `Mission:` + `Constraints:` block with a single `Prompt:` section
