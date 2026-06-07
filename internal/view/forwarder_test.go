package view

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// recordingPoster records every PostTaskInput call (taskID + payload) so the
// async PaneForwarder tests can assert order, coalescing, and routing. A
// per-call gate lets a test hold the sender goroutine mid-flight to drive the
// "items pile up while the sender is busy" coalescing scenario deterministically.
type recordingPoster struct {
	mu    sync.Mutex
	calls []postCall

	// gate, when non-nil, is received-from once per PostTaskInput call BEFORE
	// the call records, letting a test block the sender goroutine.
	gate chan struct{}
}

func (p *recordingPoster) PostTaskInput(_ context.Context, taskID string, payload []byte) (int, error) {
	if p.gate != nil {
		<-p.gate
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, postCall{TaskID: taskID, Payload: append([]byte(nil), payload...)})
	return len(payload), nil
}

func (p *recordingPoster) Calls() []postCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]postCall, len(p.calls))
	copy(out, p.calls)
	return out
}

// TestPaneForwarder_PreservesOrder proves bytes enqueued for the SAME task are
// POSTed in FIFO order — critical for typing (A,B,C must arrive as A,B,C).
func TestPaneForwarder_PreservesOrder(t *testing.T) {
	p := &recordingPoster{}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	defer f.Stop()

	f.Enqueue("t1", []byte("A"))
	f.Enqueue("t1", []byte("B"))
	f.Enqueue("t1", []byte("C"))

	if !waitForCond(2*time.Second, func() bool { return concatPayload(p.Calls(), "t1") == "ABC" }) {
		t.Fatalf("forwarder must POST A,B,C in order for t1; got %q", concatPayload(p.Calls(), "t1"))
	}
}

