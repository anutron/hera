package view

import (
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/argus-sdk/terminalpane"
	"github.com/anutron/argus-sdk/theme"
)

// fakePaneResizer records every ResizeTask call so tests can assert
// dispatch counts and dimensions.
type fakePaneResizer struct {
	mu    sync.Mutex
	calls []paneResizeCall
}

func (f *fakePaneResizer) ResizeTask(taskID string, cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, paneResizeCall{TaskID: taskID, Cols: cols, Rows: rows})
}

func (f *fakePaneResizer) Calls() []paneResizeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]paneResizeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestPinnedTerminalPane_BoundPaneKeepsPinnedSizeAcrossDraw pins the core
// contract for a BOUND-without-resizer pane (Option 2 letterbox): the SDK's
// Draw auto-resizes the emulator to its inner rect, but our wrapper must
// override that so the emulator's surface stays at the upstream PTY size for
// downstream paint logic to clip from. (An UNBOUND placeholder pane instead
// tracks its allocation so it fills the pane — see
// TestPinnedTerminalPane_UnboundPaneFillsAllocation.)
func TestPinnedTerminalPane_BoundPaneKeepsPinnedSizeAcrossDraw(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	const pinCols, pinRows = 189, 69
	// Bound to a task with NO resizer wired (Option 2 fallback): the emulator
	// surface stays pinned at the upstream PTY size and wider content
	// letterboxes inside the narrower allocation.
	p := newBoundPinnedTerminalPane(tp, pinCols, pinRows, "task-X", nil)

	if c, r := tp.PTYSize(); c != pinCols || r != pinRows {
		t.Fatalf("after construct: emulator size = %dx%d, want %dx%d",
			c, r, pinCols, pinRows)
	}

	// Allocate a smaller-than-pinned rect — production-shape for hera's
	// coord/agent column when the worker's PTY is wider than hera's allocation.
	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)

	if c, r := tp.PTYSize(); c != pinCols || r != pinRows {
		t.Fatalf("after Draw with smaller rect: emulator size = %dx%d, want %dx%d (SDK auto-resize leaked through)",
			c, r, pinCols, pinRows)
	}

	// Rect must be restored to the layout-allocated size so the next
	// Flex reflow sees the original outer bounds, not the lied size.
	x, y, w, h := p.GetRect()
	if x != 0 || y != 0 || w != allocW || h != allocH {
		t.Fatalf("after Draw: rect = (%d,%d,%d,%d), want (0,0,%d,%d)",
			x, y, w, h, allocW, allocH)
	}
}

// TestPinnedTerminalPane_UnboundPaneFillsAllocation pins BUG-003: an unbound
// placeholder pane ("(no coord selected)" / "(no agent selected)") has no
// worker PTY to letterbox, so its emulator surface must track the full
// layout-allocated inner rect — filling the pane exactly like a bound pane,
// both when the allocation is LARGER than the construction default (the
// reported symptom: empty panes rendered short at 80x24) and when it is
// smaller. No resizer dispatch occurs (there is no PTY to size).
func TestPinnedTerminalPane_UnboundPaneFillsAllocation(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	// Construction default 80x24 (the no-size fallback for an unbound pane).
	p := newPinnedTerminalPane(tp, 0, 0)

	// Allocate LARGER than the 80x24 default — the real production shape for a
	// coord/agent column in a tall terminal. Inner rect = alloc - 2 each side.
	const allocW, allocH = 120, 60
	p.SetRect(0, 0, allocW, allocH)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)

	if c, r := p.PinnedSize(); c != allocW-2 || r != allocH-2 {
		t.Fatalf("unbound pane pinned size = %dx%d, want %dx%d (must fill allocation, not stay at 80x24)",
			c, r, allocW-2, allocH-2)
	}

	// Rect restored to the allocation so the Flex reflow is unaffected.
	x, y, w, h := p.GetRect()
	if x != 0 || y != 0 || w != allocW || h != allocH {
		t.Fatalf("after Draw: rect = (%d,%d,%d,%d), want (0,0,%d,%d)",
			x, y, w, h, allocW, allocH)
	}
}

