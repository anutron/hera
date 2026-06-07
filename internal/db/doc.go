// Package db owns hera's local SQLite state.
//
// Schema is defined in code with versioned migrations. Tables:
//
//   - orchestrators (id, name, created_at)
//   - roles         (id, orchestrator_id, name, kind, argus_project, prompt, created_at)
//   - bindings      (id, role_id, argus_task_id, worktree_path, started_at, ended_at, end_reason)
//   - messages      (id, from_role_id, to_role_id, body, in_reply_to, sent_at, read_at, delivery_mode, delivered_at)
//   - role_status   (role_id PK, status, updated_at)
//   - event_cursor  (id=1 singleton, last_seen_event_id)
//   - config        (key PK, value)
//
// WAL mode is enabled. See openspec/changes/hera-v1/design.md decision D8
// for rationale.
package db
