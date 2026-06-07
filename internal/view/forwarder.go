package view

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/anutron/hera/internal/argus"
)

// PaneForwarder decouples pane-focus keystroke typing from the HTTP round-trip
// that delivers bytes to a task's PTY input endpoint. handlePane resolves the
// target task at key-press time and ENQUEUES {taskID, bytes} here, returning
// immediately so the tview input-handler goroutine never blocks on a POST. A
// single dedicated sender goroutine drains the queue in FIFO order (preserving
// key/byte order — critical for typing) and calls PostTaskInput.
//
// COALESCING: when consecutive queued items share the same taskID the sender
// batches them into a single PostTaskInput call (bytes concatenated in order),
// cutting round-trips for fast typing / paste. Items for DIFFERENT taskIDs are
// never merged — a focus change mid-stream still flushes the earlier task's
// bytes to the earlier task before the new target's bytes go out.
//
// DEAD-SESSION DETECTION: when PostTaskInput returns ErrNoTaskInput (HTTP 404)
// the forwarder calls onDead exactly once per task so the view layer can force
// focus back to RAIL. This covers the case where a session ends while the
// operator is actively typing in the pane — applyDeadFocusGuard only fires on
// rail selection changes, so without this signal keystrokes are silently dropped
// until the operator manually presses Ctrl-Q (BUG-006).
type PaneForwarder struct {
	ch     chan forwardItem
	poster InputPoster
	log    *slog.Logger

	stopOnce sync.Once
	done     chan struct{} // closed when the sender goroutine has exited
	quit     chan struct{} // closed by Stop to ask the sender to drain + exit

	// onDead, if set, is called exactly once per dead task (identified by HTTP
	// 404 from PostTaskInput). Invoked from the sender goroutine; implementations
	// MUST be goroutine-safe and MUST NOT block (they typically bounce to the
	// tview event loop via QueueUpdateDraw).
	onDead func(taskID string)

	// deadSeen tracks which task IDs have already triggered an onDead call so the
	// callback fires at most once per task per session (not on every failing POST).
	deadSeen sync.Map
}

type forwardItem struct {
	taskID  string
	payload []byte
}

// NewPaneForwarder starts the sender goroutine. ctx bounds the goroutine's
// lifetime (cancel tears it down); buf sizes the buffered enqueue channel
// (full-buffer policy below). poster and log fall back to safe defaults when
// nil. Call Stop on session teardown to terminate the goroutine cleanly.
func NewPaneForwarder(ctx context.Context, poster InputPoster, log *slog.Logger, buf int) *PaneForwarder {
	if log == nil {
		log = slog.Default()
	}
	if buf <= 0 {
		buf = 256
	}
	f := &PaneForwarder{
		ch:     make(chan forwardItem, buf),
		poster: poster,
		log:    log,
		done:   make(chan struct{}),
		quit:   make(chan struct{}),
	}
	go f.run(ctx)
	return f
}

// SetOnDead registers a callback invoked exactly once when a task's PTY returns
// HTTP 404 (argus.ErrNoTaskInput) — indicating the session ended while the
// operator was in the pane. fn must be goroutine-safe and non-blocking; it
// typically bounces to the tview event loop via QueueUpdateDraw. Setting nil
// clears the callback. Must be called before the first Enqueue (no locking).
func (f *PaneForwarder) SetOnDead(fn func(taskID string)) {
	if f == nil {
		return
	}
	f.onDead = fn
}

