## 1. Spec

- [x] 1.1 Write proposal + design + hera-view delta (in_review glyph MODIFIED; PR indicator cell ADDED; row base indent BUGFIX); `openspec validate add-pr-review-indicator --strict` green

## 2. Tests

- [x] 2.1 `stateGlyph` test: `in_review` → `theme.IconReview` (not `theme.IconMoonStars`)
- [x] 2.2 PR indicator cell tests: actionable states render glyph + consume 2 cols; non-actionable states omit cell (name reclaims space)
- [x] 2.3 BUG-008 indent test: sibling worker at depth N renders icon at same column as sub-coordinator at depth N; sibling must NOT be at depth N+1

## 3. Implementation

- [x] 3.1 `go.mod`: bump `github.com/anutron/argus-sdk` to `v0.0.6`; `go mod tidy`
- [x] 3.2 `stateGlyph`: `"in_review"` → `theme.IconReview`
- [x] 3.3 `argus.Task`: add `PRState string \`json:"pr_state,omitempty"\``
- [x] 3.4 `ArgusTaskState`: add `PRState string`; `ArgusStateCache.poll()`: capture `t.PRState`
- [x] 3.5 `roleEntry`: add `PRState string`; `applyArgusState`: copy `st.PRState → r.PRState`
- [x] 3.6 `drawRoleRow`: remove " " prefix; add PR indicator cell after status icon
- [x] 3.7 `go test ./... -race -count=1` green; `golangci-lint run` clean; `make build` + `make vet` pass

## 4. Ship

- [ ] 4.1 Commit code + tests + change folder together; push via iris; open PR; report via hera_send; hera_status done