// TestPinnedTerminalPane_ZeroDefaultsTo80x24 pins the fallback when argus
// can't supply a size (404 / no live session / unbound pane): the wrapper
// resorts to defaultPinnedCols / defaultPinnedRows instead of an empty
// emulator.
func TestPinnedTerminalPane_ZeroDefaultsTo80x24(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	p := newPinnedTerminalPane(tp, 0, 0)
	c, r := p.PinnedSize()
	if c != defaultPinnedCols || r != defaultPinnedRows {
		t.Fatalf("PinnedSize = %dx%d, want %dx%d (default fallback)",
			c, r, defaultPinnedCols, defaultPinnedRows)
	}
}

// TestPinnedTerminalPane_DrawFiresResizeWithInnerRect pins the Option 1
// resize-on-Draw path: when the pane is bound to a taskID and a resizer
// is wired, the first Draw with a fresh inner rect asks argus to resize
// the worker PTY to (inner.W, inner.H) and re-pins the emulator to the
// same dimensions.
func TestPinnedTerminalPane_DrawFiresResizeWithInnerRect(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	// Construction-time pinned = queried PTY size of 189x69 — the
	// production-shape state where the worker PTY is wider than hera's
	// allocation.
	r := &fakePaneResizer{}
	p := newBoundPinnedTerminalPane(tp, 189, 69, "task-X", r)

	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("resize calls = %d, want 1: %+v", len(calls), calls)
	}
	wantCall := paneResizeCall{TaskID: "task-X", Cols: allocW - 2, Rows: allocH - 2}
	if calls[0] != wantCall {
		t.Fatalf("call = %+v, want %+v", calls[0], wantCall)
	}

	// Pinned size should now track the inner rect, not the queried PTY.
	if c, r := p.PinnedSize(); c != allocW-2 || r != allocH-2 {
		t.Fatalf("pinned size = %dx%d, want %dx%d", c, r, allocW-2, allocH-2)
	}
}

// TestPinnedTerminalPane_DrawSkipsResizeWhenInnerMatchesQueriedSize pins
// the idempotency contract: when argus's currently-reported PTY size
// (= the construction-time pinned size) already matches the layout-
// allocated inner rect, Draw does NOT fire a ResizeTask. Argus would
// cache that as a no-op, but skipping it locally avoids waking a
// goroutine and emitting the "redundant" log noise on every frame.
func TestPinnedTerminalPane_DrawSkipsResizeWhenInnerMatchesQueriedSize(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	// Queried size 78x22 → allocated outer 80x24 → inner 78x22.
	r := &fakePaneResizer{}
	p := newBoundPinnedTerminalPane(tp, 78, 22, "task-X", r)

	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)

	if calls := r.Calls(); len(calls) != 0 {
		t.Fatalf("resize calls = %+v, want zero (inner == queried)", calls)
	}
}

// TestPinnedTerminalPane_DrawDedupesAcrossFrames pins that repeated
// Draws at the same allocation only dispatch a single ResizeTask. tview
// re-Draws on every frame; we must not flood argus with identical
// requests.
func TestPinnedTerminalPane_DrawDedupesAcrossFrames(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	r := &fakePaneResizer{}
	p := newBoundPinnedTerminalPane(tp, 189, 69, "task-X", r)

	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)

	for i := 0; i < 5; i++ {
		p.SetRect(0, 0, allocW, allocH)
		p.Draw(sim)
	}

	if calls := r.Calls(); len(calls) != 1 {
		t.Fatalf("resize calls = %d, want 1 (dedup failed across frames): %+v",
			len(calls), calls)
	}
}

