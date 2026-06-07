# Tasks: osc-title-scrub

**Design doc:** openspec/changes/osc-title-scrub/design.md

## 1. Failing tests (TDD)

- [x] 1.1 `internal/view/osc_filter_test.go` — table tests for the scrubber: inline OSC+BEL removed, OSC+ST removed, payload split across two/three chunks removed, terminator `ESC`/`\` split across chunks, snapshot→live boundary split, 0x9C payload byte not a terminator, CAN/SUB cancel, ESC-cancels-OSC reprocessed, SGR/CSI sequences pass through byte-for-byte, runaway cap, lone trailing ESC deferred, fresh-slice-per-call ownership
- [x] 1.2 `internal/view/pane_bridge_test.go` — pump integration: bridge fed a snapshot containing an OSC title + live chunks splitting a second OSC across the boundary; assert bytes received on `bridge.out` exclude both sequences while surrounding text + SGR codes survive
- [x] 1.3 Confirm tests fail (Prove-It) before implementation

## 2. Implementation

**Depends on:** Stage 1

- [x] 2.1 `internal/view/osc_filter.go` — port argus's `oscFilter` state machine (BEL/ST terminators, CAN/SUB cancel, 0x9C not a terminator, 64 KiB runaway cap), adapted to return a fresh slice per call
- [x] 2.2 `internal/view/pane_bridge.go` — `pumpPaneBridge` filters snapshot and every upstream chunk through one per-bridge stateful filter before sending on `out`; empty post-filter chunks are not sent

## 3. Verification

**Depends on:** Stage 2

- [x] 3.1 `go test ./... -race -count=1` fully green
- [x] 3.2 `openspec validate --all --strict` green
- [x] 3.3 Commit `Fix: scrub OSC title sequences from pane streams (ghost-input leak)`; report via hera_send + hera_status
