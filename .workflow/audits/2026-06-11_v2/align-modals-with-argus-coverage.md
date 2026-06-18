# Spec-coverage audit: align-modals-with-argus (hera-view)

- **Date:** 2026-06-11
- **Scope:** the change's new/changed behavior in `internal/view/modals.go`, `internal/view/mutations.go`, `internal/view/ops/spawn_worker.go`
- **Contract:** pending delta `openspec/changes/align-modals-with-argus/specs/hera-view/spec.md`, base `openspec/specs/hera-view/spec.md`, `design.md`
- **Verdict:** CLEAN — no verified behavioral gaps or contradictions in scope.

## Counts

- Behavioral branches COVERED: 16
- UNCOVERED-BEHAVIORAL (gaps): 0
- CONTRADICTS: 0
- Unimplemented delta promises: 0
- UNCOVERED-IMPLEMENTATION (no spec needed): 6 (noted, not gaps)

## Code → spec classification

### inlineListSelect widget (modals.go)

1. **Renders as list with `>` cursor + `(N/M)` counter, not a cycler** — `Draw` rows 0..N, `filteredCounter()`, `cursorMark="> "` (modals.go:748–833). COVERED — "Project selectors render…" requirement + scenario "Project selector renders as a list, not a cycler": *"The list SHALL render a `>` cursor on the highlighted option and an `(N/M)` counter."*

2. **Down/Up move cursor within filtered options without submitting; clamped (no wrap)** — `InputHandler` KeyDown/KeyUp guard `cursor < len(filtered)-1` / `cursor > 0` (modals.go:842–851). COVERED — scenario "Down moves the cursor without submitting"; design Decision "list + type-to-filter behavior" documents clamp/no-wrap as intended.

3. **Printable rune appends to filter, narrows by case-insensitive substring, resets cursor to first match** — KeyRune branch + `refilter()` lowercasing + `strings.Contains`, `cursor=0` (modals.go:852–860, 660–670). COVERED — scenario "Typing filters the visible options": *"narrow the visible options to those whose label contains the filter text (case-insensitive substring), resetting the cursor to the first filtered option."*

4. **Backspace deletes last filter rune; empty filter restores full list** — KeyBackspace branch trims one rune + `refilter()` (empty needle matches all) (modals.go:861–866, 664). COVERED — scenario "Clearing the filter restores the full list."

5. **Enter locks selection + advances focus (KeyTab), does NOT submit** — KeyEnter → `finishedFunc(KeyTab)` (modals.go:867–870) plus `enterRoutingCapture` `case *inlineListSelect: return ev` (modals.go:254–257). COVERED — scenario "Enter locks the selection and advances focus."

6. **Tab/Backtab advance/retreat focus; Esc cancels** — modals.go:871–883. COVERED — requirement body: *"Tab and Backtab MUST advance and retreat focus; Esc MUST cancel the modal."*

7. **Empty project list → single `(no projects configured)` entry mapping to "" on confirm** — `newInlineListSelect` sentinel fill (modals.go:632–635); `GetCurrentOption` maps sentinel → `(0, "")` (modals.go:706–708); ops falls back to coord project (spawn_worker.go:70–73). COVERED — scenario "Empty project list degrades to a single fallback entry" + delta "effective project" paragraph + scenario "Empty project list degrades to the coordinator's project."

8. **GetCurrentOption returns position within filtered set + highlighted label** — modals.go:701–710. COVERED — requirement: *"the `(N/M)` counter (cursor position within the currently filtered options / count of filtered options)."*

### Backend stays a cycler (modals.go)

9. **Backend uses `inlineCycler` (`◄ backend ►`) in both modals** — `newInlineCycler` for Backend in ShowNewCoordForm (modals.go:926) and ShowNewWorkerForm (modals.go:1033). COVERED — requirement: *"The Backend selector MUST remain a `◄ backend ►` cycler in both modals"* + scenario "Backend stays a cycler."

### Paste-readiness (modals.go)

10. **List widget consumes input only while focused; paste to a focused text field is not intercepted** — `inlineListSelect.InputHandler` is wrapped by `WrapInputHandler` (fires only when the widget holds focus); native `styledTextArea`/`styledInputField` PasteHandler intact (no override of paste). COVERED — requirement "Spawn modals are paste-ready" + scenario "Paste lands in the focused prompt field as one chunk"; design `## BUG-004` documents the single-chunk path. NOTE: end-to-end transport paste is an explicitly DEFERRED non-goal (design Non-Goals + Cross-repo follow-ups) — its absence is documented current state, not a gap.

### Worker spawn: Project / Branch / Backend threading (mutations.go + spawn_worker.go)

11. **`w` opens modal with Project list + Branch + Backend + Prompt** — ShowNewWorkerForm builds all four fields (modals.go:1017–1043); `OnNewWorker` calls it with backends loaded (mutations.go:663–677). COVERED — MODIFIED requirement + scenario "`w` in RAIL opens the spawn-worker modal on a coordinator."

