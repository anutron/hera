package view

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/anutron/argus-sdk/terminalpane"
)

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
