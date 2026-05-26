// Package proxy implements per-task PTY snapshot + SSE fan-out for hera-view.
//
// Every live argus binding gets one upstream subscription that fetches the
// task's output snapshot, opens an SSE stream resumed at the snapshot's
// X-Output-Total cursor, and feeds the bytes into a bounded in-memory ring
// buffer. Multiple listeners (per-WebSocket pane subscribers) fan out from
// the same ring without re-issuing network calls.
package proxy

import "sync"

// DefaultRingCapacity matches argus's own per-session ring buffer cap
// (256 KiB). See design.md D3.
const DefaultRingCapacity = 256 * 1024

// ring is a single-task circular byte buffer. Newest bytes overwrite oldest
// once cap is reached. Total tracks the cumulative byte count ever written
// so snapshot readers can advertise a since-cursor for live attach.
type ring struct {
	mu    sync.Mutex
	buf   []byte // logical contents, len <= cap
	cap   int
	total uint64
}

func newRing(capacity int) *ring {
	if capacity <= 0 {
		capacity = DefaultRingCapacity
	}
	return &ring{
		buf: make([]byte, 0, capacity),
		cap: capacity,
	}
}

// Capacity returns the configured maximum byte count.
func (r *ring) Capacity() int { return r.cap }

// Append writes p to the ring, dropping oldest bytes when full.
func (r *ring) Append(p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total += uint64(len(p))

	// If p alone exceeds capacity, only the tail of p survives.
	if len(p) >= r.cap {
		r.buf = append(r.buf[:0], p[len(p)-r.cap:]...)
		return
	}

	// Drop just enough of the existing prefix to fit p.
	overflow := len(r.buf) + len(p) - r.cap
	if overflow > 0 {
		r.buf = r.buf[overflow:]
		// Compact so future appends reuse the same underlying array up to cap.
		// Without compacting, repeated drops would grow the slice header's
		// underlying array via append's reslice, leaking memory unboundedly.
		// Copy in place onto a fresh backing array sized to cap.
		newBuf := make([]byte, len(r.buf), r.cap)
		copy(newBuf, r.buf)
		r.buf = newBuf
	}
	r.buf = append(r.buf, p...)
}

// Snapshot returns an independent copy of the current contents and the
// cumulative byte count.
func (r *ring) Snapshot() ([]byte, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out, r.total
}

// Total returns the cumulative byte count ever appended.
func (r *ring) Total() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}
