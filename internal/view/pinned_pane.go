package view

import (
	"github.com/anutron/argus-sdk/terminalpane"
	"github.com/anutron/argus-sdk/theme"
	"github.com/anutron/argus-sdk/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// defaultPinnedCols / defaultPinnedRows are used when argus has no PTY size
// for a task (no active session, or no task bound). They match the SDK
// terminalpane's own default surface so an unbound pane renders sensibly
// without a roundtrip.
const (
	defaultPinnedCols = 80
	defaultPinnedRows = 24
)

// paneResizer is the narrow capability newPinnedTerminalPane needs to
// inform argus that the worker PTY should track this pane's inner rect.
// In production, managerPaneSource (PaneSource) satisfies this; tests pass
// a recording fake.
type paneResizer interface {
	ResizeTask(taskID string, cols, rows int)
}

// pinnedTerminalPane wraps an SDK terminalpane so its emulator surface is
// kept at the size hera negotiated with argus for this pane's worker PTY,
// rather than letting the SDK auto-track the tview inner rect.
//
// Option 1 (this stage): the pane treats the tview inner rect as the
// authoritative pane allocation, asks argus to resize the worker PTY to
// match (POST /api/tasks/{id}/size), and pins the emulator at that same
// size so worker bytes line up cell-for-cell.
//
// Option 2 fallback (no taskID, no resizer wired, or argus 404 / failure):
// the emulator stays at the queried PTY size we got at construction and
// any cells outside the layout-allocated rect are dropped — the wider
// worker PTY just letterboxes inside the narrower hera pane.
//
// The SDK terminalpane's Draw calls Resize(inner.W, inner.H) on every
// invocation. We override that by lying about the rect for the duration
// of one Draw call (so the SDK's auto-resize lands on the pinned size)
// and wrapping the tcell screen so any cell writes outside hera's
// allocated rect are dropped.
type pinnedTerminalPane struct {
	*terminalpane.TerminalPane

	pinnedCols int
	pinnedRows int

	// taskID identifies the argus task this pane is bound to, for use
	// with the resizer. Empty when no task is bound (placeholder pane).
	taskID string

	// resizer dispatches POST /api/tasks/{id}/size on every alloc
	// change. nil when no upstream resize sink is wired (e.g., daemon
	// startup with the nil source, or in tests).
	resizer paneResizer

	// lastDesiredCols / lastDesiredRows record the most recent (cols,
	// rows) we asked the resizer to apply. Used to short-circuit
	// redundant calls when Draw fires repeatedly at the same allocation.
	lastDesiredCols int
	lastDesiredRows int

	// paneTitle mirrors the title passed to SetTitle so GetTitle can return it.
	// terminalpane.TerminalPane.SetTitle stores the title in an unexported field
	// that tview.Box.GetTitle cannot reach; this field bridges that gap (BUG-031).
	paneTitle string

	// Scrollback state lives in the embedded SDK pane (argus-sdk ≥ v0.0.3):
	// ScrollBy / ScrollOffset / ResetScroll are promoted from
	// *terminalpane.TerminalPane, which clamps the offset to the emulator's
	// retained history, anchor-locks the view under new output, and paints
	// the scrollback window plus a [SCROLL] badge while scrolled. Both the
	// ⇧↑/⇧↓ keys (App.ScrollFocusedPane) and the mouse wheel (App.applyWheel)
	// drive that engine. A rebind constructs a fresh pane (newBoundPane), so
	// a newly bound task always starts at the live screen.
}

// newPinnedTerminalPane wraps tp and pins its emulator surface to
// cols x rows. Values <= 0 fall back to defaultPinnedCols / defaultPinnedRows.
//
// The initial cols/rows are the construction-time fallback: argus's
// currently-reported PTY size from GET /api/tasks/{id}/size, or the
// 80x24 default when argus has no live session. The pinned size shifts
// to the layout-allocated inner rect on the first Draw (Option 1).
func newPinnedTerminalPane(tp *terminalpane.TerminalPane, cols, rows int) *pinnedTerminalPane {
	return newBoundPinnedTerminalPane(tp, cols, rows, "", nil)
}

// newBoundPinnedTerminalPane is newPinnedTerminalPane with the resizer +
// taskID wiring for Option 1 PTY-resize-on-bind. Production code paths
// call this from newBoundPane; the no-arg shim above stays for tests that
// only exercise the letterbox behavior.
func newBoundPinnedTerminalPane(tp *terminalpane.TerminalPane, cols, rows int, taskID string, resizer paneResizer) *pinnedTerminalPane {
	if cols <= 0 {
		cols = defaultPinnedCols
	}
	if rows <= 0 {
		rows = defaultPinnedRows
	}
	p := &pinnedTerminalPane{
		TerminalPane: tp,
		pinnedCols:   cols,
		pinnedRows:   rows,
		taskID:       taskID,
		resizer:      resizer,
		// Seed lastDesired with the construction-time pinned size so
		// the first Draw observing an inner rect equal to argus's
		// already-current PTY size short-circuits the resize call.
		// argus would also cache that dispatch as a no-op, but skipping
		// it locally avoids waking a goroutine and emitting the
		// "redundant" predicate trace.
		lastDesiredCols: cols,
		lastDesiredRows: rows,
	}
	// Pre-size the emulator now so the first frame after BuildApp paints
	// at the worker's PTY size rather than the SDK's 80x24 default.
	tp.Resize(cols, rows)
	return p
}

// PinnedSize returns the (cols, rows) the emulator is held at. Exposed for
// tests.
func (p *pinnedTerminalPane) PinnedSize() (int, int) {
	return p.pinnedCols, p.pinnedRows
}

// SetTitle forwards to the embedded TerminalPane (which renders the title in
// the pane border) and mirrors the value in p.paneTitle so GetTitle can return
// it — terminalpane stores the title in an unexported field that tview.Box's
// GetTitle cannot reach (BUG-031).
func (p *pinnedTerminalPane) SetTitle(t string) {
	p.paneTitle = t
	p.TerminalPane.SetTitle(t)
}

// GetTitle returns the pane title last set by SetTitle.
func (p *pinnedTerminalPane) GetTitle() string {
	return p.paneTitle
}

// Focus implements tview.Primitive by explicitly forwarding to the embedded
// TerminalPane. Without this override, Go's promotion would forward to
// TerminalPane.Box.Focus, which is the same underlying Box — but the explicit
// call makes the delegation contract clear and guards against a future
// TerminalPane.Focus override that might redirect the delegate elsewhere
// (breaking the invariant that TerminalPane.HasFocus() is true while the
// wrapper is the focused pane). The SDK's paint() gates ShowCursor on
// TerminalPane.HasFocus(), so this must be true for cursor rendering to work.
func (p *pinnedTerminalPane) Focus(delegate func(tview.Primitive)) {
	p.TerminalPane.Focus(delegate)
}

// Draw paints the emulator at its pinned surface size and clips the output
// to the layout-allocated rect.
//
// Mechanism: the SDK Draw uses GetRect() to size both the bordered panel
// and the emulator (via tp.Resize(inner.W, inner.H)). We mutate the
// embedded Box's rect to (x, y, pinnedCols+2, pinnedRows+2) for the
// duration of the SDK Draw — that's the size required for an inner rect
// of exactly pinnedCols x pinnedRows after the SDK's two-cell border
// margin. The mutated rect is restored before returning so the surrounding
// Flex layout never observes the lie.
//
// Option 1 PTY-resize: when this pane is bound to a task and a resizer
// is wired, Draw tells argus the worker PTY should be sized to the inner
// rect and re-pins the emulator surface to that same size. Subsequent
// worker bytes then line up cell-for-cell with hera's allocation, so no
// content needs to be clipped. The resizer dedupes redundant calls; we
// additionally skip the dispatch when the inner rect hasn't changed
// since the previous Draw.
//
// When no taskID / resizer is wired (Option 2 fallback), the emulator
// stays at its construction-time pinned size and any SetContent calls
// the SDK Draw issues outside the allocated rect (right/bottom border
// pieces when pinned size > allocated, cells past the visible window)
// are discarded by clippingScreen.
func (p *pinnedTerminalPane) Draw(screen tcell.Screen) {
	x, y, w, h := p.GetRect()
	if w <= 0 || h <= 0 {
		return
	}

	// Compute the inner rect the SDK would derive from this allocation.
	// Borders consume one row/column on each side; minimum 1x1 keeps
	// pathological tiny allocations from breaking the emulator math.
	innerCols := w - 2
	innerRows := h - 2
	if innerCols < 1 {
		innerCols = 1
	}
	if innerRows < 1 {
		innerRows = 1
	}

	// Option 1: when bound to a task with a resizer wired, ask argus to
	// resize the worker PTY to match this pane's inner rect, then track
	// the emulator surface to the same dimensions. Skip the dispatch
	// when the dimensions haven't changed since the previous Draw —
	// avoids waking a goroutine on every frame.
	switch {
	case p.taskID != "" && p.resizer != nil:
		if innerCols != p.lastDesiredCols || innerRows != p.lastDesiredRows {
			p.resizer.ResizeTask(p.taskID, innerCols, innerRows)
			p.lastDesiredCols = innerCols
			p.lastDesiredRows = innerRows
		}
		p.pinnedCols = innerCols
		p.pinnedRows = innerRows
	case p.taskID == "":
		// Unbound / placeholder pane (no task at all, e.g. "(no coord
		// selected)"): there is no worker PTY to letterbox, so the emulator
		// surface must track the full layout-allocated inner rect — filling its
		// Flex allocation exactly like a bound pane. Without this the pinned
		// size stays at the construction-time default (80x24) and the pane draws
		// at a fixed 82x26 box: shorter vertically and not splitting the
		// horizontal space evenly (BUG-003). No resizer dispatch on this path
		// (no PTY exists to size). NOTE: this is distinct from a BOUND pane with
		// no resizer wired (Option 2 letterbox, below) — that case has a real
		// (possibly wider) PTY whose surface must stay pinned so its content
		// clips rather than reflowing.
		p.pinnedCols = innerCols
		p.pinnedRows = innerRows
	}

	// Setting rect.W = pinnedCols + 2 produces inner.W == pinnedCols,
	// which is what tp.Resize is called with inside SDK Draw.
	p.SetRect(x, y, p.pinnedCols+2, p.pinnedRows+2)
	defer p.SetRect(x, y, w, h)

	clipped := &clippingScreen{
		Screen: screen,
		minX:   x,
		minY:   y,
		maxX:   x + w,
		maxY:   y + h,
	}
	p.TerminalPane.Draw(clipped)

	// The SDK terminalpane hardcodes a white (tcell.StyleDefault) border when
	// focused; hera mirrors argus's cyan focus border across the rail and both
	// panes, so repaint just the border lines (not the inner content) in
	// theme.ColorTitle when this pane holds focus. Drawn over the SDK's border
	// at the same allocated rect — for a bound pane the pinned surface equals
	// the allocation, so the cyan lines land exactly on the SDK's border cells.
	if p.HasFocus() {
		widget.DrawBorder(clipped, x, y, w, h, theme.StyleFocusedBorder)
	}
}

// clippingScreen wraps a tcell.Screen and drops SetContent writes that
// land outside the half-open rect [minX, maxX) x [minY, maxY). All other
// screen methods pass through to the underlying screen via the embedded
// interface.
type clippingScreen struct {
	tcell.Screen

	minX, minY, maxX, maxY int
}

func (c *clippingScreen) SetContent(x, y int, mainc rune, combc []rune, style tcell.Style) {
	if x < c.minX || x >= c.maxX || y < c.minY || y >= c.maxY {
		return
	}
	c.Screen.SetContent(x, y, mainc, combc, style)
}
