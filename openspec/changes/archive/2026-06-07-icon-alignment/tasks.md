## 1. Spec

- [x] 1.1 Write proposal + design + hera-view deltas (glyph-table requirement ADDED; rail-tree requirement's icon vocabulary MODIFIED); `openspec validate icon-alignment --strict` green

## 2. Tests first (TDD)

- [x] 2.1 Failing rail_list tests: table-driven `stateGlyph` test for every status/flag combo → expected rune + style (pending `○` gray, complete `✓` green, in_review `󰖔` blue, in_progress+needs-input `` #faa378, in_progress+idle `` blue, in_progress+running spinner orange); in_review MUST NOT render `✓`; needs-input outside in_progress defers to the status glyph; spinner frames cycle U+EE06..U+EE0B at 150 ms; archived rows keep the true glyph dimmed (updated expectations)
- [x] 2.2 Failing spinner-driver tests: rail reports a running row via `HasRunningRows` after `SetOrchestrators`/`SetFreelance`; no running rows → false

## 3. Implementation

- [x] 3.1 `stateGlyph`: rewrite to the argus table; thread an animation frame through `statusIcon`/`roleIcon`/`orchIcon`; wall-clock frame derivation off the rail's `now` source
- [x] 3.2 Spinner driver: 150 ms ticker in the App scheduling the redraw coalescer only while `HasRunningRows`; stopped in `Close`; `hasRunning` recomputed in `buildRows`
- [x] 3.3 Full `go test ./... -race -count=1` green; no regressions to rail-truthfulness state resolution, collapse defaults, marker gutter, wheel pan

## 4. Ship

- [x] 4.1 Commit code + tests + change folder together on argus/fix-icon-alignment; report via hera_send; hera_status done