// TestPinnedTerminalPane_DrawRefiresOnAllocChange pins the WS-resize
// envelope path: when hera's outer rect changes (terminal resize → tview
// recomputes layout → pane gets a new allocation), Draw must re-fire
// ResizeTask with the new inner dimensions.
func TestPinnedTerminalPane_DrawRefiresOnAllocChange(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	r := &fakePaneResizer{}
	p := newBoundPinnedTerminalPane(tp, 189, 69, "task-X", r)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(200, 60)

	p.SetRect(0, 0, 80, 24)
	p.Draw(sim)

	p.SetRect(0, 0, 100, 30)
	p.Draw(sim)

	calls := r.Calls()
	if len(calls) != 2 {
		t.Fatalf("resize calls = %d, want 2: %+v", len(calls), calls)
	}
	if calls[0] != (paneResizeCall{TaskID: "task-X", Cols: 78, Rows: 22}) {
		t.Fatalf("call[0] = %+v, want {task-X, 78, 22}", calls[0])
	}
	if calls[1] != (paneResizeCall{TaskID: "task-X", Cols: 98, Rows: 28}) {
		t.Fatalf("call[1] = %+v, want {task-X, 98, 28}", calls[1])
	}
}

// TestPinnedTerminalPane_DrawSkipsResizeWhenNoTaskID pins that detached
// (placeholder) panes never dispatch a resize — the resizer hook only
// applies to bound panes.
func TestPinnedTerminalPane_DrawSkipsResizeWhenNoTaskID(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	r := &fakePaneResizer{}
	p := newBoundPinnedTerminalPane(tp, 189, 69, "", r)

	p.SetRect(0, 0, 80, 24)
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(80, 24)
	p.Draw(sim)

	if calls := r.Calls(); len(calls) != 0 {
		t.Fatalf("resize calls = %+v, want zero (empty taskID)", calls)
	}
}

// TestPinnedTerminalPane_ScrollByTracksAndClamps pins the scroll-offset
// behavior that backs the ⇧↑/⇧↓ scrollback keys (D15), now delegated to the
// SDK engine (argus-sdk ≥ v0.0.3): ScrollBy clamps to [0, retained history]
// at BOTH ends — a fresh pane with no scrollback cannot leave the live
// screen, scrolling up past the oldest retained line stops there, and
// scrolling down past the newest content stops at the live screen.
func TestPinnedTerminalPane_ScrollByTracksAndClamps(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	p := newPinnedTerminalPane(tp, 20, 5)

	if got := p.ScrollOffset(); got != 0 {
		t.Fatalf("fresh pane scroll offset: want 0, got %d", got)
	}

	// No scrollback yet: scrolling up clamps at the (empty) history.
	p.ScrollBy(3)
	if got := p.ScrollOffset(); got != 0 {
		t.Fatalf("ScrollBy with no history must clamp to 0, got %d", got)
	}

	// Ten lines on a 5-row emulator push history into scrollback.
	feedPane(t, tp, src, []byte(tenLines))

	// Scroll up into history.
	p.ScrollBy(3)
	if got := p.ScrollOffset(); got != 3 {
		t.Fatalf("after ScrollBy(3): want offset 3, got %d", got)
	}

	// Scroll down toward the live screen.
	p.ScrollBy(-1)
	if got := p.ScrollOffset(); got != 2 {
		t.Fatalf("after ScrollBy(-1): want offset 2, got %d", got)
	}

	// Scrolling down past the live screen clamps at 0 (can't go negative).
	p.ScrollBy(-100)
	if got := p.ScrollOffset(); got != 0 {
		t.Fatalf("after large negative ScrollBy: want clamp to 0, got %d", got)
	}

	// Scrolling up past the oldest retained line clamps at the history
	// ceiling: a further ScrollBy(+N) from there must not move the offset.
	p.ScrollBy(100000)
	ceiling := p.ScrollOffset()
	if ceiling <= 0 || ceiling >= 100000 {
		t.Fatalf("offset must clamp to the retained history; got %d", ceiling)
	}
	p.ScrollBy(5)
	if got := p.ScrollOffset(); got != ceiling {
		t.Fatalf("offset must stay at the ceiling %d, got %d", ceiling, got)
	}
}

