# Tasks: hera project discovery

## Phase 1: Specs

- [x] Write proposal.md
- [x] Write design.md
- [x] Write delta spec: hera-coordination

## Phase 2: Implementation (TDD)

- [x] Add `argus.Project` type + `ListProjectsFull` (GET `/api/projects/full`) in `internal/argus/projects.go`
- [x] Write failing client tests: `ListProjectsFull` parses name/branch/backend; `ListProjects` derives names from `/api/projects/full`
- [x] Refactor `argus.Client.ListProjects` to derive names from `ListProjectsFull`
- [x] Add `/api/projects/full` route to the mcp handler-test fake (`fakeArgusForHandlers`)
- [x] Write failing handler tests: unknown project lists valid names; known project passes; list-fetch error falls through; `hera_projects` returns configured projects
- [x] Implement up-front project validation in `internal/mcp/handler_spawn_worker.go`
- [x] Implement `ProjectsHandler` in `internal/mcp/handler_projects.go`
- [x] Register `hera_projects` handler + tool definition in `internal/daemon/run.go`

## Phase 3: Verify

- [x] `make build` passes
- [x] `make test` passes (pre-existing 1Password git-signing failures in internal/view/ops did not manifest this run; suite fully green)
- [x] `make vet` passes
- [x] `make lint` passes (0 issues)
- [x] `openspec validate add-hera-project-discovery --strict` passes
