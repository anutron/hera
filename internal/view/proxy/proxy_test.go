package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// fakeArgus is a minimal HTTP server that emulates argus's per-task output +
// stream endpoints. It serves a configurable snapshot on /output and emits a
// sequence of base64-encoded chunks on /stream, then sends `event: exit` so
// the proxy treats the stream as gracefully closed.
type fakeArgus struct {
	mu          sync.Mutex
	snapshot    []byte
	snapshotTot uint64

	streamOpens int32
	streamCalls chan streamCall

	chunks [][]byte
	exitAt int // index after which to emit `event: exit`; -1 means never.
	hangAt int // index at which to hang the stream forever; -1 means never.
	hangCh chan struct{}

	output404 bool
}

type streamCall struct {
	since uint64
}

func newFakeArgus() *fakeArgus {
	return &fakeArgus{
		streamCalls: make(chan streamCall, 16),
		exitAt:      -1,
		hangAt:      -1,
		hangCh:      make(chan struct{}),
	}
}

func (f *fakeArgus) setSnapshot(data []byte, total uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = data
	f.snapshotTot = total
}

func (f *fakeArgus) setChunks(chunks [][]byte, exitAt int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunks = chunks
	f.exitAt = exitAt
}

func (f *fakeArgus) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks/{id}/output", f.handleOutput)
	mux.HandleFunc("/api/tasks/{id}/stream", f.handleStream)
	return mux
}

func (f *fakeArgus) handleOutput(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.output404 {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no output available"}`)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Output-Total", strconv.FormatUint(f.snapshotTot, 10))
	_, _ = w.Write(f.snapshot)
}

func (f *fakeArgus) handleStream(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&f.streamOpens, 1)
	since := uint64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			since = n
		}
	}
	select {
	case f.streamCalls <- streamCall{since: since}:
	default:
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flusher.Flush()

	f.mu.Lock()
	chunks := f.chunks
	exitAt := f.exitAt
	hangAt := f.hangAt
	hangCh := f.hangCh
	f.mu.Unlock()

	// pos walks the argus byte position as chunks are emitted; chunks whose
	// end position is <= since are skipped (argus's stream returns bytes
	// [since, ∞]).
	pos := f.snapshotTot
	for i, c := range chunks {
		if hangAt >= 0 && i == hangAt {
			select {
			case <-hangCh:
				// hang released — continue delivering remaining chunks.
			case <-r.Context().Done():
				return
			}
		}
		end := pos + uint64(len(c))
		if end > since {
			deliver := c
			if pos < since {
				deliver = c[since-pos:]
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString(deliver))
			flusher.Flush()
		}
		pos = end
		if exitAt >= 0 && i == exitAt {
			_, _ = fmt.Fprintf(w, "event: exit\ndata: {}\n\n")
			flusher.Flush()
			return
		}
		// Tiny sleep so listeners can interleave delivery between chunks.
		time.Sleep(2 * time.Millisecond)
	}
	if exitAt < 0 {
		// No explicit exit configured: hold the connection until r.Context
		// is canceled by the client / test.
		<-r.Context().Done()
	}
}

func startFake(t *testing.T) (*fakeArgus, *argus.Client, func()) {
	t.Helper()
	f := newFakeArgus()
	srv := httptest.NewServer(f.handler())
	c := argus.New(srv.URL, "tok")
	return f, c, srv.Close
}

// readAll drains lst.Bytes up to want bytes or until timeout, returning what
// was collected. snapshot is concatenated first so callers can compare against
// a single expected byte slice.
func readAll(t *testing.T, lst *Listener, want int, timeout time.Duration) []byte {
	t.Helper()
	got := append([]byte{}, lst.Snapshot...)
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case b, ok := <-lst.Bytes:
			if !ok {
				return got
			}
			got = append(got, b...)
		case <-deadline:
			t.Fatalf("readAll timed out: got %d/%d bytes: %q", len(got), want, got)
		}
	}
	return got
}

// TestProxy_SnapshotThenStream verifies the canonical wire pattern: the
// snapshot is fetched, X-Output-Total is passed as since= on the stream, and
// stream bytes accumulate into the ring without overlap with the snapshot.
func TestProxy_SnapshotThenStream(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()

	f.setSnapshot([]byte("SNAP"), 100)
	f.setChunks([][]byte{[]byte("hi"), []byte("!")}, 1)

	sub := NewSubscription(context.Background(), c, "t1")
	defer sub.Close()

	lst := sub.Subscribe()
	got := readAll(t, lst, 7, 2*time.Second)
	if string(got) != "SNAPhi!" {
		t.Fatalf("got %q, want %q", string(got), "SNAPhi!")
	}

	// Stream since= must match snapshot total.
	select {
	case call := <-f.streamCalls:
		if call.since != 100 {
			t.Fatalf("stream since=%d, want 100", call.since)
		}
	case <-time.After(time.Second):
		t.Fatalf("no stream call observed")
	}
}

