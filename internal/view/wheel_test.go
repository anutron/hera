package view

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/argus-sdk/terminalpane"
	"github.com/gdamore/tcell/v2"
)

// fakeWheelRouter records RouteWheel calls so raw-input tests can assert the
// wheel dispatch without a tview event loop.
type fakeWheelRouter struct {
	mu    sync.Mutex
	calls []wheelCall
}

type wheelCall struct {
	Up   bool
	X, Y int
}

func (f *fakeWheelRouter) RouteWheel(up bool, x, y int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, wheelCall{Up: up, X: x, Y: y})
}

func (f *fakeWheelRouter) Calls() []wheelCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wheelCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// newRawConnWheel mirrors newRawConn but wires a wheel recorder.
func newRawConnWheel(focus FocusState, coord, agent string, frame scriptedFrame) (*rawInputConn, *recordingForwarder, *fakeWheelRouter) {
	fwd := &recordingForwarder{}
	wheel := &fakeWheelRouter{}
	conn := &scriptedConn{frames: []scriptedFrame{frame}}
	rc := newRawInputConn(conn, &fakeFocus{s: focus}, &fakeTargets{coord: coord, agent: agent}, fwd, wheel, nil)
	return rc, fwd, wheel
}

// feedPane writes one chunk into a terminalpane source channel and blocks
// until the consumer goroutine has ingested it (Touched advances), so the
// emulator state is settled before the test asserts.
func feedPane(t *testing.T, tp *terminalpane.TerminalPane, src chan<- []byte, b []byte) {
	t.Helper()
	before := tp.Touched()
	src <- b
	deadline := time.Now().Add(2 * time.Second)
	for tp.Touched() == before {
		if time.Now().After(deadline) {
			t.Fatal("terminalpane never consumed the chunk")
		}
		time.Sleep(time.Millisecond)
	}
}

// tenLines is a settled stream of ten numbered lines — enough to push
// L01..L05 into scrollback on a 5-row emulator, leaving L06..L10 live.
const tenLines = "L01\r\nL02\r\nL03\r\nL04\r\nL05\r\nL06\r\nL07\r\nL08\r\nL09\r\nL10"

// --- rawInputConn: mouse frames are view-owned in every focus state ---

// TestRawInput_WheelInPaneFocusRoutedNotForwarded proves a wheel frame
// arriving while focus is in a pane is routed to the wheel handler and is
// NEVER forwarded to the bound task's PTY nor passed to the parser.
func TestRawInput_WheelInPaneFocusRoutedNotForwarded(t *testing.T) {
	rc, fwd, wheel := newRawConnWheel(FocusAGENT, "coord-1", "agent-1",
		scriptedFrame{typ: binFrame().typ, data: []byte("\x1b[<64;50;10M")})

	_, data := readOne(t, rc)
	if len(data) != 0 {
		t.Fatalf("wheel frame must be swallowed from the parser; got %v", data)
	}
	if calls := fwd.Calls(); len(calls) != 0 {
		t.Fatalf("wheel frame must not be forwarded to the PTY; got %+v", calls)
	}
	calls := wheel.Calls()
	if len(calls) != 1 || !calls[0].Up || calls[0].X != 50 || calls[0].Y != 10 {
		t.Fatalf("want one WheelUp at (50,10), got %+v", calls)
	}
}

// TestRawInput_WheelInRailFocusRoutedNotParsed proves RAIL focus does not
// leak the mouse bytes into the tcell parser (where they would type garbage
// rail keys) — the frame is swallowed and the wheel handler is called.
func TestRawInput_WheelInRailFocusRoutedNotParsed(t *testing.T) {
	rc, fwd, wheel := newRawConnWheel(FocusRAIL, "coord-1", "agent-1",
		scriptedFrame{typ: binFrame().typ, data: []byte("\x1b[<65;5;5M")})

	_, data := readOne(t, rc)
	if len(data) != 0 {
		t.Fatalf("wheel frame must not reach the parser in RAIL focus; got %v", data)
	}
	if calls := fwd.Calls(); len(calls) != 0 {
		t.Fatalf("wheel frame must never forward; got %+v", calls)
	}
	calls := wheel.Calls()
	if len(calls) != 1 || calls[0].Up || calls[0].X != 5 || calls[0].Y != 5 {
		t.Fatalf("want one WheelDown at (5,5), got %+v", calls)
	}
}

