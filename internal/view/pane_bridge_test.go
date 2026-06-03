package view

import (
	"bytes"
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

// TestPumpPaneBridge_ScrubsOSCFromSnapshotAndLiveStream verifies the pump
// strips OSC sequences from BOTH the bind-time ring snapshot and the live
// upstream chunks — including a sequence split across the snapshot→live
// boundary — while ordinary text and SGR escapes pass through untouched.
//
// Regression guard for the ghost-title leak: Claude Code's OSC set-title
// payload painted as phantom input text at the prompt line because the
// argus-sdk terminalpane emulator has no OSC handling; the pump is hera's
// chokepoint where the scrub must happen.
func TestPumpPaneBridge_ScrubsOSCFromSnapshotAndLiveStream(t *testing.T) {
	// Snapshot carries a complete OSC title AND ends mid-OSC (a second title
	// whose payload continues in the first live chunk).
	snapshot := []byte("snap\x1b]0;Snapshot Title\x07\x1b[32mgreen\x1b]2;Split Ti")
	upstream := make(chan []byte, 4)

	bridge := newPaneBridge()
	bridge.startPump(snapshot, upstream, "")
	defer bridge.stop()

	upstream <- []byte("tle\x1b\\live")
	upstream <- []byte(" tail\x1b]0;Another\x07end")
	close(upstream)

	var got []byte
	for chunk := range bridge.out {
		got = append(got, chunk...)
	}

	want := []byte("snap\x1b[32mgreenlive tailend")
	if !bytes.Equal(got, want) {
		t.Fatalf("pump output:\n got %q\nwant %q", got, want)
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
