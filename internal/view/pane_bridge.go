package view

import (
	"context"
	"time"

	"github.com/anutron/argus-sdk/terminalpane"
)

// paneBridge owns the source channel feeding a terminalpane plus the
// pump goroutine that copies an initial snapshot + an upstream chan onto
// that source. The bridge layer exists because terminalpane.New takes a
// single source channel at construction and has no Replace; hera needs
// to splice "this taskID's ring snapshot + its live byte stream" into a
// fresh pane per binding.
//
// Cancelling the bridge stops the pump (closing the source channel),
// which in turn lets the terminalpane consumer goroutine exit cleanly.
type paneBridge struct {
	out    chan []byte
	ctx    context.Context
	cancel context.CancelFunc
}

// newPaneBridge constructs a bridge WITHOUT starting its pump, so the
// caller can finish wiring the consuming terminalpane (the OnNeedRedraw
// hook) before any chunk flows. terminalpane.New starts its consume
// goroutine immediately, but that goroutine only reads the hook after
// receiving a chunk from out — the channel send/receive orders the hook
// assignment ahead of the read, closing the construction race the
// start-immediately shape had.
func newPaneBridge() *paneBridge {
	ctx, cancel := context.WithCancel(context.Background())
	return &paneBridge{out: make(chan []byte, 64), ctx: ctx, cancel: cancel}
}

// startPump spawns the bridge's pump goroutine. The pump emits, in
// order: snapshot bytes (if any), placeholder bytes (only when no
// upstream and no snapshot), then every chunk read from upstream. When
// upstream is closed or the bridge is cancelled, the source channel is
// closed.
func (b *paneBridge) startPump(snapshot []byte, upstream <-chan []byte, placeholder string) {
	go pumpPaneBridge(b.ctx, b.out, snapshot, upstream, placeholder)
}

// stop cancels the bridge. Safe to call multiple times.
func (b *paneBridge) stop() {
	if b == nil || b.cancel == nil {
		return
	}
	b.cancel()
}

func pumpPaneBridge(ctx context.Context, out chan []byte, snapshot []byte, upstream <-chan []byte, placeholder string) {
	defer close(out)

	send := func(b []byte) bool {
		if len(b) == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case out <- b:
			return true
		}
	}

	// One stateful OSC scrubber for the pane's whole byte stream: the SDK
	// terminalpane emulator has no OSC handling (and inherits the upstream
	// 0x9C-terminator parser bug — see oscFilter), so OSC set-title payloads
	// would paint as ghost input text at the prompt line. Filtering snapshot
	// and live chunks through the SAME instance strips sequences split across
	// any chunk boundary, including snapshot→live. send() drops chunks the
	// filter empties out.
	var scrub oscFilter

	if len(snapshot) > 0 {
		if !send(scrub.filter(snapshot)) {
			return
		}
	} else if upstream == nil && placeholder != "" {
		if !send([]byte(placeholder)) {
			return
		}
	}

	if upstream == nil {
		<-ctx.Done()
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-upstream:
			if !ok {
				return
			}
			if !send(scrub.filter(chunk)) {
				return
			}
		}
	}
}

// newBoundPane builds a terminalpane wrapped around a fresh paneBridge,
// then wraps that terminalpane in a pinnedTerminalPane so the emulator
// surface tracks the size hera negotiated with argus for this pane's
// worker PTY (Option 1: hera tells argus the desired size via POST
// /api/tasks/{id}/size and the worker re-renders at that width).
//
// taskID == "" produces a detached pane carrying placeholder text and
// pinned to the default 80x24 surface (no upstream to query). The
// returned unsub releases the proxy subscription registered for taskID
// (no-op when no subscription was opened).
//
// The construction-time pinned size is the queried PTY size from argus.
// It's only relevant as a transient until the first Draw learns the real
// inner rect — at which point the pinned pane both pins to that size
// and asks argus to resize the worker PTY to match (via src.ResizeTask,
// dispatched on a background goroutine inside the pane source). The
// queried-size fallback covers the path where src has no ResizeTask sink
// or argus reports 404 / failure: the wider worker PTY just letterboxes
// inside hera's narrower allocation.
//
// redraw is wired to the terminalpane's OnNeedRedraw hook, which the SDK
// fires once per non-empty inbound chunk AFTER the emulator has ingested
// it. That is what makes the pane repaint live on PTY output independent
// of keystroke input: echoed keystrokes AND autonomous agent output both
// schedule a redraw. (Before raw_input.go forwarded pane keystrokes as raw
// bytes, each keystroke incidentally drove a tcell→tview redraw; that path
// is gone, so output must drive its own repaint.) A nil redraw is tolerated
// (detached panes, tests, the pre-app-loop window).
func newBoundPane(title, placeholder, taskID string, src PaneSource, redraw func()) (*pinnedTerminalPane, *paneBridge, func()) {
	var cols, rows int
	if taskID != "" && src != nil {
		cols, rows = src.TaskSize(taskID)
	}
	return newBoundPaneAt(title, placeholder, taskID, src, redraw, cols, rows)
}

// newBoundPaneAt is like newBoundPane but uses the caller-supplied cols/rows
// for the initial emulator dimensions instead of querying src.TaskSize.
// Used for scrollback reflow (BUG-038): when the pane dimensions change, the
// App replays the ring buffer snapshot through a fresh emulator at the new
// size so scrollback wraps at the new width rather than persisting
// old narrow-terminal line wrapping.
//
// The call waits briefly for the terminalpane's consume goroutine to
// ingest the first chunk (snapshot or placeholder) so the first Draw
// after the pane is wired sees the initial cells already painted. The wait
// is bounded; if the terminalpane never catches up the function returns
// anyway — Draw will simply show an empty grid until later frames land.
func newBoundPaneAt(title, placeholder, taskID string, src PaneSource, redraw func(), cols, rows int) (*pinnedTerminalPane, *paneBridge, func()) {
	var snap []byte
	var upstream <-chan []byte
	unsub := func() {}
	var resizer paneResizer
	if taskID != "" && src != nil {
		snap, upstream, unsub = src.SubscribeTask(taskID)
		if unsub == nil {
			unsub = func() {}
		}
		resizer = src
	}

	// Construct the bridge but do NOT start pumping until the terminalpane
	// is fully wired: terminalpane.New spawns its consume goroutine right
	// away, and that goroutine reads OnNeedRedraw on every chunk — so the
	// hook MUST be assigned before the first chunk is sent (the channel
	// send/receive then orders the write ahead of the read; assigning after
	// the pump started was a data race with the consume goroutine).
	bridge := newPaneBridge()
	tp := terminalpane.New(bridge.out)
	tp.SetTitle(title)
	if redraw != nil {
		// Repaint the pane whenever the emulator ingests a new chunk, so PTY
		// output paints live without depending on a keystroke to force a draw.
		tp.OnNeedRedraw = redraw
	}
	pinned := newBoundPinnedTerminalPane(tp, cols, rows, taskID, resizer)
	bridge.startPump(snap, upstream, placeholder)

	if len(snap) > 0 || (upstream == nil && placeholder != "") {
		waitForInitialChunk(tp)
	}

	return pinned, bridge, unsub
}

// waitForInitialChunk polls Touched until it observes the consumer goroutine
// processed at least one chunk. Bounded by a small deadline so a stuck
// consumer doesn't hang BuildApp.
func waitForInitialChunk(tp *terminalpane.TerminalPane) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for tp.Touched() == 0 && time.Now().Before(deadline) {
		time.Sleep(1 * time.Millisecond)
	}
}