// TestRawInput_ClickSwallowedEverywhere proves non-wheel mouse events (press
// and release) are swallowed in both pane and RAIL focus: no PTY forward, no
// parser dispatch, no wheel routing.
func TestRawInput_ClickSwallowedEverywhere(t *testing.T) {
	cases := []struct {
		name  string
		focus FocusState
		frame []byte
	}{
		{"click press in pane focus", FocusAGENT, []byte("\x1b[<0;5;5M")},
		{"click release in rail focus", FocusRAIL, []byte("\x1b[<0;5;5m")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc, fwd, wheel := newRawConnWheel(tc.focus, "coord-1", "agent-1",
				scriptedFrame{typ: binFrame().typ, data: tc.frame})

			_, data := readOne(t, rc)
			if len(data) != 0 {
				t.Fatalf("click frame must be swallowed; got %v", data)
			}
			if calls := fwd.Calls(); len(calls) != 0 {
				t.Fatalf("click frame must not forward; got %+v", calls)
			}
			if calls := wheel.Calls(); len(calls) != 0 {
				t.Fatalf("click frame must not route as wheel; got %+v", calls)
			}
		})
	}
}

// TestRawInput_NilWheelRouterSafe proves a wheel frame with no router wired
// is swallowed without panicking (daemon startup wiring order).
func TestRawInput_NilWheelRouterSafe(t *testing.T) {
	fwd := &recordingForwarder{}
	conn := &scriptedConn{frames: []scriptedFrame{{typ: binFrame().typ, data: []byte("\x1b[<64;1;1M")}}}
	rc := newRawInputConn(conn, &fakeFocus{s: FocusAGENT}, &fakeTargets{coord: "c", agent: "a"}, fwd, nil, nil)

	_, data := readOne(t, rc)
	if len(data) != 0 || len(fwd.Calls()) != 0 {
		t.Fatalf("wheel frame must be swallowed with nil router; data=%v fwd=%+v", data, fwd.Calls())
	}
}

// TestRawInput_HeraChordStillPassesWithWheelWired guards against the mouse
// peel-off regressing chord recognition: a view-owned chord still reaches the
// parser.
func TestRawInput_HeraChordStillPassesWithWheelWired(t *testing.T) {
	rc, fwd, wheel := newRawConnWheel(FocusAGENT, "coord-1", "agent-1",
		scriptedFrame{typ: binFrame().typ, data: []byte{0x1b, '[', '1', ';', '5', 'A'}})

	_, data := readOne(t, rc)
	if string(data) != "\x1b[1;5A" {
		t.Fatalf("Ctrl-Up chord must pass to the parser; got %v", data)
	}
	if len(fwd.Calls()) != 0 || len(wheel.Calls()) != 0 {
		t.Fatalf("chord must neither forward nor route as wheel")
	}
}

// --- App.applyWheel: hit-testing routes by position ---

// wheelTestApp builds an App with laid-out rail/coord/agent rects matching
// production geometry (top bar row 0, rail cols [0,36), panes splitting the
// rest) without a running tview event loop. Callers feed pane content through
// the returned source channels.
func wheelTestApp(t *testing.T) (*App, chan []byte, chan []byte) {
	t.Helper()
	coordSrc := make(chan []byte)
	agentSrc := make(chan []byte)
	coordTp := terminalpane.New(coordSrc)
	agentTp := terminalpane.New(agentSrc)
	coord := newPinnedTerminalPane(coordTp, 20, 5)
	agent := newPinnedTerminalPane(agentTp, 20, 5)
	pieces := buildLayout(coord, agent)
	a := &App{pieces: pieces, coordPresent: true, agentPresent: true}
	t.Cleanup(func() {
		coord.Close()
		agent.Close()
		close(coordSrc)
		close(agentSrc)
	})

	pieces.rail.SetRect(0, 1, 36, 20)
	coord.SetRect(36, 1, 40, 20)
	agent.SetRect(76, 1, 40, 20)
	return a, coordSrc, agentSrc
}