// TestPinnedTerminalPane_ClipsBeyondAllocatedRect pins the letterbox
// behavior: when the emulator surface (pinned to the worker PTY) is wider
// than the layout-allocated rect, paint output past the rect must be
// dropped rather than spilling into neighbor cells.
func TestPinnedTerminalPane_ClipsBeyondAllocatedRect(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	// Pinned much wider than the allocated rect; the SDK's border draw will
	// try to paint a right vertical at x = pinCols+1. Bound-without-resizer
	// (Option 2 letterbox) so the surface STAYS larger than the allocation —
	// the case that actually exercises clipping. (An unbound placeholder pane
	// would instead re-pin to its allocation and never spill.)
	const pinCols, pinRows = 120, 30
	p := newBoundPinnedTerminalPane(tp, pinCols, pinRows, "task-X", nil)

	const allocX, allocY, allocW, allocH = 5, 2, 40, 10
	p.SetRect(allocX, allocY, allocW, allocH)

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	const screenW, screenH = 200, 40
	sim.SetSize(screenW, screenH)
	p.Draw(sim)

	cells, cw, _ := sim.GetContents()
	if cw != screenW {
		t.Fatalf("simulation width = %d, want %d", cw, screenW)
	}

	// Every cell outside [allocX, allocX+allocW) x [allocY, allocY+allocH)
	// must be untouched (zero runes).
	for row := 0; row < screenH; row++ {
		for col := 0; col < screenW; col++ {
			inside := col >= allocX && col < allocX+allocW &&
				row >= allocY && row < allocY+allocH
			idx := row*cw + col
			if idx >= len(cells) {
				continue
			}
			runes := cells[idx].Runes
			if inside {
				continue
			}
			for _, r := range runes {
				if r != 0 && r != ' ' {
					t.Fatalf("cell (%d,%d) outside alloc rect was painted (rune=%q) — clipping failed",
						col, row, r)
				}
			}
		}
	}
}

// TestPinnedTerminalPane_RepaintsCyanBorderOnFocus pins the focus-feedback
// contract for the panes (D12): the embedded SDK terminalpane hardcodes a
// white (tcell.StyleDefault) border when focused, but hera mirrors argus's
// cyan focus border everywhere, so pinnedTerminalPane.Draw repaints the border
// in theme.ColorTitle once HasFocus() is true. We focus the pane through a
// tview Application (the only thing that flips Box.hasFocus, as in production),
// draw it onto a SimulationScreen, and assert the bordered corner cell carries
// the cyan style — not the SDK's default white.
//
// HasFocus() is only true after an Application focuses the primitive; this is
// the same seam OnFocusChanged uses (a.app.SetFocus(pane)). No event loop needs
// to run — SetFocus calls pane.Focus synchronously.
func TestPinnedTerminalPane_RepaintsCyanBorderOnFocus(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	// Construction size == allocation inner rect (78x22 inner of an 80x24
	// alloc) so the pinned surface lines up with the allocated border cells —
	// the production-shape state for a bound pane after the resize handshake.
	p := newPinnedTerminalPane(tp, 78, 22)
	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	// Focus the pane via a real Application — the production path that sets
	// Box.hasFocus (OnFocusChanged calls a.app.SetFocus on the focused pane).
	app := tview.NewApplication()
	app.SetFocus(p)
	if !p.HasFocus() {
		t.Fatal("pane should report HasFocus() after Application.SetFocus")
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)
	sim.Show() // commit the back buffer so GetContents reflects the paint

	// DrawBorder paints the rounded corner '╭' at the pane's top-left cell.
	// Read it back and assert both the rune and the cyan (theme.ColorTitle)
	// foreground that StyleFocusedBorder applies — a regression to the SDK's
	// default-white focus border would fail the color check.
	cells, cw, _ := sim.GetContents()
	corner := cells[0*cw+0]
	if len(corner.Runes) == 0 || corner.Runes[0] != '╭' {
		t.Fatalf("top-left cell rune = %q, want '╭' (focused border repaint missing)", corner.Runes)
	}
	fg, _, _ := corner.Style.Decompose()
	if fg != theme.ColorTitle {
		t.Fatalf("focused border color = %v, want theme.ColorTitle (argus cyan); SDK white leaked through", fg)
	}
}

