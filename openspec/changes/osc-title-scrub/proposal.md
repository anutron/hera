# Proposal: osc-title-scrub

## Why

Claude Code emits OSC set-title sequences (`ESC ] 0 ; <title> BEL` / `ESC ] 2 ; <title> ST`) into its PTY stream. Hera's pane pipeline (ring snapshot + live SSE bytes → paneBridge → argus-sdk terminalpane emulator) passes those bytes to the emulator unfiltered, and the SDK emulator mishandles them — the session's title payload paints as ghost text at the input-prompt line in BOTH the coord and agent panes, looking like typed input that isn't in the real input buffer. Argus hit and fixed this exact bug in its own task terminal (`internal/tui/terminal/oscfilter.go`: a charmbracelet/x/ansi parser bug treats a 0x9C UTF-8 continuation byte — present in Claude's "✳" spinner title — as a C1 String Terminator, truncating the OSC and leaking the rest as printable text); hera never inherited the fix because the extracted argus-sdk terminalpane has no OSC filter.

## What Changes

- Port argus's streaming OSC filter (`oscFilter` state machine) into hera as `internal/view/osc_filter.go`, adapted so each filtered chunk owns its backing array (the pane bridge sends chunks onto an asynchronously-consumed channel).
- Apply the filter inside `pumpPaneBridge` (`internal/view/pane_bridge.go`) — the single chokepoint through which BOTH the ring snapshot and every live byte chunk flow into a pane's emulator. One stateful filter per bridge carries parser state across chunk boundaries, including the snapshot→live boundary.
- OSC sequences (any code: 0/1/2/8/…) are dropped entirely; all other escape sequences (CSI colors/cursor, etc.) pass through untouched.

## Capabilities

### New Capabilities

(none — the behavior lands in the existing `hera-view` capability)

### Modified Capabilities

- `hera-view`: new requirement — pane byte streams MUST be scrubbed of OSC sequences before reaching the pane emulator, including sequences split across chunk boundaries, with both BEL and 7-bit ST terminators handled and non-OSC escape sequences preserved.

## Impact

- **Code:** `internal/view/osc_filter.go` (new), `internal/view/pane_bridge.go` (pump applies the filter), tests alongside both.
- **Dependencies:** none — no argus-sdk change required (the filter sits upstream of the SDK emulator, mirroring how argus filters upstream of x/vt).
- **Out of scope:** a ring snapshot whose retention window begins mid-escape-sequence (argus's `AlignToEscBoundary` concern) — pre-existing for all sequence types and not part of the ghost-title leak.