// TestApp_ApplyWheel_PaneUnderCursorScrolls proves a wheel-up whose
// coordinates land inside the agent pane scrolls THAT pane 3 lines into
// history, leaving the coord pane and the rail untouched.
func TestApp_ApplyWheel_PaneUnderCursorScrolls(t *testing.T) {
	a, _, agentSrc := wheelTestApp(t)
	feedPane(t, a.pieces.agent.TerminalPane, agentSrc, []byte(tenLines))

	// (80, 10) zero-based is inside the agent rect (76,1,40,20) → SGR 1-based (81, 11).
	a.applyWheel(true, 81, 11)

	if got := a.pieces.agent.ScrollOffset(); got != wheelStep {
		t.Fatalf("agent pane: want offset %d after one wheel-up, got %d", wheelStep, got)
	}
	if got := a.pieces.coord.ScrollOffset(); got != 0 {
		t.Fatalf("coord pane must not scroll; got offset %d", got)
	}
	if a.pieces.rail.offset != 0 {
		t.Fatalf("rail must not pan; got offset %d", a.pieces.rail.offset)
	}
}

// TestApp_ApplyWheel_PositionBeatsFocus proves routing is positional: a wheel
// over the COORD pane scrolls it even though nothing about focus points there.
func TestApp_ApplyWheel_PositionBeatsFocus(t *testing.T) {
	a, coordSrc, _ := wheelTestApp(t)
	feedPane(t, a.pieces.coord.TerminalPane, coordSrc, []byte(tenLines))

	// (40, 10) zero-based is inside the coord rect (36,1,40,20) → SGR (41, 11).
	a.applyWheel(true, 41, 11)

	if got := a.pieces.coord.ScrollOffset(); got != wheelStep {
		t.Fatalf("coord pane: want offset %d, got %d", wheelStep, got)
	}
	if got := a.pieces.agent.ScrollOffset(); got != 0 {
		t.Fatalf("agent pane must not scroll; got offset %d", got)
	}
}

// TestApp_ApplyWheel_WheelDownReturnsToLive proves wheel-down walks the
// offset back to 0 (the live screen) and clamps there.
func TestApp_ApplyWheel_WheelDownReturnsToLive(t *testing.T) {
	a, _, agentSrc := wheelTestApp(t)
	feedPane(t, a.pieces.agent.TerminalPane, agentSrc, []byte(tenLines))

	a.applyWheel(true, 81, 11)  // up 3
	a.applyWheel(false, 81, 11) // down 3 → live
	if got := a.pieces.agent.ScrollOffset(); got != 0 {
		t.Fatalf("want offset 0 after symmetric wheel-down, got %d", got)
	}
	a.applyWheel(false, 81, 11) // below live clamps
	if got := a.pieces.agent.ScrollOffset(); got != 0 {
		t.Fatalf("offset must clamp at live; got %d", got)
	}
}

// TestApp_ApplyWheel_DeadZoneSwallowed proves a wheel on the top bar (outside
// rail and panes) does nothing.
func TestApp_ApplyWheel_DeadZoneSwallowed(t *testing.T) {
	a, _, agentSrc := wheelTestApp(t)
	feedPane(t, a.pieces.agent.TerminalPane, agentSrc, []byte(tenLines))

	a.applyWheel(true, 50, 1) // SGR (50,1) → zero-based row 0: the top bar

	if got := a.pieces.agent.ScrollOffset(); got != 0 {
		t.Fatalf("dead-zone wheel must not scroll the agent pane; got %d", got)
	}
	if got := a.pieces.coord.ScrollOffset(); got != 0 {
		t.Fatalf("dead-zone wheel must not scroll the coord pane; got %d", got)
	}
	if a.pieces.rail.offset != 0 {
		t.Fatalf("dead-zone wheel must not pan the rail; got %d", a.pieces.rail.offset)
	}
}

