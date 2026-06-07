package proxy

import (
	"context"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// Fetcher is the subset of *argus.Client the proxy needs. It exists so tests
// can drive the proxy with a thin in-package fake — production code passes a
// real *argus.Client.
type Fetcher interface {
	GetTaskOutput(ctx context.Context, taskID string) (argus.TaskOutputSnapshot, error)
	StreamTaskOutput(ctx context.Context, taskID string, since uint64, handler argus.TaskOutputHandler) error
	GetTaskSize(ctx context.Context, taskID string) (cols, rows int, err error)
	ResizeTask(ctx context.Context, taskID string, cols, rows int) error
	GetTask(ctx context.Context, taskID string) (*argus.Task, error)
}

// DefaultListenerBufferSize is the per-listener channel depth used when no
// override is supplied. Snapshot replays can burst up to DefaultRingCapacity
// bytes; 128 chunks at ~2-4 KiB/chunk covers typical snapshot sizes and keeps
// the channel from dropping under normal load. When full, new bytes are dropped
// on the floor rather than blocking the upstream — the listener can resync via
// a fresh Subscribe() if it falls far behind.
const DefaultListenerBufferSize = 128

// reconnectBackoffInitial / Max bound the SSE reconnect cadence. The proxy
// reconnects on any non-context error from StreamTaskOutput so the ring
// stays current across argus restarts. Backoff resets after a successful
// receive of at least one byte (the upstream loop tracks this).
const (
	reconnectBackoffInitial = 250 * time.Millisecond
	reconnectBackoffMax     = 10 * time.Second
)

// Listener is one fan-out consumer of a Subscription. Snapshot holds the
// ring contents at the moment Subscribe was called; Bytes delivers every
// subsequent chunk appended to the ring. The channel is closed when the
// Subscription is closed.
type Listener struct {
	Snapshot []byte
	Bytes    <-chan []byte
	ch       chan []byte
}

// Subscription is one per-task PTY pipeline. It owns the upstream snapshot +
// SSE loop, a local ring buffer, and the fan-out registry of listeners. Use
// NewSubscription to construct one and Subscribe to attach a listener.
type Subscription struct {
	taskID  string
	fetcher Fetcher

	ctx    context.Context
	cancel context.CancelFunc

	bufSize int

	mu        sync.Mutex
	ring      *ring
	listeners map[*Listener]struct{}
	closed    bool

	// localTotal tracks the highest argus byte position we have appended.
	// Used to deduplicate snapshot bytes on reconnect: a fresh /output call
	// that reports the same X-Output-Total we already consumed contributes
	// zero new bytes.
	localTotal uint64

	// errFn is an optional error callback used by tests; nil in production.
	errFn func(error)

	wg sync.WaitGroup
}

// Option customizes a Subscription.
type Option func(*Subscription)

// WithRingCapacity overrides the default 256 KiB ring buffer cap.
func WithRingCapacity(n int) Option {
	return func(s *Subscription) {
		s.ring = newRing(n)
	}
}

// WithListenerBufferSize overrides the per-listener channel depth.
func WithListenerBufferSize(n int) Option {
	return func(s *Subscription) {
		if n > 0 {
			s.bufSize = n
		}
	}
}

// WithErrorFunc routes upstream errors to a callback (test hook).
func WithErrorFunc(fn func(error)) Option {
	return func(s *Subscription) { s.errFn = fn }
}

// NewSubscription constructs a Subscription for taskID, starts the upstream
// snapshot+SSE goroutine, and returns. The caller MUST call Close to release
// the goroutine and HTTP connections. The parent ctx bounds the upstream
// lifetime; canceling parent ctx is equivalent to calling Close.
func NewSubscription(parent context.Context, fetcher Fetcher, taskID string, opts ...Option) *Subscription {
	ctx, cancel := context.WithCancel(parent)
	s := &Subscription{
		taskID:    taskID,
		fetcher:   fetcher,
		ctx:       ctx,
		cancel:    cancel,
		bufSize:   DefaultListenerBufferSize,
		ring:      newRing(DefaultRingCapacity),
		listeners: make(map[*Listener]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.wg.Add(1)
	go s.run()
	return s
}

// Subscribe registers a new listener and returns the current ring snapshot
// alongside a channel of subsequent byte chunks. The snapshot and the
// channel are consistent: bytes already in the snapshot will NOT be
// re-delivered on the channel.
//
// If the Subscription is already closed, the returned Listener's channel is
// already closed and the snapshot is the final ring contents at close time.
func (s *Subscription) Subscribe() *Listener {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, _ := s.ring.Snapshot()
	ch := make(chan []byte, s.bufSize)
	lst := &Listener{
		Snapshot: snap,
		Bytes:    ch,
		ch:       ch,
	}
	if s.closed {
		close(ch)
		return lst
	}
	s.listeners[lst] = struct{}{}
	return lst
}

// Unsubscribe removes lst from the fan-out registry and closes its channel.
// Safe to call repeatedly.
func (s *Subscription) Unsubscribe(lst *Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.listeners[lst]; ok {
		delete(s.listeners, lst)
		close(lst.ch)
	}
}

// Close stops the upstream goroutine and closes every listener's channel.
// Idempotent.
func (s *Subscription) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	for lst := range s.listeners {
		close(lst.ch)
		delete(s.listeners, lst)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// TaskID returns the argus task id this subscription serves.
func (s *Subscription) TaskID() string { return s.taskID }

// append writes p to the ring and fans out a copy to every registered
// listener. Drop-on-full keeps the upstream loop responsive when a listener
// stalls.
func (s *Subscription) append(p []byte) {
	if len(p) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.ring.Append(p)
	s.localTotal += uint64(len(p))
	for lst := range s.listeners {
		cp := make([]byte, len(p))
		copy(cp, p)
		select {
		case lst.ch <- cp:
		default:
			// Listener is slow; drop. The listener can resync via a new
			// Subscribe call which will deliver the up-to-date snapshot.
		}
	}
}

// applySnapshot appends only the suffix of snap that extends beyond what we
// have already consumed via the prior stream attach. Mirrors argus's snapshot
// semantics: the X-Output-Total cursor is the argus-side byte position the
// bytes end at; subtracting len(data) gives the start position. After apply,
// localTotal is pinned to snap.Total so the next stream open resumes there
// even if our local ring contains fewer bytes (e.g., argus's on-disk log
// reaches further back than our 256 KiB ring; the gap is invisible to
// downstream listeners but the cursor stays consistent).
func (s *Subscription) applySnapshot(snap argus.TaskOutputSnapshot) {
	if snap.Total == 0 && len(snap.Data) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if snap.Total <= s.localTotal {
		return
	}
	start := snap.Total - uint64(len(snap.Data))
	var bytesToAppend []byte
	if s.localTotal <= start {
		bytesToAppend = snap.Data
	} else {
		// Snapshot's range partially overlaps our consumed range. Append
		// only the new tail. This branch fires on reconnect when the prior
		// stream had already advanced localTotal past the snapshot's first
		// byte.
		offset := s.localTotal - start
		bytesToAppend = snap.Data[offset:]
	}
	if len(bytesToAppend) > 0 {
		s.ring.Append(bytesToAppend)
		for lst := range s.listeners {
			cp := make([]byte, len(bytesToAppend))
			copy(cp, bytesToAppend)
			select {
			case lst.ch <- cp:
			default:
			}
		}
	}
	s.localTotal = snap.Total
}

// streamSince returns the cursor to send as ?since= on the next stream
// open. Reads localTotal under the proxy lock.
func (s *Subscription) streamSince() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localTotal
}

// reportErr surfaces an upstream error to the optional callback. Tests use
// this to assert reconnect behavior; production code lets the upstream
// loop retry silently.
func (s *Subscription) reportErr(err error) {
	if s.errFn != nil {
		s.errFn(err)
	}
}

// run is the upstream loop. It fetches the snapshot to seed the ring, opens
// the SSE stream resumed at the snapshot's cursor, and feeds incoming bytes
// into the ring. On any non-context error it retries with bounded backoff.
func (s *Subscription) run() {
	defer s.wg.Done()

	backoff := reconnectBackoffInitial
	for {
		if s.ctx.Err() != nil {
			return
		}
		snap, err := s.fetcher.GetTaskOutput(s.ctx, s.taskID)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.reportErr(err)
			if !s.sleep(backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		s.applySnapshot(snap)

		received := false
		err = s.fetcher.StreamTaskOutput(s.ctx, s.taskID, s.streamSince(), func(chunk []byte) {
			received = true
			s.append(chunk)
		})
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			s.reportErr(err)
		}
		if received {
			backoff = reconnectBackoffInitial
		}
		if !s.sleep(backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

// sleep blocks for d or until ctx is done. Returns false if ctx is done.
func (s *Subscription) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > reconnectBackoffMax {
		d = reconnectBackoffMax
	}
	return d
}
