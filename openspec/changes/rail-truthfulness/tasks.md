## 1. Spec

- [x] 1.1 Write proposal + design + hera-view deltas (icon-truth requirement ADDED; Freelance partition requirement MODIFIED); `openspec validate rail-truthfulness --strict` green

## 2. Tests first (TDD)

- [x] 2.1 Failing rail_list tests: archived/argus-archived/dead row with known state renders its true status glyph dimmed (`✓` for complete/in_review, `☾` working, `?` needs-input), never `○`; unknown-state archived row keeps the `○` dimmed fallback; coordinator-header icon obeys the same rule
- [x] 2.2 Failing app tests: a non-archived argus task whose only binding ENDED on a coordinator role appears as a named row in the Freelance section (the hera-1.0-ux-qa shape, verified against the live data's exact topology); a worker rendered via the latest-binding fallback does NOT duplicate into Freelance; live-binding tasks stay excluded; archived freelancers stay gated on showArchived

## 3. Implementation

- [x] 3.1 `statusIcon`: derive the glyph from the known state first; archived/dead dims the style instead of overriding the glyph; `○` dimmed only as the unknown-state archived fallback
- [x] 3.2 `populateRail`/`buildFreelance`: collect rendered role-row task ids; exclude from Freelance on live bindings (`Bindings.ListLive`) + rendered set; remove the orphaned `AllArgusTaskIDs` DAO
- [x] 3.3 Full `go test ./... -race -count=1` green; no regressions to collapse-empty-coords defaults, archive expando visibility, async mutations, selection marker

## 4. Ship

- [x] 4.1 Commit code + tests + change folder together on argus/fix-rail-truth; report via hera_send; hera_status done
