package view

// maxOSCDropBytes bounds how many bytes a single OSC sequence may consume
// before the filter gives up and resumes passing bytes through. Real OSC
// sequences (window titles, hyperlinks) are short; the cap only guards against
// a pathological stream whose OSC never sends a terminator we recognize, so
// the filter can never drop unboundedly.
const maxOSCDropBytes = 64 * 1024

type oscFilterState uint8

const (
	oscNormal      oscFilterState = iota // outside any sequence
	oscEscPending                        // saw ESC at top level, awaiting next byte
	oscInString                          // inside an OSC payload, dropping bytes
	oscInStringEsc                       // inside an OSC payload, saw ESC (maybe 7-bit ST)
)

// oscFilter is a streaming filter that removes OSC sequences (`ESC ] … ST`)
// from a PTY byte stream before it reaches the pane emulator. Ported from
// argus's internal/tui/terminal/oscfilter.go, which works around an upstream
// parser bug in charmbracelet/x/ansi (StringState): a 0x9C byte inside an OSC
// string is treated as a C1 String Terminator even when it is a UTF-8
// continuation byte. Claude Code's spinner title "✳ …" (✳ = E2 9C B3) hits
// this — the parser truncates the OSC at the 0x9C and renders the rest of the
// title as printable ground text, which is the ghost "typed input" operators
// saw at the prompt line of hera's coord/agent panes.
//
// Dropping OSC entirely is safe for an embedded pane: window titles are never
// displayed, and OSC-8 hyperlink *text* is ordinary ground text that survives
// (only the invisible URL association is dropped). The filter is stateful so
// OSC sequences split across chunks — including the snapshot→live boundary in
// pumpPaneBridge — are handled.
//
// Crucially, 0x9C is deliberately NOT treated as a terminator here — that is
// the exact upstream behavior being worked around. OSC strings are terminated
// only by BEL or 7-bit ST (`ESC \`) and cancelled by CAN/SUB or a fresh ESC
// sequence, matching how a UTF-8-mode terminal behaves.
//
// Unlike argus's variant (which reuses an internal buffer across calls for a
// synchronous hand-off to its emulator), filter returns a FRESH slice per
// call: pumpPaneBridge sends each filtered chunk onto a channel consumed by
// another goroutine, so every chunk must own its backing array.
type oscFilter struct {
	state   oscFilterState
	dropped int
}

// filter returns `in` with OSC sequences removed, carrying parser state across
// calls so a sequence split between chunks is still stripped. The returned
// slice owns its backing array and is safe to hand to another goroutine. A
// lone trailing ESC is held (not emitted) until the next chunk decides whether
// it introduces an OSC; on stream end a deferred ESC is simply dropped — a
// dangling ESC paints nothing.
func (f *oscFilter) filter(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		b := in[i]
		switch f.state {
		case oscNormal:
			if b == 0x1b {
				f.state = oscEscPending
			} else {
				out = append(out, b)
			}
		case oscEscPending:
			if b == ']' {
				f.state = oscInString
				f.dropped = 0
			} else {
				// Not an OSC introducer: emit the deferred ESC and reprocess
				// this byte from oscNormal (it may begin another sequence,
				// e.g. CSI `ESC [`).
				out = append(out, 0x1b)
				f.state = oscNormal
				i--
			}
		case oscInString:
			f.dropped++
			switch {
			case b == 0x07: // BEL terminates the OSC.
				f.state = oscNormal
			case b == 0x1b: // possible 7-bit ST (`ESC \`) or a fresh sequence.
				f.state = oscInStringEsc
			case b == 0x18 || b == 0x1a: // CAN/SUB cancel the OSC.
				f.state = oscNormal
			case f.dropped > maxOSCDropBytes:
				// Runaway guard: an OSC with no terminator we recognize. Stop
				// dropping and re-emit this byte so the filter can never hang.
				f.state = oscNormal
				out = append(out, b)
			default:
				// Drop the payload byte. 0x9C is intentionally NOT treated as
				// a terminator — that is the upstream bug being worked around.
			}
		case oscInStringEsc:
			if b == '\\' {
				// 7-bit ST: end of OSC; drop the trailing backslash too.
				f.state = oscNormal
			} else {
				// The ESC begins a new sequence, cancelling the OSC. Defer the
				// ESC and reprocess this byte (handles back-to-back `ESC ]`).
				f.state = oscEscPending
				i--
			}
		}
	}
	return out
}
