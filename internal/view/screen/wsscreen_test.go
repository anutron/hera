package screen

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gdamore/tcell/v2"
)

type fakeMsg struct {
	typ  websocket.MessageType
	data []byte
}

type fakeConn struct {
	in        chan fakeMsg
	out       chan fakeMsg
	closeOnce sync.Once
	closed    chan struct{}
	closes    atomic.Int32
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		in:     make(chan fakeMsg, 32),
		out:    make(chan fakeMsg, 64),
		closed: make(chan struct{}),
	}
}

func (c *fakeConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case msg := <-c.in:
		return msg.typ, msg.data, nil
	case <-c.closed:
		return 0, nil, io.EOF
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (c *fakeConn) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	select {
	case <-c.closed:
		return io.ErrClosedPipe
	default:
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case c.out <- fakeMsg{typ: typ, data: cp}:
		return nil
	case <-c.closed:
		return io.ErrClosedPipe
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeConn) Close(code websocket.StatusCode, reason string) error {
	c.closes.Add(1)
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeConn) push(typ websocket.MessageType, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	c.in <- fakeMsg{typ: typ, data: cp}
}

func (c *fakeConn) waitOut(t *testing.T, timeout time.Duration) (fakeMsg, bool) {
	t.Helper()
	select {
	case msg := <-c.out:
		return msg, true
	case <-time.After(timeout):
		return fakeMsg{}, false
	}
}

// drainOut collects every outbound frame until a quiet period of `quiet`
// elapses, or the cap is hit. It is used when we don't know how many frames
// the underlying tcell screen will emit but want to bound the wait.
func (c *fakeConn) drainOut(quiet time.Duration, capN int) []fakeMsg {
	var out []fakeMsg
	for {
		select {
		case msg := <-c.out:
			out = append(out, msg)
			if len(out) >= capN {
				return out
			}
		case <-time.After(quiet):
			return out
		}
	}
}

func newScreenForTest(t *testing.T) (*Screen, *fakeConn) {
	t.Helper()
	conn := newFakeConn()
	s, err := New(context.Background(), conn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		s.Fini()
	})
	return s, conn
}

func TestNewRequiresConn(t *testing.T) {
	if _, err := New(context.Background(), nil); err == nil {
		t.Fatal("New(nil) returned no error")
	}
}

func TestShowEmitsBinaryFrameWithCellContent(t *testing.T) {
	s, conn := newScreenForTest(t)

	s.SetContent(0, 0, 'X', nil, tcell.StyleDefault)
	s.Show()

	// Pull every frame the screen has queued; the cell write may be batched
	// with initialization sequences, but the visible rune must appear.
	frames := conn.drainOut(200*time.Millisecond, 16)
	if len(frames) == 0 {
		t.Fatal("no outbound frames")
	}
	var combined bytes.Buffer
	for _, f := range frames {
		if f.typ != websocket.MessageBinary {
			t.Errorf("frame typ=%v want MessageBinary", f.typ)
		}
		combined.Write(f.data)
	}
	if !bytes.Contains(combined.Bytes(), []byte{'X'}) {
		t.Errorf("emitted frames did not contain 'X': %q", combined.Bytes())
	}
	// Frames must contain at least one ESC byte (terminal control).
	if !bytes.ContainsRune(combined.Bytes(), 0x1b) {
		t.Errorf("emitted frames contained no ANSI escape bytes: %q", combined.Bytes())
	}
}

func TestSyncForcesEmission(t *testing.T) {
	s, conn := newScreenForTest(t)

	// Drain init frames first.
	conn.drainOut(150*time.Millisecond, 32)

	s.SetContent(2, 0, 'Y', nil, tcell.StyleDefault)
	s.Sync()

	frames := conn.drainOut(200*time.Millisecond, 16)
	if len(frames) == 0 {
		t.Fatal("Sync produced no frames")
	}
	var combined bytes.Buffer
	for _, f := range frames {
		combined.Write(f.data)
	}
	if !bytes.Contains(combined.Bytes(), []byte{'Y'}) {
		t.Errorf("Sync frames did not contain 'Y': %q", combined.Bytes())
	}
}