12. **Project selector defaults to coordinator's project** — `defaultIdx` = index of coordProj in projects, passed as `defaultProjectIdx` (mutations.go:668–677), used as initial cursor (modals.go:1021, 631–654). COVERED — scenario "Project selector defaults to the coordinator's project."

13. **Selection resolves to a target coordinator (orchestrator/sub-coord/agent/archived-dead → coord; freelance/separator → not-applicable notice)** — resolution switch in `OnNewWorker` (mutations.go:608–644). COVERED — MODIFIED requirement resolution paragraph + scenarios "resolves an agent selection," "resolves an archived or dead agent row," "non-coordinator selection gives feedback." (Pre-existing resolution logic, unchanged by this change; still correctly threaded.)

14. **Untouched selector + chosen project both forwarded to POST /api/tasks; meta:role=worker mirrored; role.argus_project set to effective project** — `effectiveProject` override-or-fallback (spawn_worker.go:70–76); `CreateTaskRequest.Project` + `Meta:{"role":"worker"}` (spawn_worker.go:96–102); `CreateRole.ArgusProject` (spawn_worker.go:122–128). COVERED — scenarios "Untouched selector spawns in the coordinator's project" and "Selecting a project in the list spawns in the chosen project."

15. **Chosen Branch and Backend forwarded to the spawn** — `OnNewWorker` threads `branch`/`backend` into `SpawnWorkerInput` (mutations.go:689–691); `SpawnWorker` sets `CreateTaskRequest.Branch`/`.Backend` (trimmed) (spawn_worker.go:100–101). COVERED — scenario "Chosen Branch and Backend are forwarded to the spawn."

16. **Empty Branch → unset, worker branches off project default ref** — `strings.TrimSpace(in.Branch)` yields "" for empty Branch field (default empty per modals.go:1025–1027); request carries empty Branch (spawn_worker.go:100). COVERED — scenario "Empty Branch branches off the project default ref" + requirement: *"empty Branch uses the effective project's default ref."*

### UNCOVERED-IMPLEMENTATION (internal detail; correctly NOT spec'd)

- `listSelectVisibleRows = 5` scroll-window size and `scrollToCursor` math (modals.go:560–563, 672–684) — draw/scroll detail. Design explicitly says "a bounded number of list rows."
- Modal height constants `newCoordModalHeight`, `newWorkerModalHeight` (modals.go:980, 1080) — layout arithmetic; design Decision "multi-row field height + taller modals" covers intent without pinning numbers.
- `formFieldWidth` clamp and `(N/M)` blurred-vs-focused header rendering (modals.go:46–64, 781–798) — draw detail.
- Backend empty-list `["claude"]` default + `defaultBackendIdx=0` (modals.go:922–926, 1029–1033; mutations.go:677) — design Cross-repo follow-up explicitly documents "first configured backend, not per-project default" as accepted current state; MODIFIED requirement says so verbatim. Matching code = COVERED-as-documented, never a contradiction.
- `enterRoutingCapture` `*styledTextArea` plain-Enter-submits / modified-Enter-newline branch (modals.go:258–264) — BUG-011 behavior, governed by base spec, unchanged.
- ListBackends load-failure abort + ShowError path (mutations.go:663–667) — defensive error surfacing; consistent with off-loop mutation contract.

## Spec → code (delta promises, all implemented)

Every scenario in the pending delta has corresponding scoped code:

- Project-list-select requirement (7 scenarios) → inlineListSelect (#1–#8 above).
- Paste-ready requirement (1 scenario) → list non-interception + intact native PasteHandler (#10).
- MODIFIED `w` requirement (10 scenarios) → OnNewWorker resolution + ShowNewWorkerForm + SpawnWorker threading (#11–#16). The two pre-existing partial-failure scenarios (GetTask-fails-still-binds, role/binding-insert-fails-no-rollback) are implemented at spawn_worker.go:108–143 and unchanged by this change — still satisfied.

No unimplemented promise found.

## Contradiction sweep (none)

- Backend = first configured backend, not per-project default: matches MODIFIED requirement's explicit "a per-project default backend is not yet plumbed" clause and design Cross-repo follow-up → documented current state, COVERED (not a contradiction per the spec-misreading guard).
- End-to-end paste speed not guaranteed: design Non-Goal + the paste requirement scopes itself to "hera's modal-side handling only" → documented deferral, not a gap.
- ShowNewCoordForm Branch defaults to `"origin/main"` while ShowNewWorkerForm Branch defaults empty: the delta governs the WORKER modal's empty default ("Branch field SHALL default empty"); the coord form's `origin/main` prefill is pre-existing base behavior outside this delta's worker-default scenarios — no contradiction with any worker scenario.
