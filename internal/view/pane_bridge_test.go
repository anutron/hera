package view

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestNewBoundPane_RedrawFiresOnUpstreamChunk verifies that a chunk arriving on
// the bound task's upstream channel triggers the redraw callback — the pane
// repaints on PTY output independent of any keystroke input.
//
// Regression guard for the live-repaint fix: raw_input.go now swallows pane
// keystrokes before tcell, which removed the incidental keystroke-driven
// redraw that used to repaint the latest emulator content. PTY output (echoed
// keystrokes AND autonomous agent output) must drive its own repaint via the
// terminalpane's OnNeedRedraw hook wired here.
func TestNewBoundPane_RedrawFiresOnUpstreamChunk(t *testing.T) {
	ch := make(chan []byte, 4)
	src := &fakePaneSource{
		channels: map[string]chan []byte{"t-1": ch},
	}

	var redraws atomic.Int64
	pane, bridge, unsub := newBoundPane("Agent", "(none)", "t-1", src, func() {
		redraws.Add(1)
	})
	defer unsub()
	defer bridge.stop()
	defer pane.Close()

	ch <- []byte("hello")

	deadline := time.Now().Add(2 * time.Second)
	for redraws.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := redraws.Load(); got == 0 {
		t.Fatal("expected redraw callback to fire on upstream PTY chunk; it never fired (output would not repaint live)")
	}
}

// TestNewBoundPane_NilRedrawSafe verifies a nil redraw callback is tolerated
// (e.g. tests / detached panes built before the tview app exists).
func TestNewBoundPane_NilRedrawSafe(t *testing.T) {
	ch := make(chan []byte, 4)
	src := &fakePaneSource{
		channels: map[string]chan []byte{"t-2": ch},
	}

	pane, bridge, unsub := newBoundPane("Agent", "(none)", "t-2", src, nil)
	defer unsub()
	defer bridge.stop()
	defer pane.Close()

	// Must not panic when a chunk is processed with no redraw wired.
	ch <- []byte("hello")
	time.Sleep(20 * time.Millisecond)
}
