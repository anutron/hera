package view

import (
	"context"
	"io"
	"testing"

	"github.com/coder/websocket"
)

// scriptedConn is a fake pluginview.Conn that serves a fixed list of inbound
// frames, then EOFs. Writes/Close are no-ops. It lets us drive
// rawInputConn.Read without a real WebSocket.
type scriptedConn struct {
	frames []scriptedFrame
	i      int
}

type scriptedFrame struct {
	typ  websocket.MessageType
	data []byte
}

func (c *scriptedConn) Read(_ context.Context) (websocket.MessageType, []byte, error) {
	if c.i >= len(c.frames) {
		return 0, nil, io.EOF
	}
	f := c.frames[c.i]
	c.i++
	return f.typ, f.data, nil
}

func (c *scriptedConn) Write(_ context.Context, _ websocket.MessageType, _ []byte) error {
	return nil
}

func (c *scriptedConn) Close(_ websocket.StatusCode, _ string) error { return nil }

// fakeFocus is a thread-safe-enough focusReader for single-threaded tests.
type fakeFocus struct{ s FocusState }

func (f *fakeFocus) CurrentFocus() FocusState { return f.s }

// readOne drives a single rawInputConn.Read against a one-frame scriptedConn.
func readOne(t *testing.T, rc *rawInputConn) (websocket.MessageType, []byte) {
	t.Helper()
	mt, data, err := rc.Read(context.Background())
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	return mt, data
}

func newRawConn(focus FocusState, coord, agent string, frame scriptedFrame) (*rawInputConn, *recordingForwarder) {
	fwd := &recordingForwarder{}
	conn := &scriptedConn{frames: []scriptedFrame{frame}}
	rc := newRawInputConn(conn, &fakeFocus{s: focus}, &fakeTargets{coord: coord, agent: agent}, fwd, nil, nil)
	return rc, fwd
}

func binFrame(b ...byte) scriptedFrame {
	return scriptedFrame{typ: websocket.MessageBinary, data: b}
}

// TestRawInput_PaneContentForwardedVerbatim proves a multi-byte sequence that a
// tcell re-parse would mangle (ESC DEL = Alt+Backspace / word-delete) is
// forwarded to the bound AGENT task byte-for-byte and swallowed from the parser.
func TestRawInput_PaneContentForwardedVerbatim(t *testing.T) {
	rc, fwd := newRawConn(FocusAGENT, "coord-1", "agent-1", binFrame(0x1b, 0x7f))

	_, data := readOne(t, rc)
	if len(data) != 0 {
		t.Fatalf("pane content must be swallowed from the parser; got %v", data)
	}
	calls := fwd.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 forward, got %d", len(calls))
	}
	if calls[0].TaskID != "agent-1" {
		t.Fatalf("want forward to agent-1, got %q", calls[0].TaskID)
	}
	if string(calls[0].Payload) != string([]byte{0x1b, 0x7f}) {
		t.Fatalf("want verbatim ESC DEL, got %v", calls[0].Payload)
	}
}

// TestRawInput_BareEnterForwardsCR proves plain Enter (CR) in a pane forwards
// the 0x0d byte (so the agent's line discipline submits).
func TestRawInput_BareEnterForwardsCR(t *testing.T) {
	rc, fwd := newRawConn(FocusAGENT, "coord-1", "agent-1", binFrame(0x0d))

	_, data := readOne(t, rc)
	if len(data) != 0 {
		t.Fatalf("CR must be swallowed from the parser; got %v", data)
	}
	calls := fwd.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "\r" {
		t.Fatalf("want a single CR forward, got %+v", calls)
	}
}

// TestRawInput_CoordFocusForwardsToCoord proves COORD focus routes to the coord task.
func TestRawInput_CoordFocusForwardsToCoord(t *testing.T) {
	rc, fwd := newRawConn(FocusCOORD, "coord-1", "agent-1", binFrame('x'))

	readOne(t, rc)
	calls := fwd.Calls()
	if len(calls) != 1 || calls[0].TaskID != "coord-1" || string(calls[0].Payload) != "x" {
		t.Fatalf("want forward of 'x' to coord-1, got %+v", calls)
	}
}