// TestPinnedTerminalPane_FocusDelegatesToEmbeddedPaneForCursorRendering pins
// the BUG-010 fix: when the operator focuses the agent or coord pane,
// OnFocusChanged calls a.app.SetFocus(pinnedTerminalPane). The wrapper's
// Focus() must ensure the embedded TerminalPane reports HasFocus()==true,
// because the SDK's paint() (v0.0.7+) gates ShowCursor on that exact check.
//
// The test verifies three things:
//   - After app.SetFocus(wrapper), the embedded TerminalPane.HasFocus()==true.
//   - After app.SetFocus(wrapper), the wrapper itself also reports HasFocus()==true
//     so the Draw border-focus branch (pinned_pane.go ~line 218) still fires.
//   - Draw places the hardware cursor on the sim screen at the emulator's
//     cursor position (the SDK's ShowCursor path only fires when HasFocus is
//     true, so a missing cursor here means the focus delegation is broken).
func TestPinnedTerminalPane_FocusDelegatesToEmbeddedPaneForCursorRendering(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	p := newPinnedTerminalPane(tp, 78, 22)
	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	// Move the emulator cursor to a known position: CSI 5;10H → row 5, col 10
	// (1-indexed) = (col=9, row=4) zero-indexed. The SDK's paint() will call
	// ShowCursor at (inner.X+9, inner.Y+4) = (1+9, 1+4) = (10, 5) on screen.
	feedPane(t, tp, src, []byte("\x1b[5;10H"))

	// Focus the wrapper via a real Application — exactly the production path
	// used by OnFocusChanged (a.app.SetFocus(a.pieces.agent)).
	app := tview.NewApplication()
	app.SetFocus(p)

	// Both the embedded TerminalPane and the wrapper must report HasFocus.
	if !tp.HasFocus() {
		t.Fatal("embedded TerminalPane.HasFocus() must be true after app.SetFocus(wrapper) — SDK paint() gates ShowCursor on this")
	}
	if !p.HasFocus() {
		t.Fatal("wrapper.HasFocus() must be true after app.SetFocus(wrapper) — border-focus styling requires this")
	}

	// Draw onto a simulation screen and verify the hardware cursor was placed.
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)

	cx, cy, visible := sim.GetCursor()
	if !visible {
		t.Fatal("cursor must be visible after drawing a focused pane — Focus delegation to embedded TerminalPane is broken")
	}
	// inner origin is (1,1) (one-cell border). Emulator cursor is at (9,4)
	// zero-indexed → screen position (1+9, 1+4) = (10, 5).
	if cx != 10 || cy != 5 {
		t.Errorf("cursor at (%d,%d), want (10,5) — cursor placed at wrong emulator position", cx, cy)
	}
}

// TestPinnedTerminalPane_ReflowCallbackFiresOnDimsChange pins the BUG-038 fix:
// when a bound pane's inner rect changes, onReflow must be called with the new
// dimensions so the App can replay the ring buffer through a fresh emulator at
// the new size, reflowing scrollback to the new width.
func TestPinnedTerminalPane_ReflowCallbackFiresOnDimsChange(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	resizer := &fakePaneResizer{}
	p := newBoundPinnedTerminalPane(tp, 78, 22, "task-X", resizer)

	type reflowCall struct{ cols, rows int }
	var mu sync.Mutex
	var reflowCalls []reflowCall
	p.onReflow = func(cols, rows int) {
		mu.Lock()
		reflowCalls = append(reflowCalls, reflowCall{cols, rows})
		mu.Unlock()
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}

	// First Draw at the construction-time size (78x22 inner = 80x24 alloc):
	// lastDesiredCols/Rows start at 78x22, inner rect matches — no callback.
	sim.SetSize(80, 24)
	p.SetRect(0, 0, 80, 24)
	p.Draw(sim)

	mu.Lock()
	got := len(reflowCalls)
	mu.Unlock()
	if got != 0 {
		t.Fatalf("after Draw at construction size: onReflow fired %d times, want 0", got)
	}

	// Simulate going fullscreen: inner rect expands to 158x46 (160x48 outer).
	sim.SetSize(160, 48)
	p.SetRect(0, 0, 160, 48)
	p.Draw(sim)

	mu.Lock()
	calls := append([]reflowCall(nil), reflowCalls...)
	mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("after expansion Draw: onReflow fired %d times, want 1", len(calls))
	}
	if calls[0].cols != 158 || calls[0].rows != 46 {
		t.Fatalf("onReflow called with (%d, %d), want (158, 46)", calls[0].cols, calls[0].rows)
	}

	// Subsequent Draw at the same size must NOT fire the callback again.
	p.Draw(sim)

	mu.Lock()
	got = len(reflowCalls)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("after second Draw at same size: onReflow fired %d times total, want 1", got)
	}
}