// TestApp_ApplyWheel_AbsentPaneStaleRectIgnored proves a pane removed from
// the body (freelance mode: coordPresent=false) is not scrolled through its
// stale rect.
func TestApp_ApplyWheel_AbsentPaneStaleRectIgnored(t *testing.T) {
	a, coordSrc, _ := wheelTestApp(t)
	feedPane(t, a.pieces.coord.TerminalPane, coordSrc, []byte(tenLines))
	a.coordPresent = false

	a.applyWheel(true, 41, 11) // inside coord's stale rect

	if got := a.pieces.coord.ScrollOffset(); got != 0 {
		t.Fatalf("absent coord pane must not scroll via stale rect; got %d", got)
	}
}

// TestApp_ScrollFocusedPane_DrivesSDKEngine proves the ⇧↑/⇧↓ keyboard path
// shares the wheel's engine: the offset is now a real, SDK-rendered offset.
func TestApp_ScrollFocusedPane_DrivesSDKEngine(t *testing.T) {
	a, _, agentSrc := wheelTestApp(t)
	feedPane(t, a.pieces.agent.TerminalPane, agentSrc, []byte(tenLines))

	a.ScrollFocusedPane(FocusAGENT, 1)
	if got := a.pieces.agent.ScrollOffset(); got != 1 {
		t.Fatalf("⇧↑ must scroll one visible line; got offset %d", got)
	}
	a.ScrollFocusedPane(FocusAGENT, -1)
	if got := a.pieces.agent.ScrollOffset(); got != 0 {
		t.Fatalf("⇧↓ must return to live; got offset %d", got)
	}
}

// --- pinned pane: scrolled rendering through the clipping Draw ---

// TestPinnedTerminalPane_ScrolledRendersHistoryAndBadge proves the visible
// surface actually changes when scrolled (the D15 limitation is gone): a
// scrolled pane paints history lines plus the [SCROLL] badge, and returning
// to live restores the tail without the badge.
func TestPinnedTerminalPane_ScrolledRendersHistoryAndBadge(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()
	p := newPinnedTerminalPane(tp, 20, 5)
	feedPane(t, tp, src, []byte(tenLines))

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(22, 7)
	p.SetRect(0, 0, 22, 7)

	// Live: tail visible, no badge.
	p.Draw(sim)
	sim.Show()
	if got := readScreen(sim); !strings.Contains(got, "L10") || strings.Contains(got, "[SCROLL]") {
		t.Fatalf("live pane must show the tail without a badge; got:\n%s", got)
	}

	// Scrolled up 3: history window visible, badge on.
	p.ScrollBy(3)
	sim.Clear()
	p.Draw(sim)
	sim.Show()
	got := readScreen(sim)
	if !strings.Contains(got, "[SCROLL]") {
		t.Fatalf("scrolled pane must show the [SCROLL] badge; got:\n%s", got)
	}
	if !strings.Contains(got, "L04") {
		t.Fatalf("scrolled pane must paint history (want L04 visible); got:\n%s", got)
	}
	if strings.Contains(got, "L09") || strings.Contains(got, "L10") {
		t.Fatalf("scrolled pane must not still show the live tail; got:\n%s", got)
	}

	// Back to live: badge gone, tail restored.
	p.ScrollBy(-3)
	sim.Clear()
	p.Draw(sim)
	sim.Show()
	if got := readScreen(sim); !strings.Contains(got, "L10") || strings.Contains(got, "[SCROLL]") {
		t.Fatalf("pane returned to live must show the tail without a badge; got:\n%s", got)
	}
}

// TestPinnedTerminalPane_AnchorLockHoldsContent proves output arriving while
// scrolled does not shift the viewed window (the effective offset grows).
func TestPinnedTerminalPane_AnchorLockHoldsContent(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()
	p := newPinnedTerminalPane(tp, 20, 5)
	feedPane(t, tp, src, []byte(tenLines))

	p.ScrollBy(3)
	before := p.ScrollOffset()

	// Two more lines arrive while scrolled → two more scrollback lines.
	feedPane(t, tp, src, []byte("\r\nL11\r\nL12"))

	if got := p.ScrollOffset(); got != before+2 {
		t.Fatalf("anchor-lock: want offset %d after 2 new scrollback lines, got %d", before+2, got)
	}
}

