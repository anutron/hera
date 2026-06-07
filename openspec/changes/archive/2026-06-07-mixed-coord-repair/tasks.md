# Tasks

## 1. Spec

- [x] 1.1 Delta: mixed-coord repair cue + repair-first `a` (ADDED); `a` toggle carve-out + Freelance fallback dedupe (MODIFIED)

## 2. Tests (failing first)

- [x] 2.1 orchIcon: mixed-coord header (active orch + argus-archived coord task) renders `⊘` in error style
- [x] 2.2 orchIcon: archived orchestrator does NOT render the cue (dimmed-archived treatment wins)
- [x] 2.3 populateRail: coord task's argus `archived` bit lands on `orchEntry.CoordArgusArchived`
- [x] 2.4 OnArchive: mixed-coord header → task-direct unarchive of the coord task; NO cascade archive
- [x] 2.5 OnArchive: non-mixed active header still cascade-archives; archived header still unarchives
- [x] 2.6 buildFreelance: header-reachable coord task (CoordTaskID of a rendered header) excluded from Freelance
- [x] 2.7 buildFreelance: truly orphaned task (hera-archived coord role → header binds nothing) still falls back

## 3. Implementation

- [x] 3.1 `orchEntry.CoordArgusArchived` + populateRail capture; `orchIcon` repair cue
- [x] 3.2 `railSelection.CoordTaskID`/`CoordArgusArchived`; `CurrentRailSelection` carries them; `OnArchive` repair-first branch
- [x] 3.3 populateRail collects each entry's `CoordTaskID` into the rendered set for `buildFreelance`

## 4. Verify

- [x] 4.1 `go test ./... -race -count=1` green
- [x] 4.2 `openspec validate --all --strict` green

## 5. Shared-task guard (cascade collateral fix)

- [x] 5.1 Delta: archive via ended binding skips argus when the task is live-bound to another role (MODIFIED `a` toggle requirement + scenarios)
- [x] 5.2 Tests (failing first): ended binding to live-bound-elsewhere task skips argus archive + logs both role names; own-live-binding archives even when shared; ended binding with no other live binding archives as today; orchestrator cascade inherits the guard
- [x] 5.3 `ops.DB.ListLiveBindingsByTask` (wraps `BindingsDAO.ListLiveByTaskID`); guard in `ArchiveRole`'s ended-fallback path; `ToggleArchiveTask` exempt (freelance rows are rail-excluded whenever any live binding exists)
- [x] 5.4 `go test ./... -race -count=1` green; `openspec validate --all --strict` green
