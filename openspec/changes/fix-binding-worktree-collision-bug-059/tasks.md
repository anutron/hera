# Tasks

## 1. DAO — worktree-keyed lookups

- [x] 1.1 Add `BindingsDAO.GetLiveByWorktreeAndOrchestrator(ctx, worktreePath, orchID)` in `internal/db/bindings.go` — worktree-keyed twin of `GetLiveByTaskAndOrchestrator`, mapping onto `bindings_live_unique_worktree_orch`.
- [x] 1.2 Add `BindingsDAO.ListLiveByWorktree(ctx, worktreePath)` — worktree-keyed twin of `ListLiveByTaskID`.

## 2. Resolver — disambiguation + fallback

- [x] 2.1 `TaskForCwd` collects all worktree matches and disambiguates: single match → return; else drop archived; single active → return; else prefer the single `in_progress` task; else `CwdAmbiguousError`.
- [x] 2.2 Add `CwdAmbiguousError` listing the candidate tasks.
- [x] 2.3 Add `LiveBindingForOrch` / `LiveBindingsForTask` (task-keyed first, worktree-keyed fallback) and route `CallerRole` through them.

## 3. Handlers — claim/attach/bootstrap agreement

- [x] 3.1 `hera_join` claim (both the explicit-orchestrator and no-orchestrator paths) resolves through the resolver fallback.
- [x] 3.2 `hera_join` attach guards on the worktree-keyed binding and returns an actionable message (claim / `hera_rebind`) instead of a raw `UNIQUE constraint failed`.
- [x] 3.3 `hera_new_orchestrator` bootstrap guards on the worktree-keyed binding too.

## 4. `hera_rebind` verb

- [x] 4.1 New `internal/mcp/handler_rebind.go`: reconcile a stuck binding to the caller's real live task; end + recreate under the same role (preserving prompt/messages/status); refuse genuinely ambiguous states.
- [x] 4.2 Register `hera_rebind` handler + tool definition in `internal/daemon/run.go`; update the registration contract in `internal/daemon/run_test.go`.

## 5. Tests

- [x] 5.1 `internal/db/bindings_worktree_test.go`: worktree lookups (happy / not-found / orch-scoped / excludes-ended / multi-orch) and a DAO-level reproduction of the claim-vs-attach disagreement (task-keyed miss + worktree-keyed UNIQUE reject + worktree-keyed reconciliation).
- [x] 5.2 `internal/mcp/binding_collision_test.go`: `TaskForCwd` disambiguation (prefers in_progress, refuses two-in_progress, all-archived unknown, single-match unchanged) and `hera_join` claim/attach agreement across a stale-task collision.
- [x] 5.3 `internal/mcp/handler_rebind_test.go`: happy repair (post-state agreement + role/message survival + meta mirror), no-op-when-consistent, role_name selection, and refusals (ambiguous cwd, multi-role without role_name, nothing to reconcile, unknown orchestrator).
- [x] 5.4 `go test ./...` green (modulo the sandbox's 1Password commit-signing hook, which makes unrelated `internal/view/ops` git-commit tests fail; verified green with `GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null`).