// TestPinnedTerminalPane_ReflowCallbackNotFiredForUnbound pins that unbound
// placeholder panes (taskID == "") never call onReflow, even when the
// inner rect changes — those panes track their allocation directly and have
// no ring buffer to replay.
func TestPinnedTerminalPane_ReflowCallbackNotFiredForUnbound(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	// Unbound pane: taskID == "", resizer == nil — unbound case in Draw.
	p := newPinnedTerminalPane(tp, 80, 24)

	fired := false
	p.onReflow = func(_, _ int) { fired = true }

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(160, 48)
	p.SetRect(0, 0, 160, 48)
	p.Draw(sim)

	if fired {
		t.Fatal("onReflow fired for an unbound placeholder pane — must not fire (no ring buffer)")
	}
}

// TestPinnedTerminalPane_ReflowCallbackNotFiredForBoundWithoutResizer pins the
// Option 2 letterbox path: a bound pane with taskID set but resizer == nil
// keeps its emulator at the pinned PTY size. The inner-rect branch that calls
// onReflow is guarded by resizer != nil, so onReflow must not fire.
func TestPinnedTerminalPane_ReflowCallbackNotFiredForBoundWithoutResizer(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	// Bound but no resizer (Option 2 letterbox): taskID set, resizer nil.
	p := newBoundPinnedTerminalPane(tp, 78, 22, "task-X", nil)

	fired := false
	p.onReflow = func(_, _ int) { fired = true }

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	// Allocate smaller than pinned — the letterbox case.
	sim.SetSize(80, 24)
	p.SetRect(0, 0, 80, 24)
	p.Draw(sim)

	if fired {
		t.Fatal("onReflow fired for a bound-without-resizer pane — must not fire (letterbox path)")
	}
}

// --- greyscale / desaturation ---