// TestProxy_404SnapshotStartsStreamAtZero verifies the empty-snapshot case
// produces since=0 on the stream open. Confirms the "no output available"
// 404 response is treated as a benign empty snapshot.
func TestProxy_404SnapshotStartsStreamAtZero(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()
	f.output404 = true
	f.setChunks([][]byte{[]byte("x")}, 0)

	sub := NewSubscription(context.Background(), c, "t1")
	defer sub.Close()
	lst := sub.Subscribe()
	got := readAll(t, lst, 1, 2*time.Second)
	if string(got) != "x" {
		t.Fatalf("got %q", string(got))
	}
	select {
	case call := <-f.streamCalls:
		if call.since != 0 {
			t.Fatalf("since=%d, want 0", call.since)
		}
	case <-time.After(time.Second):
		t.Fatalf("no stream call")
	}
}

// TestProxy_FanOutMultipleListeners asserts every listener registered before
// a chunk arrives sees that chunk exactly once.
func TestProxy_FanOutMultipleListeners(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()
	f.setSnapshot(nil, 0)
	f.setChunks([][]byte{[]byte("A"), []byte("B"), []byte("C")}, 2)

	sub := NewSubscription(context.Background(), c, "t1")
	defer sub.Close()

	const n = 4
	listeners := make([]*Listener, n)
	for i := 0; i < n; i++ {
		listeners[i] = sub.Subscribe()
	}

	// Each listener should see "ABC".
	for i, lst := range listeners {
		got := readAll(t, lst, 3, 2*time.Second)
		if string(got) != "ABC" {
			t.Fatalf("listener %d got %q, want ABC", i, string(got))
		}
	}
}

// TestProxy_LateSubscriberSeesSnapshotNotStreamOverlap verifies that a
// listener that subscribes mid-stream sees the current ring as its snapshot
// and only subsequent bytes on its channel — no duplication.
func TestProxy_LateSubscriberSeesSnapshotNotStreamOverlap(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()
	f.setSnapshot([]byte("START"), 5)
	// Three chunks, with a hang point so we can subscribe between bytes.
	f.mu.Lock()
	f.chunks = [][]byte{[]byte("aa"), []byte("bb"), []byte("cc")}
	f.exitAt = 2
	f.hangAt = 1 // hang before delivering chunk index 1 ("bb")
	f.mu.Unlock()

	sub := NewSubscription(context.Background(), c, "t1")
	defer sub.Close()

	first := sub.Subscribe()
	// First listener should see "START" + "aa".
	got := readAll(t, first, 7, 2*time.Second)
	if string(got) != "STARTaa" {
		t.Fatalf("first got %q, want STARTaa", string(got))
	}

	// Subscribe late. Snapshot must be "STARTaa". Channel must not yet have
	// "STARTaa" — only "bb" + "cc" once unhung.
	late := sub.Subscribe()
	if string(late.Snapshot) != "STARTaa" {
		t.Fatalf("late snapshot = %q, want STARTaa", string(late.Snapshot))
	}

	// Release the hang.
	close(f.hangCh)

	got = readAll(t, late, 11, 2*time.Second)
	if string(got) != "STARTaabbcc" {
		t.Fatalf("late got %q, want STARTaabbcc", string(got))
	}
}

// TestProxy_RingBounded asserts the local ring discards old bytes when it
// fills, mirroring the design.md D3 cap.
func TestProxy_RingBounded(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()
	// Build a 1 KiB chunk; with WithRingCapacity(512) we should retain only
	// the last 512 bytes.
	chunk := make([]byte, 1024)
	for i := range chunk {
		chunk[i] = byte('A' + (i % 26))
	}
	f.setSnapshot(nil, 0)
	f.setChunks([][]byte{chunk}, 0)

	sub := NewSubscription(context.Background(), c, "t1", WithRingCapacity(512))
	defer sub.Close()

	// Subscribe immediately so we collect the live byte; assertion is on the
	// late-subscriber snapshot which reflects the ring's bounded state.
	first := sub.Subscribe()
	_ = readAll(t, first, 1024, 2*time.Second)

	// Give ring write a moment to settle before the late snapshot read; the
	// readAll above guarantees Append fired, but the snapshot is taken under
	// the same mutex so this is safe immediately.
	late := sub.Subscribe()
	if len(late.Snapshot) != 512 {
		t.Fatalf("late snapshot len = %d, want 512", len(late.Snapshot))
	}
	// The retained tail should be the LAST 512 bytes of chunk.
	want := chunk[len(chunk)-512:]
	if string(late.Snapshot) != string(want) {
		t.Fatalf("late snapshot tail mismatch")
	}
}