// Enqueue queues bytes for delivery to taskID's PTY. It NEVER blocks the
// caller (the UI event loop): the channel is buffered, and on the pathological
// case of a full buffer the OLDEST queued item is dropped to make room rather
// than blocking. Dropping oldest (vs newest) keeps the most recent keystrokes —
// the ones the operator is actively typing — and never deadlocks the UI. A
// dropped keystroke is logged at debug level. Enqueue after Stop / ctx-cancel
// is a safe no-op.
func (f *PaneForwarder) Enqueue(taskID string, payload []byte) {
	if f == nil || taskID == "" || len(payload) == 0 {
		return
	}
	item := forwardItem{taskID: taskID, payload: append([]byte(nil), payload...)}
	for {
		select {
		case <-f.done:
			return // sender gone (Stop or ctx-cancel); drop silently.
		case f.ch <- item:
			return
		default:
			// Buffer full: drop the oldest queued item to make room, then retry.
			select {
			case dropped := <-f.ch:
				f.log.Debug("view.forwarder: enqueue buffer full; dropped oldest keystroke",
					"task_id", dropped.taskID, "bytes", len(dropped.payload))
			default:
				// Another goroutine drained it first; loop and retry the send.
			}
		}
	}
}

// Stop signals the sender goroutine to stop and waits for it to exit. Safe to
// call multiple times. Queued items may or may not be flushed depending on
// timing — Stop prioritizes a prompt, leak-free teardown over draining (the
// session is going away, so undelivered keystrokes are moot).
func (f *PaneForwarder) Stop() {
	if f == nil {
		return
	}
	f.stopOnce.Do(func() { close(f.quit) })
	<-f.done
}

// run is the single sender goroutine. It drains f.ch in FIFO order, coalescing
// consecutive same-task items into one PostTaskInput call. It exits when the
// context is cancelled or Stop closes f.quit.
func (f *PaneForwarder) run(ctx context.Context) {
	defer close(f.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.quit:
			return
		case item := <-f.ch:
			f.flush(ctx, item)
		}
	}
}

// flush sends item, greedily coalescing any already-queued items that share the
// same taskID into the same POST. It stops coalescing at the first different
// taskID (that item is NOT consumed — it stays at the head of the channel for
// the next flush) or when the channel is momentarily empty.
func (f *PaneForwarder) flush(ctx context.Context, item forwardItem) {
	taskID := item.taskID
	buf := append([]byte(nil), item.payload...)

	// Greedily coalesce consecutive same-task items currently sitting in the
	// channel. A non-blocking receive: we only batch what's ALREADY queued, so
	// we never wait for more typing (which would add latency).
coalesce:
	for {
		select {
		case next := <-f.ch:
			if next.taskID == taskID {
				buf = append(buf, next.payload...)
				continue
			}
			// Different target: flush the current batch, then recurse to send
			// the just-received item (and coalesce ITS same-task successors).
			f.post(ctx, taskID, buf)
			f.flush(ctx, next)
			return
		default:
			break coalesce
		}
	}
	f.post(ctx, taskID, buf)
}

// post performs the round-trip and preserves the E1 failure-logging contract:
// a failed forward logs a warning carrying the task id, byte count, and error,
// so a keystroke that never reached the pane's PTY is diagnosable. Context
// cancellation during teardown is not logged as a failure.
//
// Dead-session detection: when PostTaskInput returns ErrNoTaskInput (HTTP 404),
// onDead is called exactly once per task so the view layer can force focus back
// to RAIL (BUG-006). Subsequent 404s for the same task are silently swallowed
// after the first notification — the operator is already being guided back to RAIL.
func (f *PaneForwarder) post(ctx context.Context, taskID string, payload []byte) {
	if f.poster == nil || len(payload) == 0 {
		return
	}
	_, err := f.poster.PostTaskInput(ctx, taskID, payload)
	if err == nil || ctx.Err() != nil {
		return
	}
	f.log.Warn("view.forwarder: forward keystroke to pane PTY failed",
		"task_id", taskID, "bytes", len(payload), "err", err)
	if errors.Is(err, argus.ErrNoTaskInput) {
		if _, alreadyNotified := f.deadSeen.LoadOrStore(taskID, struct{}{}); !alreadyNotified {
			if f.onDead != nil {
				f.onDead(taskID)
			}
		}
	}
}