// TestRawInput_RailFocusPassesToParser proves RAIL-focus input is handed to the
// tcell parser unchanged and never forwarded to a task.
func TestRawInput_RailFocusPassesToParser(t *testing.T) {
	rc, fwd := newRawConn(FocusRAIL, "coord-1", "agent-1", binFrame('j'))

	mt, data := readOne(t, rc)
	if mt != websocket.MessageBinary || string(data) != "j" {
		t.Fatalf("rail input must pass through unchanged; got typ=%v data=%v", mt, data)
	}
	if len(fwd.Calls()) != 0 {
		t.Fatalf("rail input must not be forwarded; got %+v", fwd.Calls())
	}
}

// TestRawInput_ChordsPassToParserEvenInPane proves the view-owned control chords
// are handed to the parser (so KeyRouter navigates) and NOT forwarded, even when
// focus is in a pane.
func TestRawInput_ChordsPassToParserEvenInPane(t *testing.T) {
	chords := map[string][]byte{
		"Ctrl-Q":       {0x11},
		"Ctrl-Right":   []byte("\x1b[1;5C"),
		"Ctrl-Left":    []byte("\x1b[1;5D"),
		"Cmd-Right(7)": []byte("\x1b[1;7C"),
		"Ctrl-Up":      []byte("\x1b[1;5A"),
		"Ctrl-Down":    []byte("\x1b[1;5B"),
		"Shift-Up":     []byte("\x1b[1;2A"),
		"Shift-Down":   []byte("\x1b[1;2B"),
	}
	for name, seq := range chords {
		t.Run(name, func(t *testing.T) {
			rc, fwd := newRawConn(FocusAGENT, "coord-1", "agent-1", binFrame(seq...))
			mt, data := readOne(t, rc)
			if mt != websocket.MessageBinary || string(data) != string(seq) {
				t.Fatalf("chord must pass to parser unchanged; got %v", data)
			}
			if len(fwd.Calls()) != 0 {
				t.Fatalf("chord must NOT be forwarded; got %+v", fwd.Calls())
			}
		})
	}
}

// TestRawInput_AltArrowForwardedNotIntercepted proves Alt-modified word-motion
// (mod 3) is treated as forwardable content, not a hera chord — it must NOT
// collide with the Ctrl/Shift chord allowlist.
func TestRawInput_AltArrowForwardedNotIntercepted(t *testing.T) {
	rc, fwd := newRawConn(FocusAGENT, "coord-1", "agent-1", binFrame([]byte("\x1b[1;3D")...))

	_, data := readOne(t, rc)
	if len(data) != 0 {
		t.Fatalf("Alt-arrow is content and must be swallowed from parser; got %v", data)
	}
	calls := fwd.Calls()
	if len(calls) != 1 || string(calls[0].Payload) != "\x1b[1;3D" {
		t.Fatalf("want verbatim Alt-Left forward, got %+v", calls)
	}
}

// TestRawInput_NoBoundTaskDropsContent proves pane content with no bound task is
// dropped (not forwarded) and swallowed from the parser.
func TestRawInput_NoBoundTaskDropsContent(t *testing.T) {
	rc, fwd := newRawConn(FocusAGENT, "", "", binFrame('Z'))

	_, data := readOne(t, rc)
	if len(data) != 0 {
		t.Fatalf("content must be swallowed; got %v", data)
	}
	if len(fwd.Calls()) != 0 {
		t.Fatalf("no task bound — nothing to forward; got %+v", fwd.Calls())
	}
}

// TestRawInput_TextEnvelopePassesThrough proves text frames (resize/focus/blur
// envelopes) are never forwarded and always reach the parser unchanged.
func TestRawInput_TextEnvelopePassesThrough(t *testing.T) {
	env := []byte(`{"type":"resize","cols":120,"rows":40}`)
	fwd := &recordingForwarder{}
	conn := &scriptedConn{frames: []scriptedFrame{{typ: websocket.MessageText, data: env}}}
	rc := newRawInputConn(conn, &fakeFocus{s: FocusAGENT}, &fakeTargets{coord: "c", agent: "a"}, fwd, nil, nil)

	mt, data, err := rc.Read(context.Background())
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if mt != websocket.MessageText || string(data) != string(env) {
		t.Fatalf("text envelope must pass through unchanged; got typ=%v data=%s", mt, data)
	}
	if len(fwd.Calls()) != 0 {
		t.Fatalf("text envelope must never be forwarded; got %+v", fwd.Calls())
	}
}

