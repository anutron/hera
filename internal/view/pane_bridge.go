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
	cancel context.CancelFunc
}

// startPaneBridge constructs a bridge and spawns its pump goroutine. The
// pump emits, in order: snapshot bytes (if any), placeholder bytes (only
// when no upstream and no snapshot), then every chunk read from upstream.
// When upstream is closed or the bridge is cancelled, the source channel
// is closed.
func startPaneBridge(snapshot []byte, upstream <-chan []byte, placeholder string) *paneBridge {
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan []byte, 64)

	go pumpPaneBridge(ctx, out, snapshot, upstream, placeholder)

	return &paneBridge{out: out, cancel: cancel}
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

	if len(snapshot) > 0 {
		if !send(snapshot) {
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
			if !send(chunk) {
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
// The call waits briefly for the terminalpane's consume goroutine to
// ingest the first chunk (snapshot or placeholder) so the first Draw
// after BuildApp sees the initial cells already painted. The wait is
// bounded; if the terminalpane never catches up the function returns
// anyway — Draw will simply show an empty grid until later frames land.
func newBoundPane(title, placeholder, taskID string, src PaneSource) (*pinnedTerminalPane, *paneBridge, func()) {
	var snap []byte
	var upstream <-chan []byte
	unsub := func() {}
	var cols, rows int
	var resizer paneResizer
	if taskID != "" && src != nil {
		snap, upstream, unsub = src.SubscribeTask(taskID)
		if unsub == nil {
			unsub = func() {}
		}
		cols, rows = src.TaskSize(taskID)
		resizer = src
	}

	bridge := startPaneBridge(snap, upstream, placeholder)
	tp := terminalpane.New(bridge.out)
	tp.SetTitle(title)
	pinned := newBoundPinnedTerminalPane(tp, cols, rows, taskID, resizer)

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