// TestPaneForwarder_NonBlockingEnqueue proves Enqueue returns promptly even
// when the sender goroutine is blocked on a slow PostTaskInput — the UI event
// loop must never wait on a round-trip.
func TestPaneForwarder_NonBlockingEnqueue(t *testing.T) {
	gate := make(chan struct{})
	p := &recordingPoster{gate: gate}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	defer f.Stop()
	defer close(gate)

	// First Enqueue is picked up by the sender, which then blocks on the gate.
	f.Enqueue("t1", []byte("A"))
	// Give the sender a moment to dequeue the first item and block on the gate.
	time.Sleep(50 * time.Millisecond)

	// These Enqueues must NOT block on the stuck sender — they land in the
	// buffered channel and return immediately.
	done := make(chan struct{})
	go func() {
		f.Enqueue("t1", []byte("B"))
		f.Enqueue("t1", []byte("C"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Enqueue blocked while sender was stuck on a slow PostTaskInput; the UI event loop must never block")
	}
}

// TestPaneForwarder_CoalescesConsecutiveSameTask proves that when several
// same-target items pile up while the sender is busy, they batch into a single
// PostTaskInput call with the concatenated payload (fewer calls than bytes).
func TestPaneForwarder_CoalescesConsecutiveSameTask(t *testing.T) {
	gate := make(chan struct{})
	p := &recordingPoster{gate: gate}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	defer f.Stop()

	// Enqueue the first byte; the sender dequeues it and blocks on the gate.
	f.Enqueue("t1", []byte("A"))
	time.Sleep(50 * time.Millisecond)

	// While the sender is parked on the gate, queue several more same-target
	// bytes. They accumulate in the channel.
	f.Enqueue("t1", []byte("B"))
	f.Enqueue("t1", []byte("C"))
	f.Enqueue("t1", []byte("D"))
	time.Sleep(50 * time.Millisecond)

	// Release the sender: it finishes the first call (A), then drains the
	// remaining three and coalesces them into ONE call ("BCD").
	gate <- struct{}{} // release A
	gate <- struct{}{} // release the coalesced batch

	if !waitForCond(2*time.Second, func() bool {
		calls := p.Calls()
		return len(calls) == 2
	}) {
		t.Fatalf("want exactly 2 PostTaskInput calls (A, then coalesced BCD); got %d (%v)", len(p.Calls()), payloadStrings(p.Calls()))
	}
	calls := p.Calls()
	if string(calls[0].Payload) != "A" {
		t.Fatalf("first call must be 'A'; got %q", calls[0].Payload)
	}
	if string(calls[1].Payload) != "BCD" {
		t.Fatalf("second call must coalesce to 'BCD'; got %q", calls[1].Payload)
	}
	// 4 bytes, fewer than 4 calls.
	if len(calls) >= 4 {
		t.Fatalf("coalescing must yield fewer calls than bytes; got %d calls for 4 bytes", len(calls))
	}
}

// TestPaneForwarder_DoesNotCoalesceAcrossTasks proves a target change mid-stream
// flushes the earlier task's bytes in their own call — bytes for t1 and t2 are
// never merged into one POST.
func TestPaneForwarder_DoesNotCoalesceAcrossTasks(t *testing.T) {
	gate := make(chan struct{})
	p := &recordingPoster{gate: gate}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	defer f.Stop()

	f.Enqueue("t1", []byte("A")) // sender dequeues, blocks on gate
	time.Sleep(50 * time.Millisecond)
	f.Enqueue("t1", []byte("B"))
	f.Enqueue("t2", []byte("C")) // different task — must NOT coalesce with t1
	f.Enqueue("t2", []byte("D"))
	time.Sleep(50 * time.Millisecond)

	gate <- struct{}{} // release A
	gate <- struct{}{} // release coalesced t1 batch (B)
	gate <- struct{}{} // release coalesced t2 batch (CD)

	if !waitForCond(2*time.Second, func() bool { return len(p.Calls()) == 3 }) {
		t.Fatalf("want 3 calls (A | B | CD); got %d (%v)", len(p.Calls()), payloadStrings(p.Calls()))
	}
	calls := p.Calls()
	if calls[0].TaskID != "t1" || string(calls[0].Payload) != "A" {
		t.Fatalf("call 0: want t1/'A'; got %s/%q", calls[0].TaskID, calls[0].Payload)
	}
	if calls[1].TaskID != "t1" || string(calls[1].Payload) != "B" {
		t.Fatalf("call 1: want t1/'B'; got %s/%q", calls[1].TaskID, calls[1].Payload)
	}
	if calls[2].TaskID != "t2" || string(calls[2].Payload) != "CD" {
		t.Fatalf("call 2: want t2/'CD' (coalesced same-target); got %s/%q", calls[2].TaskID, calls[2].Payload)
	}
}

// TestPaneForwarder_StopTerminatesGoroutine proves Stop() returns and the
// sender goroutine exits (no leak). A second Stop is a safe no-op.
func TestPaneForwarder_StopTerminatesGoroutine(t *testing.T) {
	p := &recordingPoster{}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	f.Enqueue("t1", []byte("A"))
	f.Stop()
	f.Stop() // idempotent
	// Enqueue after Stop must not panic or block.
	f.Enqueue("t1", []byte("B"))
}

// TestPaneForwarder_ContextCancelStops proves cancelling the supplied context
// stops the sender goroutine cleanly.
func TestPaneForwarder_ContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &recordingPoster{}
	f := NewPaneForwarder(ctx, p, slog.Default(), 64)
	cancel()
	// Stop should return promptly even though it was the context, not Stop,
	// that triggered teardown.
	done := make(chan struct{})
	go func() { f.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Stop did not return after context cancel; sender goroutine may be leaked")
	}
}

// failingForwardPoster always errors so the forwarder's own logging path can be
// asserted (the failure log moved from the router's synchronous path into the
// sender goroutine).
type failingForwardPoster struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (p *failingForwardPoster) PostTaskInput(_ context.Context, _ string, _ []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return 0, p.err
}

func (p *failingForwardPoster) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestPaneForwarder_FailureLogged proves a failed POST in the sender goroutine
// logs a warning carrying the task id + error (preserving the E1 diagnostic).
func TestPaneForwarder_FailureLogged(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	p := &failingForwardPoster{err: errors.New("argus down")}
	f := NewPaneForwarder(context.Background(), p, logger, 64)
	defer f.Stop()

	f.Enqueue("agent-1", []byte("Z"))

	if !waitForCond(2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(buf.String(), "forward keystroke to pane PTY failed")
	}) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("forwarder must log forward failures; log was: %q", buf.String())
	}
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "agent-1") || !strings.Contains(out, "argus down") {
		t.Fatalf("failure log must carry task id + error; log was: %q", out)
	}
}