// TestIsHeraChord_Matrix locks the chord predicate against the keys.go
// interception logic: Ctrl-arrows (focus ladder / in-pane nav) and Shift-arrows
// (scroll) and Ctrl-Q are chords; Alt-arrows and plain content are not.
// BUG-029: Ctrl-Z (0x1a) must also be a chord — it must never be forwarded to
// a pane PTY because that byte delivers SIGTSTP and suspends the pane's process.
func TestIsHeraChord_Matrix(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want bool
	}{
		{"Ctrl-Q", []byte{0x11}, true},
		{"Ctrl-Z", []byte{0x1a}, true}, // BUG-029: must be intercepted, never forwarded
		{"Ctrl-Right", []byte("\x1b[1;5C"), true},
		{"Ctrl-Left", []byte("\x1b[1;5D"), true},
		{"CtrlAlt-Right(cmd)", []byte("\x1b[1;7C"), true},
		{"CtrlAlt-Left(cmd)", []byte("\x1b[1;7D"), true},
		{"Ctrl-Up", []byte("\x1b[1;5A"), true},
		{"Ctrl-Down", []byte("\x1b[1;5B"), true},
		{"Shift-Up", []byte("\x1b[1;2A"), true},
		{"Shift-Down", []byte("\x1b[1;2B"), true},
		{"Alt-Left(word)", []byte("\x1b[1;3D"), false},
		{"Alt-Right(word)", []byte("\x1b[1;3C"), false},
		{"plain-Up", []byte("\x1b[A"), false},
		{"plain-rune", []byte("x"), false},
		{"CR", []byte{0x0d}, false},
		{"ESC-DEL", []byte{0x1b, 0x7f}, false},
		{"ESC-b(alt-rune)", []byte{0x1b, 'b'}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHeraChord(tc.b); got != tc.want {
				t.Fatalf("isHeraChord(%v) = %v, want %v", tc.b, got, tc.want)
			}
		})
	}
}

// TestRawInput_CtrlZ_0x1a_PassesToParserNotForwardedInPane proves that the
// raw SIGTSTP byte (0x1a, Ctrl-Z) is NEVER forwarded to a pane PTY when a
// pane has focus. BUG-029: BUG-027 intercepted Ctrl-Z in HandleKey (the
// keyenc/tcell path) but the raw-input forwarder was a second path to the PTY
// that bypassed that interception — 0x1a reached the pane's process as SIGTSTP
// and suspended it. The fix adds 0x1a to isHeraChord so the raw frame is
// returned to the parser (where HandleKey consumes it) instead of being
// forwarded verbatim.
func TestRawInput_CtrlZ_0x1a_PassesToParserNotForwardedInPane(t *testing.T) {
	for _, focus := range []FocusState{FocusCOORD, FocusAGENT} {
		t.Run(focus.String(), func(t *testing.T) {
			rc, fwd := newRawConn(focus, "coord-1", "agent-1", binFrame(0x1a))

			mt, data := readOne(t, rc)

			// Must pass to the tcell parser (so HandleKey can consume it).
			if mt != websocket.MessageBinary || len(data) != 1 || data[0] != 0x1a {
				t.Fatalf("0x1a must pass to parser unchanged; got typ=%v data=%v", mt, data)
			}
			// Must NOT be forwarded to the PTY (that would SIGTSTP the pane's process).
			if calls := fwd.Calls(); len(calls) != 0 {
				t.Fatalf("0x1a must NOT be forwarded to PTY; got %d forward call(s): %+v", len(calls), calls)
			}
		})
	}
}

// TestApp_CurrentFocus_TracksOnFocusChanged proves the thread-safe focus mirror
// the raw-input router reads is updated by OnFocusChanged.
func TestApp_CurrentFocus_TracksOnFocusChanged(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	if got := a.CurrentFocus(); got != FocusRAIL {
		t.Fatalf("default focus mirror = %v, want RAIL", got)
	}
	a.OnFocusChanged(FocusAGENT)
	if got := a.CurrentFocus(); got != FocusAGENT {
		t.Fatalf("after OnFocusChanged(AGENT), mirror = %v, want AGENT", got)
	}
	a.OnFocusChanged(FocusRAIL)
	if got := a.CurrentFocus(); got != FocusRAIL {
		t.Fatalf("after OnFocusChanged(RAIL), mirror = %v, want RAIL", got)
	}
}
