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

// newBoundPane builds a terminalpane wrapped around a fresh paneBridge.
// taskID == "" produces a detached pane carrying placeholder text. The
// returned unsub releases the proxy subscription registered for taskID
// (no-op when no subscription was opened).
//
// The call waits briefly for the terminalpane's consume goroutine to
// ingest the first chunk (snapshot or placeholder) so the first Draw
// after BuildApp sees the initial cells already painted. The wait is
// bounded; if the terminalpane never catches up the function returns
// anyway — Draw will simply show an empty grid until later frames land.
func newBoundPane(title, placeholder, taskID string, src PaneSource) (*terminalpane.TerminalPane, *paneBridge, func()) {
	var snap []byte
	var upstream <-chan []byte
	unsub := func() {}
	if taskID != "" && src != nil {
		snap, upstream, unsub = src.SubscribeTask(taskID)
		if unsub == nil {
			unsub = func() {}
		}
	}

	bridge := startPaneBridge(snap, upstream, placeholder)
	tp := terminalpane.New(bridge.out)
	tp.SetTitle(title)

	if len(snap) > 0 || (upstream == nil && placeholder != "") {
		waitForInitialChunk(tp)
	}

	return tp, bridge, unsub
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
