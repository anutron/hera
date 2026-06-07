// Package proxy implements per-task PTY snapshot + SSE fan-out for hera-view.
//
// Every live argus binding gets one upstream subscription that fetches the
// task's output snapshot, opens an SSE stream resumed at the snapshot's
// X-Output-Total cursor, and feeds the bytes into a bounded in-memory ring
// buffer. Multiple listeners (per-WebSocket pane subscribers) fan out from
// the same ring without re-issuing network calls.
package proxy

import "sync"

// DefaultRingCapacity is the per-task in-memory ring buffer cap.
//
// Sized at 4 MiB — 16× argus's own 256 KiB on-disk snapshot cap. The larger
// ring retains more frames when a pane is viewed after a task has been running
// for a while; the seeded SSE subscription accumulates output continuously, so
// long-lived tasks keep up to 4 MiB of recent history in the ring regardless
// of what the argus snapshot size allows on reconnect.
//
// The practical motivation: the argus-sdk terminalpane captures alt-screen
// frames to the main scrollback by intercepting \033[2J (ED2). Each Bubble Tea
// / Claude Code frame is ~20–50 KiB of raw terminal bytes, so the old 256 KiB
// ring only held ~9 frames. After those 9 frames the \033[?1049h alt-screen
// entry sequence was pushed out; replaying the snapshot through a fresh
// emulator left it in main-screen mode, causing the ED2 capture guard
// (IsAltScreen) to skip every frame and produce zero scrollback. 4 MiB holds
// ~80–200 frames — enough for typical usage sessions. The companion fix in
// argus-sdk removes the IsAltScreen guard entirely (blank-frame skipping is
// the correct safety valve), which eliminates the problem regardless of ring
// size. Until that SDK bump lands, the larger ring is the active mitigation.
const DefaultRingCapacity = 4 * 1024 * 1024

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
