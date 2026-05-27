package view

import (
	"github.com/anutron/argus-sdk/terminalpane"
	"github.com/gdamore/tcell/v2"
)

// defaultPinnedCols / defaultPinnedRows are used when argus has no PTY size
// for a task (no active session, or no task bound). They match the SDK
// terminalpane's own default surface so an unbound pane renders sensibly
// without a roundtrip.
const (
	defaultPinnedCols = 80
	defaultPinnedRows = 24
)

// pinnedTerminalPane wraps an SDK terminalpane so its emulator surface is
// kept at the worker's real PTY size (queried from argus) instead of the
// tview inner rect.
//
// The SDK terminalpane's Draw calls Resize(inner.W, inner.H) on every
// invocation so a Flex layout shuffle just-works for normal nested
// widgets. In hera that's wrong: hera's coord column is narrower than the
// worker's PTY, so emulator-width-tracking-inner-rect makes the emulator
// wrap content that the worker emitted column-positioned for its own
// (wider) PTY. The result on-screen is long horizontal bar runs and
// vertical text spilling down the right edge.
//
// The fix is letterboxing: keep the emulator at the worker's PTY size and
// only paint the portion that fits inside hera's allocated rect. We do
// that without forking the SDK by lying about the rect for the duration
// of one Draw call (so the SDK's auto-resize lands on the pinned size)
// and wrapping the tcell screen so any cell writes outside hera's
// allocated rect are dropped.
type pinnedTerminalPane struct {
	*terminalpane.TerminalPane

	pinnedCols int
	pinnedRows int
}

// newPinnedTerminalPane wraps tp and pins its emulator surface to
// cols x rows. Values <= 0 fall back to defaultPinnedCols / defaultPinnedRows.
func newPinnedTerminalPane(tp *terminalpane.TerminalPane, cols, rows int) *pinnedTerminalPane {
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
// Any SetContent calls that the SDK Draw issues outside the allocated
// rect (right/bottom border pieces when pinned size > allocated, cells
// past the visible window) are discarded by clippingScreen. Cells that
// fit are painted normally.
func (p *pinnedTerminalPane) Draw(screen tcell.Screen) {
	x, y, w, h := p.GetRect()
	if w <= 0 || h <= 0 {
		return
	}

	// Borders consume one row/column on each side; the inner rect width
	// the SDK derives is (rect.W - 2). Setting rect.W = pinnedCols + 2
	// produces inner.W == pinnedCols, which is what tp.Resize is called
	// with inside SDK Draw.
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
