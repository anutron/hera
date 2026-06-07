# Tasks: bug040-mission-to-prompt

- [ ] DB layer: rename `Mission`→`Prompt`, drop `Constraints` in `types.go`, `roles.go`, `schema.go`
- [ ] DB migration 0007: rename `mission`→`prompt` + DROP `constraints` column via table recreation
- [ ] Events layer: `MetaKeyMission`→`MetaKeyPrompt`, remove `MetaKeyConstraints`, update `adopt.go`
- [ ] MCP handlers: `handler_join.go`, `handler_new_orchestrator.go`, `handler_spawn_worker.go`
- [ ] Daemon MCP schemas: remove `mission`/`constraints` params, add `prompt` to `run.go`
- [ ] View ops types: rename `Mission`→`Prompt`, drop `Constraints` in `types.go`
- [ ] View ops new: `mission=""`→`prompt=""` in `buildBootstrapPrompt`
- [ ] View ops spawn_worker: `Mission:`→`Prompt:` in `CreateRoleInput`
- [ ] View coord_details: rename `Mission`→`Prompt`, drop `Constraints`, update `Draw`
- [ ] Tests: update all test files that reference `Mission`/`Constraints`/`mission`/`constraints`
- [ ] OpenSpec: validate change passes
- [ ] Docs: update README.md, NEXT.md, FOLLOWUPS.md