func TestInboundBinaryDeliversKeyEvent(t *testing.T) {
	s, conn := newScreenForTest(t)

	conn.push(websocket.MessageBinary, []byte("a"))

	ev := pollFor(t, s, 2*time.Second, func(ev tcell.Event) bool {
		k, ok := ev.(*tcell.EventKey)
		return ok && k.Rune() == 'a'
	})
	if ev == nil {
		t.Fatal("did not observe EventKey('a')")
	}
}

func TestResizeEnvelopeProducesEventResize(t *testing.T) {
	s, conn := newScreenForTest(t)

	conn.push(websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`))

	ev := pollFor(t, s, 2*time.Second, func(ev tcell.Event) bool {
		r, ok := ev.(*tcell.EventResize)
		if !ok {
			return false
		}
		w, h := r.Size()
		return w == 120 && h == 40
	})
	if ev == nil {
		t.Fatal("did not observe EventResize(120, 40)")
	}
}

func TestFocusEnvelopeProducesEventFocus(t *testing.T) {
	s, conn := newScreenForTest(t)

	conn.push(websocket.MessageText, []byte(`{"type":"focus"}`))
	ev := pollFor(t, s, 2*time.Second, func(ev tcell.Event) bool {
		f, ok := ev.(*tcell.EventFocus)
		return ok && f.Focused
	})
	if ev == nil {
		t.Fatal("did not observe EventFocus(true)")
	}
}

func TestBlurEnvelopeProducesEventFocus(t *testing.T) {
	s, conn := newScreenForTest(t)

	conn.push(websocket.MessageText, []byte(`{"type":"blur"}`))
	ev := pollFor(t, s, 2*time.Second, func(ev tcell.Event) bool {
		f, ok := ev.(*tcell.EventFocus)
		return ok && !f.Focused
	})
	if ev == nil {
		t.Fatal("did not observe EventFocus(false)")
	}
}

func TestMalformedEnvelopeIgnored(t *testing.T) {
	s, conn := newScreenForTest(t)

	// Two malformed frames followed by a well-formed focus frame. The
	// screen should not panic and should still surface the focus event.
	conn.push(websocket.MessageText, []byte(`not json`))
	conn.push(websocket.MessageText, []byte(`{"type":"unknown"}`))
	conn.push(websocket.MessageText, []byte(`{"type":"focus"}`))

	ev := pollFor(t, s, 2*time.Second, func(ev tcell.Event) bool {
		f, ok := ev.(*tcell.EventFocus)
		return ok && f.Focused
	})
	if ev == nil {
		t.Fatal("focus event lost after malformed envelopes")
	}
}

func TestResizeUpdatesScreenSize(t *testing.T) {
	s, conn := newScreenForTest(t)

	conn.push(websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":30}`))

	// Drain events until we see the matching resize so we know the screen
	// has internally processed the new geometry.
	ev := pollFor(t, s, 2*time.Second, func(ev tcell.Event) bool {
		r, ok := ev.(*tcell.EventResize)
		if !ok {
			return false
		}
		w, h := r.Size()
		return w == 100 && h == 30
	})
	if ev == nil {
		t.Fatal("did not observe EventResize(100, 30)")
	}
	w, h := s.Size()
	if w != 100 || h != 30 {
		t.Errorf("Size() = (%d, %d), want (100, 30)", w, h)
	}
}

func TestFiniClosesConn(t *testing.T) {
	conn := newFakeConn()
	s, err := New(context.Background(), conn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	s.Fini()

	if got := conn.closes.Load(); got == 0 {
		t.Errorf("expected Conn.Close to be called at least once, got %d", got)
	}
}

func TestReadErrorShutsDownConn(t *testing.T) {
	conn := newFakeConn()
	s, err := New(context.Background(), conn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { s.Fini() })

	// Trigger Read EOF by closing the conn from the "outside".
	_ = conn.Close(websocket.StatusNormalClosure, "")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("read loop did not stop after conn close")
		default:
		}
		if conn.closes.Load() >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pollFor pumps events from the screen until the predicate matches or the
// timeout expires.
func pollFor(t *testing.T, s *Screen, timeout time.Duration, pred func(tcell.Event) bool) tcell.Event {
	t.Helper()
	deadline := time.Now().Add(timeout)
	done := make(chan struct{})
	defer close(done)
	ch := make(chan tcell.Event, 64)
	go s.ChannelEvents(ch, done)
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if pred(ev) {
				return ev
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}
