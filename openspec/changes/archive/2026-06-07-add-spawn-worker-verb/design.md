# Design: hera_spawn_worker

## Verb signature

```
hera_spawn_worker(cwd, orchestrator?, role_name?, mission?, prompt, project?, branch?, backend?)
```

- `cwd` – required. Caller's worktree path. Must resolve to an argus task with a live coordinator binding.
- `orchestrator` – optional. Disambiguates when the calling task holds multiple live bindings.
- `role_name` – optional. If omitted, derived from the first 40 chars of `prompt` via slug normalization (same algorithm as the rail's `w` key).
- `mission` – optional. Free-form prose stored on the worker role.
- `prompt` – required. The full task prompt delivered to the new session. An orientation prefix (`You are a worker agent under coordinator "<coord>". You may report progress via hera_send.`) is prepended automatically.
- `project` – optional. Override the coordinator's `argus_project`. Defaults to the coordinator's project.
- `branch` – optional. Branch passed to argus `CreateTask`. Defaults to the project default.
- `backend` – optional. Backend passed to argus `CreateTask`. Defaults to the project default.

## Steps (handler)

1. `LinkGate` check.
2. Validate: `cwd` required, `prompt` non-empty.
3. `CallerRole(cwd, orchestrator)` → `(task, role, binding)`. Reject if `role.Kind != coordinator`.
4. Determine project: `input.Project` if set, else `role.ArgusProject`. Reject if empty.
5. Load orchestrator by ID (to get name for the orientation prefix in output).
6. Derive worker role name: `input.RoleName` if set; else `deriveWorkerName(input.Prompt)`.
7. Unique-ify role name: scan non-archived sibling roles and append `-2`, `-3`, … until a free slot is found.
8. Build task prompt: orientation prefix + user's prompt (verbatim).
9. `argus.CreateTask({Project, Prompt: taskPrompt, Branch, Backend}, meta{"role":"worker"})`.
10. `argus.GetTask(taskID)` → `worktreePath` (soft-fail: empty path on error).
11. `db.Roles.Create({OrchestratorID, Name: uniqueName, Kind: worker, ArgusProject: project, Mission})`.
12. `db.Bindings.Create({RoleID, OrchestratorID, ArgusTaskID, WorktreePath})`.
13. `client.PutTaskMeta(ctx, taskID, MetaKeyRole, "worker")` – best-effort.
14. `client.PostTaskInput(ctx, taskID, []byte("\r"))` – best-effort; `prompt_auto_submitted` bool in response reflects outcome.
15. Return `SpawnWorkerOutput`.

## Partial-failure handling

- If `GetTask` fails after `CreateTask` succeeds: binding inserted with empty `worktree_path`; logged but not propagated (same as `ops.SpawnWorker` D6/D7).
- If `CreateRole` or `CreateBinding` fail after `CreateTask` succeeds: argus task is orphaned (visible as freelancer). Error returned; task NOT deleted.
- If `PutTaskMeta` fails: non-fatal; binding stands.
- If `PostTaskInput` fails: non-fatal; `prompt_auto_submitted: false` in response; worker prompt must be manually submitted.

## Tool count decision update

NEXT.md's "Six MCP tools, no more no less" was a v1 decision. This adds a sanctioned 7th verb for v1.x. The base spec and the locked-decision comment in `toolDefinitions()` are updated to reflect this.

## Name derivation

`deriveWorkerName` and `uniqueWorkerName` are duplicated from `internal/view/ops/spawn_worker.go` into the MCP handler to avoid a new cross-package dependency. The algorithm is identical: lowercase slug from first 40 chars, uniqued with `-2`/`-3` suffix among active sibling roles.
