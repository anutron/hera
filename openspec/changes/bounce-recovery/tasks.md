# Tasks: Bounce Recovery

## Phase 1: Specs

- [x] Write proposal.md
- [x] Write design.md
- [x] Write delta spec: hera-substrate-link
- [x] Write delta spec: hera-coordination

## Phase 2: Implementation

- [ ] Create `internal/daemon/bounce_recovery.go` with `BounceRecoverer`
- [ ] Write `internal/daemon/bounce_recovery_test.go` with failing tests
- [ ] Implement `BounceRecoverer.ResumeWorkers` to pass tests
- [ ] Wire `BounceRecoverer` into `daemon.Start` (run.go)

## Phase 3: Verify

- [ ] `make build` passes
- [ ] `make test` passes
- [ ] `make vet` passes
- [ ] `make lint` passes
- [ ] `openspec validate --strict` passes
