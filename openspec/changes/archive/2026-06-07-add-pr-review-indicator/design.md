## Approach

All changes are in `internal/view/rail_list.go`, `internal/view/taskstate.go`, `internal/view/app.go`, and `internal/argus/tasks.go`.

**SDK bump**: change `github.com/anutron/argus-sdk v0.0.4` to `v0.0.6` in `go.mod`; run `go mod tidy`.

**in_review glyph**: in `stateGlyph()`, change `case "in_review": return theme.IconMoonStars, ...` to `return theme.IconReview, ...`.

**PR state data path**: Add `PRState string \`json:"pr_state,omitempty"\`` to `argus.Task`. Add `PRState string` to `ArgusTaskState`. Propagate in `ArgusStateCache.poll()`. Add `PRState string` to `roleEntry`. Copy in `applyArgusState`.

**PR indicator cell**: In `drawRoleRow`, after the status icon block (col += 2), add:
```go
if prIcon, prStyle, ok := prGlyph(r.PRState); ok {
    screen.SetContent(col, y, prIcon, nil, prStyle)
    col += 2
}
```
where `prGlyph` is a package-local function that mirrors argus's `theme.PRGlyph` (mapping string → `theme.IconPRAwaiting/Changes/Approved` + `StylePR*`, ok=false for non-actionable). This avoids importing argus's model package.

**Prefix removal (BUG-008)**: Remove `prefix := " "; widget.DrawText(..., prefix, ...); col += runeLen(prefix)` from `drawRoleRow`. All depth-N rows now start their icon at `cx + N*indentStep`.