// --- rail: wheel pans the viewport without moving the selection ---

// manyRoleRail builds a rail with one orchestrator and n roles so the row
// list overflows small heights.
func manyRoleRail(n int) *railList {
	rl := newRailList()
	roles := make([]*roleEntry, 0, n)
	for i := 0; i < n; i++ {
		roles = append(roles, &roleEntry{OrchestratorID: 1, RoleID: int64(100 + i), Name: "w" + string(rune('a'+i)), Live: true})
	}
	rl.SetOrchestrators([]*orchEntry{{ID: 1, Name: "p", Roles: roles}})
	return rl
}

// TestRailList_PanByPansWithoutMovingSelection proves PanBy shifts the
// viewport, leaves the cursor where it was, and the pan survives subsequent
// draws (no snap-back while the cursor hasn't moved).
func TestRailList_PanByPansWithoutMovingSelection(t *testing.T) {
	rl := manyRoleRail(12) // 13 rows
	renderRail(t, rl, 22, 6)

	cursorBefore := rl.cursor
	rl.PanBy(3)
	renderRail(t, rl, 22, 6)
	if rl.offset != 3 {
		t.Fatalf("pan must survive a draw without cursor movement; offset=%d want 3", rl.offset)
	}
	if rl.cursor != cursorBefore {
		t.Fatalf("PanBy must not move the cursor; got %d want %d", rl.cursor, cursorBefore)
	}

	// And again — repeated refresh repaints must not snap back.
	renderRail(t, rl, 22, 6)
	if rl.offset != 3 {
		t.Fatalf("pan must persist across refresh repaints; offset=%d want 3", rl.offset)
	}
}

// TestRailList_PanByClampsToContent proves panning clamps at the last
// viewport-full of rows and at zero.
func TestRailList_PanByClampsToContent(t *testing.T) {
	rl := manyRoleRail(12) // 13 rows
	renderRail(t, rl, 22, 6)

	rl.PanBy(1000)
	_, _, _, innerH := rl.GetInnerRect()
	if want := len(rl.rows) - innerH; rl.offset != want {
		t.Fatalf("pan past the end must clamp to rows-innerH=%d (rows=%d innerH=%d); got %d",
			want, len(rl.rows), innerH, rl.offset)
	}
	rl.PanBy(-1000)
	if rl.offset != 0 {
		t.Fatalf("pan past the top must clamp to 0; got %d", rl.offset)
	}
}

// TestRailList_PanByNoOpWhenContentFits proves the wheel does nothing when
// all rows fit the viewport.
func TestRailList_PanByNoOpWhenContentFits(t *testing.T) {
	rl := manyRoleRail(3) // 4 rows
	renderRail(t, rl, 22, 6)

	rl.PanBy(3)
	renderRail(t, rl, 22, 6)
	if rl.offset != 0 {
		t.Fatalf("pan must be a no-op when content fits; got offset %d", rl.offset)
	}
}

// TestRailList_CursorMoveResnapsAfterPan proves selection movement restores
// the cursor-follow behavior after a wheel pan.
func TestRailList_CursorMoveResnapsAfterPan(t *testing.T) {
	rl := manyRoleRail(12)
	renderRail(t, rl, 22, 6)

	rl.PanBy(5) // cursor (0) far above the viewport now
	renderRail(t, rl, 22, 6)
	if rl.offset != 5 {
		t.Fatalf("precondition: pan to 5, got %d", rl.offset)
	}

	rl.cursor = 1 // j: selection moved
	renderRail(t, rl, 22, 6)
	if rl.cursor < rl.offset || rl.cursor >= rl.offset+6 {
		t.Fatalf("cursor move must re-snap the viewport; cursor=%d offset=%d", rl.cursor, rl.offset)
	}
}