// TestPaneForwarder_DeadCallbackFiredOnErrNoTaskInput proves that SetOnDead
// callback fires when PostTaskInput returns ErrNoTaskInput (the HTTP 404 that
// argus returns when a task's PTY session has ended). This is the BUG-006
// dead-session detection path: the callback lets OnPaneDead force focus back
// to RAIL so keystrokes stop being silently swallowed.
func TestPaneForwarder_DeadCallbackFiredOnErrNoTaskInput(t *testing.T) {
	p := &failingForwardPoster{err: argus.ErrNoTaskInput}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	defer f.Stop()

	var deadMu sync.Mutex
	var deadCalls []string
	f.SetOnDead(func(taskID string) {
		deadMu.Lock()
		deadCalls = append(deadCalls, taskID)
		deadMu.Unlock()
	})

	f.Enqueue("task-dead", []byte("hello"))

	if !waitForCond(2*time.Second, func() bool {
		deadMu.Lock()
		defer deadMu.Unlock()
		return len(deadCalls) > 0
	}) {
		t.Fatalf("onDead callback must fire when PostTaskInput returns ErrNoTaskInput")
	}
	deadMu.Lock()
	got := deadCalls[0]
	deadMu.Unlock()
	if got != "task-dead" {
		t.Fatalf("onDead must receive the dead task ID; got %q", got)
	}
}

// TestPaneForwarder_DeadCallbackFiresOncePerTask proves the onDead callback
// fires at most once per task even when many keystrokes return ErrNoTaskInput.
// Subsequent 404s for the same task are silently swallowed so the callback
// (which triggers focus-to-RAIL) does not spam the tview event loop.
func TestPaneForwarder_DeadCallbackFiresOncePerTask(t *testing.T) {
	p := &failingForwardPoster{err: argus.ErrNoTaskInput}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	defer f.Stop()

	var mu sync.Mutex
	var calls int
	f.SetOnDead(func(_ string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	// Send many keystrokes — all will 404.
	for i := 0; i < 20; i++ {
		f.Enqueue("task-dead", []byte("x"))
	}

	// Wait for at least one dead callback.
	if !waitForCond(2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls > 0
	}) {
		t.Fatalf("onDead callback must fire at least once")
	}
	// Give a moment for any extra calls to land.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("onDead must fire exactly once per task; got %d calls", n)
	}
}

// TestPaneForwarder_DeadCallbackNotFiredOnOtherErrors proves the onDead
// callback is NOT triggered for non-404 failures — only ErrNoTaskInput
// (dead-session 404) should force focus to RAIL.
func TestPaneForwarder_DeadCallbackNotFiredOnOtherErrors(t *testing.T) {
	p := &failingForwardPoster{err: errors.New("network timeout")}
	f := NewPaneForwarder(context.Background(), p, slog.Default(), 64)
	defer f.Stop()

	var mu sync.Mutex
	var calls int
	f.SetOnDead(func(_ string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	f.Enqueue("task-1", []byte("x"))

	// Wait until the poster was called.
	if !waitForCond(2*time.Second, func() bool { return p.Calls() > 0 }) {
		t.Fatalf("poster was never called")
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 0 {
		t.Fatalf("onDead must NOT fire for non-404 errors; got %d calls", n)
	}
}

// lockedWriter serializes concurrent writes from the sender goroutine + test.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// --- helpers ---

func waitForCond(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func concatPayload(calls []postCall, taskID string) string {
	var b strings.Builder
	for _, c := range calls {
		if c.TaskID == taskID {
			b.Write(c.Payload)
		}
	}
	return b.String()
}

func payloadStrings(calls []postCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.TaskID + ":" + string(c.Payload)
	}
	return out
}
