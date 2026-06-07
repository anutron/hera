# Design: osc-title-scrub

## Context

- **Symptom:** the Claude session's title text renders inside both panes at the input-prompt line as ghost input — not in the real input buffer (typing overwrites it; Enter does nothing).
- **Root cause chain:** Claude Code emits `ESC ] 0 ; <title> BEL` (and ST-terminated variants). Hera's pipeline (proxy ring snapshot + SSE live chunks → `pumpPaneBridge` → argus-sdk `terminalpane` emulator) feeds those bytes to the emulator unfiltered. The SDK emulator inherits the charmbracelet/x/ansi parser bug argus documented: a `0x9C` UTF-8 continuation byte inside the OSC payload (Claude's `✳` spinner glyph encodes one) is treated as C1 ST, truncating the OSC and rendering the rest of the title as ground text.
- **Prior art:** argus fixed this with `internal/tui/terminal/oscfilter.go` — a streaming state machine that strips OSC sequences before its x/vt emulator, applied statefully on the live feed (`tp.oscStrip.filter(...)`) and one-shot on whole-buffer replays (`FilterOSC(AlignToEscBoundary(raw))`).

## Goals / Non-Goals

- **Goal:** no OSC payload ever paints in a hera pane; sequences split across chunks (snapshot→live included) are removed; legitimate escapes untouched.
- **Non-goal:** fixing the SDK emulator's parser (upstream); handling a ring snapshot whose retention window starts mid-sequence (pre-existing for all escape types, separate concern).

## Decisions

- **Port, don't depend:** copy argus's `oscFilter` state machine into `internal/view/osc_filter.go` rather than importing argus internals (unimportable) or waiting on an argus-sdk release. The state machine is ~100 lines and frozen behavior; mirroring argus's exact semantics (BEL/ST/CAN/SUB terminators, 0x9C deliberately NOT a terminator, 64 KiB runaway cap, deferred lone ESC) keeps the two implementations reasoning-compatible.
- **Filter in the pump:** apply one stateful `oscFilter` per `paneBridge` inside `pumpPaneBridge`, filtering the snapshot and every upstream chunk through the same instance. The pump is the single chokepoint feeding `terminalpane.New(bridge.out)`; filtering there covers bind-time snapshot, live SSE bytes, and the snapshot→live boundary with zero changes to proxy or SDK. Per-bridge state also means a fresh pane (rebind) starts at a clean parser state, mirroring argus's `oscStrip.reset()` on emulator recreation.
- **Chunk ownership:** argus's `filter()` returns a slice reused across calls (safe there: synchronous hand-off to the emulator). Hera's pump sends chunks onto a buffered channel consumed by another goroutine, so the ported filter MUST return a fresh slice per call (allocate per chunk, no shared `buf`).
- **No mid-stream flush:** a lone trailing ESC at a chunk boundary stays deferred until the next chunk (flushing it would emit the ESC before knowing whether it opens an OSC). On stream end the deferred ESC is dropped — a dangling ESC paints nothing and the stream is over.

## Risks / Trade-offs

- **OSC-8 hyperlinks lose their URL association** (link text still paints — it's ordinary ground text). Same trade-off argus accepted; titles and hyperlink targets are invisible in an embedded pane anyway.
- **Per-chunk allocation** in the pump: chunks are ~2–4 KiB at human-readable rates; negligible.