// TestProxy_CloseTearsDownListeners verifies Close closes every listener's
// channel and stops the upstream goroutine.
func TestProxy_CloseTearsDownListeners(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()
	f.setSnapshot(nil, 0)
	// Long-lived chunks: don't exit so Close has to interrupt.
	f.setChunks([][]byte{[]byte("x")}, -1)

	sub := NewSubscription(context.Background(), c, "t1")
	lst := sub.Subscribe()
	// Drain to confirm we're attached.
	_ = readAll(t, lst, 1, 2*time.Second)

	sub.Close()
	// Channel must close.
	select {
	case _, ok := <-lst.Bytes:
		// Either a final drained byte (ok=true) or the closure (ok=false).
		// Loop until closure.
		if ok {
			for {
				select {
				case _, ok := <-lst.Bytes:
					if !ok {
						return
					}
				case <-time.After(time.Second):
					t.Fatalf("channel did not close after Close")
				}
			}
		}
	case <-time.After(time.Second):
		t.Fatalf("channel did not close within 1s")
	}
}

// TestProxy_UnsubscribeStopsDelivery verifies Unsubscribe removes a listener
// from fan-out without affecting other listeners or the upstream.
func TestProxy_UnsubscribeStopsDelivery(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()
	f.setSnapshot(nil, 0)
	f.mu.Lock()
	f.chunks = [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	f.exitAt = 2
	f.hangAt = 1 // hang after delivering "a", before "b"
	f.mu.Unlock()

	sub := NewSubscription(context.Background(), c, "t1")
	defer sub.Close()
	a := sub.Subscribe()
	b := sub.Subscribe()

	// Drain "a" off both listeners before releasing the hang.
	pull := func(lst *Listener, want string, timeout time.Duration) []byte {
		deadline := time.After(timeout)
		var got []byte
		for len(got) < len(want) {
			select {
			case bts, ok := <-lst.Bytes:
				if !ok {
					return got
				}
				got = append(got, bts...)
			case <-deadline:
				return got
			}
		}
		return got
	}
	if got := pull(a, "a", 2*time.Second); string(got) != "a" {
		t.Fatalf("a got %q", string(got))
	}
	if got := pull(b, "a", 2*time.Second); string(got) != "a" {
		t.Fatalf("b got %q", string(got))
	}

	sub.Unsubscribe(a)
	close(f.hangCh)

	// b sees the remaining chunks "bc".
	if got := pull(b, "bc", 2*time.Second); string(got) != "bc" {
		t.Fatalf("b post-unsubscribe got %q, want bc", string(got))
	}
	// a's channel must close.
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-a.Bytes:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("a channel did not close after Unsubscribe")
		}
	}
}

// TestProxy_StreamReconnectAfterError verifies a stream that errors out is
// reopened, with the snapshot re-fetched to bound the ring against gaps.
func TestProxy_StreamReconnectAfterError(t *testing.T) {
	var streamCount atomic.Int32
	var snapCount atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks/{id}/output", func(w http.ResponseWriter, r *http.Request) {
		snapCount.Add(1)
		w.Header().Set("X-Output-Total", "0")
		_, _ = io.WriteString(w, "")
	})
	mux.HandleFunc("/api/tasks/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		n := streamCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flusher.Flush()
		if n == 1 {
			// Send one byte then return a partial response (close the
			// hijack-style writer to simulate a network blip).
			_, _ = fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString([]byte("X")))
			flusher.Flush()
			// Returning without `event: exit` leaves the scanner with EOF —
			// the proxy treats it the same as a clean close + reconnects.
			return
		}
		// Second open: send a byte and then exit cleanly.
		_, _ = fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString([]byte("Y")))
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "event: exit\ndata: {}\n\n")
		flusher.Flush()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := argus.New(srv.URL, "tok")

	// Tight initial backoff so the test doesn't block on the 250ms default.
	sub := NewSubscription(context.Background(), c, "t1")
	defer sub.Close()
	lst := sub.Subscribe()
	got := readAll(t, lst, 2, 5*time.Second)
	if !strings.Contains(string(got), "X") || !strings.Contains(string(got), "Y") {
		t.Fatalf("expected both X and Y, got %q", string(got))
	}
	if streamCount.Load() < 2 {
		t.Fatalf("expected >= 2 stream opens, got %d", streamCount.Load())
	}
	if snapCount.Load() < 2 {
		t.Fatalf("expected snapshot re-fetched on reconnect, got %d", snapCount.Load())
	}
}

// TestProxy_SubscribeAfterCloseReturnsClosedChannel asserts the no-leak
// invariant: subscribing after Close still returns a Listener but its
// channel is already closed and contains no events.
func TestProxy_SubscribeAfterCloseReturnsClosedChannel(t *testing.T) {
	f, c, stop := startFake(t)
	defer stop()
	f.setSnapshot([]byte("z"), 1)
	f.setChunks(nil, 0)

	sub := NewSubscription(context.Background(), c, "t1")
	sub.Close()

	lst := sub.Subscribe()
	if _, ok := <-lst.Bytes; ok {
		t.Fatalf("Bytes channel should be closed immediately on Subscribe-after-Close")
	}
}
