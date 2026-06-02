package view

import (
	"sync"
	"testing"

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

// TestPinnedTerminalPane_KeepsPinnedSizeAcrossDraw pins the core contract:
// the SDK's Draw auto-resizes the emulator to its inner rect, but our
// wrapper must override that so the emulator's surface stays at the
// upstream PTY size for downstream paint logic to clip from.
func TestPinnedTerminalPane_KeepsPinnedSizeAcrossDraw(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	const pinCols, pinRows = 189, 69
	p := newPinnedTerminalPane(tp, pinCols, pinRows)

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
// bookkeeping that backs the ⇧↑/⇧↓ scrollback keys (D15). ScrollBy moves the
// scrollback offset, clamping at 0 (the live screen — can't scroll past the
// newest content). A positive delta scrolls UP into history; the offset never
// goes negative.
//
// NOTE (flagged limitation): the SDK terminalpane renders only the live
// emulator screen via its unexported emulator and does NOT expose scrollback
// for rendering, so this offset is recorded but not yet painted. See ScrollBy's
// doc comment. Wiring true scrollback rendering requires an argus-sdk change to
// expose the emulator (or a ScrollbackCellAt accessor).
func TestPinnedTerminalPane_ScrollByTracksAndClamps(t *testing.T) {
	src := make(chan []byte)
	defer close(src)
	tp := terminalpane.New(src)
	defer tp.Close()

	p := newPinnedTerminalPane(tp, 80, 24)

	if got := p.ScrollOffset(); got != 0 {
		t.Fatalf("fresh pane scroll offset: want 0, got %d", got)
	}

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
	// try to paint a right vertical at x = pinCols+1.
	const pinCols, pinRows = 120, 30
	p := newPinnedTerminalPane(tp, pinCols, pinRows)

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