// TestGrayscaleColor_MapsColors verifies the Rec. 601 luminance mapping for a
// set of known colors. Pure red (255,0,0) maps to luma 76; pure green to 149;
// white and black are invariant. ColorDefault must pass through unchanged.
func TestGrayscaleColor_MapsColors(t *testing.T) {
	tests := []struct {
		name string
		in   tcell.Color
		want tcell.Color
	}{
		{"default passes through", tcell.ColorDefault, tcell.ColorDefault},
		{"pure red", tcell.NewRGBColor(255, 0, 0), tcell.NewRGBColor(76, 76, 76)},
		{"pure green", tcell.NewRGBColor(0, 255, 0), tcell.NewRGBColor(149, 149, 149)},
		{"white stays white", tcell.NewRGBColor(255, 255, 255), tcell.NewRGBColor(255, 255, 255)},
		{"black stays black", tcell.NewRGBColor(0, 0, 0), tcell.NewRGBColor(0, 0, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := grayscaleColor(tc.in); got != tc.want {
				t.Errorf("grayscaleColor(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestGrayscaleColor_PaletteResolvesToGray verifies that a 256-palette color
// is resolved via its Hex value and mapped to a true gray, not passed through
// as a still-colored palette index.
func TestGrayscaleColor_PaletteResolvesToGray(t *testing.T) {
	got := grayscaleColor(tcell.PaletteColor(196)) // xterm bright red #FF0000
	r, g, b := got.RGB()
	if r < 0 || r != g || g != b {
		t.Fatalf("expected gray (r==g==b), got rgb(%d,%d,%d)", r, g, b)
	}
}

// TestDesaturateStyle_GreysAllChannels verifies fg, bg, and underline color are
// all desaturated, while non-color attributes (bold, reverse) are preserved.
func TestDesaturateStyle_GreysAllChannels(t *testing.T) {
	style := tcell.StyleDefault.
		Foreground(tcell.NewRGBColor(200, 30, 30)).
		Background(tcell.NewRGBColor(20, 20, 220)).
		Bold(true)
	out := desaturateStyle(style)

	fg, bg, attr := out.Decompose()
	assertGray(t, fg)
	assertGray(t, bg)
	if attr&tcell.AttrBold == 0 {
		t.Error("bold attribute must survive desaturation")
	}
}

// TestDesaturateStyle_GreysUnderlineColor verifies that an explicit SGR-58
// underline color is also desaturated.
func TestDesaturateStyle_GreysUnderlineColor(t *testing.T) {
	style := tcell.StyleDefault.
		Foreground(tcell.NewRGBColor(200, 30, 30)).
		Underline(tcell.UnderlineStyleCurly, tcell.NewRGBColor(0, 200, 0))
	out := desaturateStyle(style)

	assertGray(t, out.GetUnderlineColor())
	// Only the color changes; the underline style must be preserved.
	if out.GetUnderlineStyle() != tcell.UnderlineStyleCurly {
		t.Error("underline style must survive desaturation")
	}
}

// TestDesaturateStyle_DefaultUnderlineColorUntouched verifies that an underline
// with no explicit color (ulColor == ColorDefault / invalid) passes through
// unchanged, keeping the terminal's own default foreground color.
func TestDesaturateStyle_DefaultUnderlineColorUntouched(t *testing.T) {
	style := tcell.StyleDefault.Underline(true)
	out := desaturateStyle(style)
	if got := out.GetUnderlineColor(); got != tcell.ColorDefault {
		t.Errorf("default underline color = %v, want ColorDefault", got)
	}
}

// TestDesaturateStyle_DefaultForeground verifies the common pattern of a
// default fg with a colored bg: the default fg must stay default (not become a
// hard gray), and the bg must be grayed.
func TestDesaturateStyle_DefaultForeground(t *testing.T) {
	style := tcell.StyleDefault.Background(tcell.NewRGBColor(20, 20, 220))
	out := desaturateStyle(style)

	fg, bg, _ := out.Decompose()
	if fg != tcell.ColorDefault {
		t.Errorf("default fg changed to %v, want ColorDefault", fg)
	}
	assertGray(t, bg)
	if !bg.Valid() {
		t.Error("bg must be a grayed color, not ColorDefault")
	}
}

// TestPinnedTerminalPane_UnfocusedPaintedInGreyscale verifies that an unfocused
// pane draws its PTY content in Rec. 601 luminance grayscale. We feed a
// true-color red cell (SGR 38;2) and assert the sim screen cell is gray (r==g==b).
func TestPinnedTerminalPane_UnfocusedPaintedInGreyscale(t *testing.T) {
	src := make(chan []byte, 4)
	tp := terminalpane.New(src)
	defer func() { close(src); tp.Close() }()

	p := newPinnedTerminalPane(tp, 78, 22)
	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	// True-color red fg. After greying: luma = (299*200 + 587*30 + 114*30)/1000 = 80.
	feedPane(t, tp, src, []byte("\x1b[38;2;200;30;30mA\x1b[0m"))

	// pane is unfocused (no app.SetFocus called).
	if p.HasFocus() {
		t.Skip("pane unexpectedly has focus; test requires unfocused state")
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)
	sim.Show()

	cells, cw, _ := sim.GetContents()
	// "A" paints at emulator (col=0,row=0) → screen (col=1,row=1) with 1-cell border.
	cell := cells[1*cw+1]
	fg, _, _ := cell.Style.Decompose()
	assertGray(t, fg)
}

// TestPinnedTerminalPane_FocusedPaintedInColor verifies that a focused pane
// draws its PTY content in full color (no greyscale applied).
func TestPinnedTerminalPane_FocusedPaintedInColor(t *testing.T) {
	src := make(chan []byte, 4)
	tp := terminalpane.New(src)
	defer func() { close(src); tp.Close() }()

	p := newPinnedTerminalPane(tp, 78, 22)
	const allocW, allocH = 80, 24
	p.SetRect(0, 0, allocW, allocH)

	// True-color red: after greying would be ~(80,80,80); focused must keep
	// the original (200,30,30) with r != g.
	feedPane(t, tp, src, []byte("\x1b[38;2;200;30;30mA\x1b[0m"))

	app := tview.NewApplication()
	app.SetFocus(p)
	if !p.HasFocus() {
		t.Fatal("pane must have focus for this test")
	}

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(allocW, allocH)
	p.Draw(sim)
	sim.Show()

	cells, cw, _ := sim.GetContents()
	cell := cells[1*cw+1]
	fg, _, _ := cell.Style.Decompose()
	if !fg.Valid() {
		t.Skip("cell has ColorDefault fg; unable to verify non-gray color")
	}
	r, g, b := fg.RGB()
	if r < 0 {
		t.Skip("color not resolvable to RGB; skipping color check")
	}
	if r == g && g == b {
		t.Errorf("focused pane has gray fg rgb(%d,%d,%d) — greyscale was incorrectly applied to focused pane", r, g, b)
	}
}

// assertGray passes when c is ColorDefault (preserved as-is) or a true gray (r==g==b).
func assertGray(t *testing.T, c tcell.Color) {
	t.Helper()
	if !c.Valid() {
		return // ColorDefault — preserved unchanged, acceptable
	}
	r, g, b := c.RGB()
	if r != g || g != b {
		t.Fatalf("expected gray (r==g==b), got rgb(%d,%d,%d)", r, g, b)
	}
}

// --- BUG-012 clean-slate reattach ---

// SetReattaching(true) must stamp reattachSince so App.OnTaskReattached can
// enforce the minimum 1-second splash hold. A zero reattachSince would make
// time.Since return a huge elapsed duration, bypassing the hold entirely.
func TestPinnedTerminalPane_SetReattaching_StampsReattachSince(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	p := newPinnedTerminalPane(tp, 80, 24)

	// reattachSince must be zero before any reattach.
	if !p.reattachSince.IsZero() {
		t.Fatal("reattachSince must start at zero")
	}

	before := time.Now()
	p.SetReattaching(true, "connecting...")
	after := time.Now()

	if p.reattachSince.Before(before) || p.reattachSince.After(after) {
		t.Errorf("reattachSince = %v, want in [%v, %v]", p.reattachSince, before, after)
	}
	if !p.reattaching {
		t.Error("reattaching must be true after SetReattaching(true)")
	}

	// SetReattaching(false) must NOT reset reattachSince — the timestamp stays
	// so a delayed clearReattachAndResize can still check elapsed time after the
	// splash is cleared.
	p.SetReattaching(false, "")
	if p.reattachSince.IsZero() {
		t.Error("reattachSince must remain set after SetReattaching(false)")
	}
	if p.reattaching {
		t.Error("reattaching must be false after SetReattaching(false)")
	}
}

// Calling SetReattaching(true) multiple times must update reattachSince to the
// most recent call so the hold is measured from the last splash activation.
func TestPinnedTerminalPane_SetReattaching_UpdatesReattachSinceOnRepeat(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	p := newPinnedTerminalPane(tp, 80, 24)

	p.SetReattaching(true, "first")
	first := p.reattachSince

	time.Sleep(2 * time.Millisecond)

	p.SetReattaching(false, "")
	p.SetReattaching(true, "second")
	second := p.reattachSince

	if !second.After(first) {
		t.Errorf("second reattachSince (%v) must be after first (%v)", second, first)
	}
}
