package view

import (
	"context"
	"log/slog"

	"github.com/coder/websocket"

	"github.com/anutron/argus-sdk/pluginview"
	"github.com/anutron/argus-sdk/terminalpane"
)

// rawInputConn wraps the plugin-view WebSocket connection so pane-focus
// keystrokes are forwarded to the bound task's PTY as RAW BYTES, verbatim,
// BEFORE the tcell input parser ever sees them.
//
// Why this layer exists: argus (the host) has already encoded each keystroke
// into the exact byte sequence a real terminal would emit (it shares one
// encoder — internal/tui/keyenc — across its own agent terminal and the plugin
// panes). If hera let those bytes flow into pluginview's wsTty and through
// tcell's parser, tcell would re-parse and hera would re-encode via the lossy
// encodeEventForPTY — a double round-trip that SWALLOWS some keys entirely
// (Shift+Enter = ESC CR, Alt+Backspace = ESC DEL) and mangles others
// (Alt+arrows / word-motion). Forwarding the raw bytes straight to the PTY when
// a pane is focused preserves full terminal fidelity and matches what argus's
// own panes do.
//
// Routing by focus state (read via the thread-safe focusReader since Read runs
// on pluginview's read goroutine, not the tview event loop):
//
//   - RAIL focus: pass the frame to tcell unchanged — rail navigation and
//     mutation keys (j/k/n/r/a/…) parse as before.
//   - COORD/AGENT focus: forward the raw bytes to the bound task and SWALLOW
//     them from the parser (return empty) so they are never double-handled —
//     EXCEPT the view-owned control chords (Ctrl-Q, the Cmd/Ctrl-←/→ focus
//     ladder, the Ctrl-↑/↓ in-pane nav, the Shift-↑/↓ scroll), which are passed
//     to tcell so KeyRouter handles them even from inside a pane.
//
// SGR mouse frames (argus's plugin-pane wheel forwarding) are view-owned in
// EVERY focus state and are peeled off BEFORE the focus fork: forwarding one
// would type escape garbage into the bound agent's PTY, and parsing one would
// fire garbage rail keys. Wheel ticks route to the WheelRouter (which hops to
// the tview event loop for positional hit-testing); every other mouse event
// (click, drag, motion, release) is swallowed. Detection is frame-aligned —
// argus sends one event per binary frame — so a frame counts as mouse only
// when it is exactly one well-formed SGR sequence; anything else follows the
// focus routing unchanged.
//
// Only Read is overridden; Write (surface frames) and Close delegate to the
// embedded conn.
type rawInputConn struct {
	pluginview.Conn

	focus   focusReader
	targets PaneTargets
	fwd     PaneByteForwarder
	wheel   WheelRouter
	log     *slog.Logger
}

// WheelRouter routes a decoded wheel tick to the view's scroll handling. The
// call arrives on pluginview's read goroutine; implementations must bounce to
// the tview event loop themselves (*App queues applyWheel via
// QueueUpdateDraw). Coordinates are the SGR-encoded 1-based viewport cell.
type WheelRouter interface {
	RouteWheel(up bool, x, y int)
}

// focusReader exposes the current focus state in a goroutine-safe way. *App
// satisfies it via an atomic mirror updated in OnFocusChanged; tests inject a
// fake. (FocusMachine itself is single-threaded by contract, so it must not be
// read from the read goroutine directly.)
type focusReader interface {
	CurrentFocus() FocusState
}

// newRawInputConn wraps conn. A nil log falls back to slog.Default(); a nil
// wheel router swallows wheel frames (they are still never forwarded/parsed).
func newRawInputConn(conn pluginview.Conn, focus focusReader, targets PaneTargets, fwd PaneByteForwarder, wheel WheelRouter, log *slog.Logger) *rawInputConn {
	if log == nil {
		log = slog.Default()
	}
	return &rawInputConn{Conn: conn, focus: focus, targets: targets, fwd: fwd, wheel: wheel, log: log}
}

// Read intercepts inbound frames. Binary frames carry keystroke bytes; text
// frames carry resize/focus/blur envelopes and always pass through untouched.
func (c *rawInputConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	mt, data, err := c.Conn.Read(ctx)
	if err != nil || mt != websocket.MessageBinary || len(data) == 0 {
		return mt, data, err
	}

	// Mouse frames are view-owned regardless of focus: never forwarded to a
	// PTY, never fed to the parser. Wheel ticks route to the scroll handling;
	// everything else (clicks, drags, motion) is swallowed.
	if ev, ok := terminalpane.DecodeSGRMouse(data); ok {
		if c.wheel != nil && (ev.Kind == terminalpane.WheelUp || ev.Kind == terminalpane.WheelDown) {
			c.wheel.RouteWheel(ev.Kind == terminalpane.WheelUp, ev.X, ev.Y)
		}
		return mt, nil, nil
	}

	state := c.focus.CurrentFocus()
	if state == FocusRAIL {
		// Rail navigation / mutation keys are parsed by tcell + KeyRouter.
		return mt, data, nil
	}
	if isHeraChord(data) {
		// View-owned control chord: let the parser produce the event so
		// KeyRouter can navigate, even though focus is in a pane.
		return mt, data, nil
	}

	var taskID string
	if state == FocusCOORD {
		taskID = c.targets.CoordTaskID()
	} else {
		taskID = c.targets.AgentTaskID()
	}
	if taskID == "" {
		// Focus is in a pane but nothing is bound — nowhere to send the bytes.
		// Swallow them (don't drive the parser) and surface the drop.
		c.log.Debug("view.rawinput: focused pane has no bound task; dropping raw input",
			"focus", state.String(), "bytes", len(data))
		return mt, nil, nil
	}

	// Forward the raw bytes verbatim, then swallow from the parser so the same
	// bytes are never additionally handled as tcell events (no double send).
	c.fwd.Enqueue(taskID, data)
	return mt, nil, nil
}

// isHeraChord reports whether a raw input frame is one of the view-owned control
// chords that must be intercepted for navigation even when focus is in a pane.
// It mirrors the interception logic in keys.go (HandleKey + handlePane):
//
//   - Ctrl-Q (0x11) — escape to RAIL.
//   - Ctrl-←/→ (and the Ctrl+Alt "Cmd-emulated" form) — focus ladder.
//   - Ctrl-↑/↓ — in-pane navigation.
//   - Shift-↑/↓ — pane scrollback.
//
// The xterm modified-key form is "ESC [ 1 ; <param> <final>" where
// <param> = 1 + Shift(1) + Alt(2) + Ctrl(4). Decoding the param lets us
// distinguish a chord (Ctrl, or Shift on an arrow) from forwardable content
// (Alt-modified word-motion, plain arrows, ESC-prefixed runes), so passthrough
// never collides with a chord.
func isHeraChord(b []byte) bool {
	if len(b) == 1 && b[0] == 0x11 { // Ctrl-Q
		return true
	}
	// ESC [ 1 ; <param> <final>
	if len(b) == 6 && b[0] == 0x1b && b[1] == '[' && b[2] == '1' && b[3] == ';' {
		param := int(b[4] - '0')
		if param < 1 || param > 8 {
			return false
		}
		mod := param - 1
		ctrl := mod&4 != 0
		shift := mod&1 != 0
		switch b[5] {
		case 'C', 'D': // Right / Left — focus ladder (Ctrl / Cmd-emulated)
			return ctrl
		case 'A', 'B': // Up / Down — scroll (Shift) or in-pane nav (Ctrl)
			return shift || ctrl
		}
	}
	return false
}
