# Design

## The authoritative table (argus source)

Extracted from argus `internal/tui/theme/theme.go:29-34` + `internal/tui/taskview/tasklist.go:1095-1132`:

| State | Glyph | Color |
|---|---|---|
| pending | `○` U+25CB | gray (`ColorPending`, 245) |
| complete | `✓` U+2713 | green (`ColorComplete`, 78) |
| in_review | `󰖔` U+F0594 (nf-md-weather_night) | blue (`ColorInReview`, 81) |
| in_progress + needs_input | `` U+F059 | #faa378 (`ColorNeedsInput`) — highest priority within in_progress |
| in_progress + idle | `` U+F186 (nf-fa-moon_o) | blue (`ColorInReview`) |
| in_progress + running | spinner U+EE06..U+EE0B animated | orange (`ColorInProgress`, 214) |

All colors come from the SDK theme (`github.com/anutron/argus-sdk/theme`), which carries argus's exact values — no hex mirroring needed.

## Decisions

- **Spinner: animated, wall-clock-derived.** Frame = `unixMillis / 150ms mod 6` (argus's progress-spinner cadence), computed at draw time from the rail's overridable `now` source so tests are deterministic. A spinner driver (150 ms ticker in the App) calls `redraw.Schedule()` only while the rail contains a running row — mirroring argus's `spinnerLoop` ("tick only when there's live work"), reusing the existing coalescer instead of a second draw path. The rail exposes an `atomic.Bool` (`hasRunning`, recomputed in `buildRows` on the event loop) so the ticker goroutine reads race-free.
- **needs-input scoped to in_progress.** Argus checks `needsInput` only inside the `StatusInProgress` case; hera previously checked it before the status switch. The API only serves `needs_input` for `in_progress` (omitempty), so behavior is identical in practice, but the switch now mirrors argus's nesting exactly.
- **idleUnvisited not mirrored.** Argus's moon-stars for unvisited-idle is TUI-local state (visited tracking), not in the API. Hera renders plain moon `` U+F186 for all `in_progress`+idle.
- **Rail-truthfulness preserved.** `statusIcon`'s contract is unchanged: archive/dead state modulates only STYLE (dimmed), never the GLYPH; `○` dimmed remains the unknown-state archived fallback; binding-presence fallback icons for unknown state are untouched. A running archived row renders the spinner dimmed.
