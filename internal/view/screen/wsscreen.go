// Package screen provides a tcell.Screen implementation backed by a WebSocket
// connection that follows argus's plugin-view wire contract. Show() and
// Sync() emit the accumulated ANSI surface bytes as a single binary frame;
// inbound binary frames feed tcell's input parser, and inbound text frames
// are JSON envelopes ({"type":"resize"|"focus"|"blur",...}) translated into
// tcell.EventResize and tcell.EventFocus.
//
// The package is the substrate the hera daemon uses to render its argus
// plugin view inside the daemon process. See openspec/changes/add-hera-view
// for the broader design.
package screen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/coder/websocket"
	"github.com/gdamore/tcell/v2"
	// Register the xterm terminfo entries (includes xterm-256color). The
	// chosen terminfo only affects the ANSI dialect emitted on the wire;
	// argus's connector consumes whatever well-formed ANSI we send.
	_ "github.com/gdamore/tcell/v2/terminfo/x/xterm"
)

// terminfoName pins the dialect we emit. argus's streampane parser is
// xterm-compatible (verified during the add-hera-view brainstorm).
const terminfoName = "xterm-256color"

// defaultCols / defaultRows are the geometry used until the first resize
// envelope arrives from argus.
const (
	defaultCols = 80
	defaultRows = 24
)

// Conn is the slice of *websocket.Conn we depend on. Tests substitute a fake.
type Conn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	Close(code websocket.StatusCode, reason string) error
}

// Screen wraps a tcell.Screen whose Tty is the WebSocket connection. Show()
// and Sync() flush whatever tcell wrote during the call as a single binary
// frame; the read loop turns inbound traffic into key events and control
// envelopes.
type Screen struct {
	tcell.Screen // delegated; we override Show, Sync, and Fini

	conn Conn
	tty  *wsTty

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// New builds a Screen ready to be Init()ed. The supplied context governs
// reads and writes on the underlying connection; cancelling it causes the
// screen to shut down on its next blocking operation. Callers MUST invoke
// Init exactly once and Fini before discarding the value.
func New(ctx context.Context, conn Conn) (*Screen, error) {
	if conn == nil {
		return nil, errors.New("wsscreen: conn must not be nil")
	}
	ti, err := tcell.LookupTerminfo(terminfoName)
	if err != nil {
		return nil, fmt.Errorf("wsscreen: lookup terminfo %q: %w", terminfoName, err)
	}
	tty := newWSTty()
	inner, err := tcell.NewTerminfoScreenFromTtyTerminfo(tty, ti)
	if err != nil {
		return nil, fmt.Errorf("wsscreen: construct screen: %w", err)
	}
	sCtx, cancel := context.WithCancel(ctx)
	s := &Screen{
		Screen: inner,
		conn:   conn,
		tty:    tty,
		ctx:    sCtx,
		cancel: cancel,
	}
	s.wg.Add(1)
	go s.readLoop()
	return s, nil
}

// Show flushes pending draws and ships the resulting ANSI bytes as one
// binary WebSocket frame.
func (s *Screen) Show() {
	s.Screen.Show()
	s.flushFrame()
}

// Sync forces a full redraw and emits the resulting bytes as one binary
// frame. Sync is the heavier of the two and is the right call after a
// resize or a suspected desync.
func (s *Screen) Sync() {
	s.Screen.Sync()
	s.flushFrame()
}

// Fini tears down the inner screen, stops the read loop, and closes the
// underlying connection. Safe to call multiple times.
func (s *Screen) Fini() {
	s.Screen.Fini()
	s.shutdown()
}

func (s *Screen) flushFrame() {
	buf := s.tty.drainOutput()
	if len(buf) == 0 {
		return
	}
	if err := s.conn.Write(s.ctx, websocket.MessageBinary, buf); err != nil {
		s.shutdown()
	}
}

func (s *Screen) shutdown() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.tty.close()
		_ = s.conn.Close(websocket.StatusNormalClosure, "")
	})
}

func (s *Screen) readLoop() {
	defer s.wg.Done()
	for {
		typ, data, err := s.conn.Read(s.ctx)
		if err != nil {
			s.shutdown()
			return
		}
		switch typ {
		case websocket.MessageBinary:
			s.tty.push(data)
		case websocket.MessageText:
			s.handleEnvelope(data)
		}
	}
}

// envelope is the JSON shape argus sends on text frames. Extra fields are
// ignored; unknown types are dropped silently.
type envelope struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Screen) handleEnvelope(data []byte) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	switch env.Type {
	case "resize":
		if env.Cols <= 0 || env.Rows <= 0 {
			return
		}
		s.tty.setSize(env.Cols, env.Rows)
	case "focus":
		_ = s.Screen.PostEvent(tcell.NewEventFocus(true))
	case "blur":
		_ = s.Screen.PostEvent(tcell.NewEventFocus(false))
	}
}

// wsTty satisfies tcell.Tty by buffering bytes the screen writes (so Show /
// Sync can ship them as one frame) and serving inbound WebSocket bytes as
// the input stream tcell's parser scans.
type wsTty struct {
	inMu     sync.Mutex
	inBuf    []byte
	inWake   chan struct{}

	outMu  sync.Mutex
	outBuf []byte

	sizeMu   sync.Mutex
	cols     int
	rows     int
	resizeCB func()

	doneMu    sync.Mutex
	doneClosed bool
	done      chan struct{}
}

func newWSTty() *wsTty {
	return &wsTty{
		inWake: make(chan struct{}, 1),
		cols:   defaultCols,
		rows:   defaultRows,
		done:   make(chan struct{}),
	}
}

func (t *wsTty) Start() error { return nil }

func (t *wsTty) Stop() error {
	t.close()
	return nil
}

func (t *wsTty) Drain() error {
	t.close()
	return nil
}

func (t *wsTty) Close() error {
	t.close()
	return nil
}

func (t *wsTty) close() {
	t.doneMu.Lock()
	defer t.doneMu.Unlock()
	if t.doneClosed {
		return
	}
	t.doneClosed = true
	close(t.done)
}

// Read serves bytes received over the WebSocket to tcell's input parser.
// It blocks until either bytes arrive or the screen is torn down.
func (t *wsTty) Read(p []byte) (int, error) {
	for {
		t.inMu.Lock()
		if len(t.inBuf) > 0 {
			n := copy(p, t.inBuf)
			t.inBuf = t.inBuf[n:]
			t.inMu.Unlock()
			return n, nil
		}
		t.inMu.Unlock()
		select {
		case <-t.done:
			return 0, io.EOF
		case <-t.inWake:
		}
	}
}

// Write appends to the outbound buffer. The screen wrapper flushes the
// buffer once per Show / Sync call.
func (t *wsTty) Write(p []byte) (int, error) {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	select {
	case <-t.done:
		return 0, io.ErrClosedPipe
	default:
	}
	t.outBuf = append(t.outBuf, p...)
	return len(p), nil
}

func (t *wsTty) drainOutput() []byte {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	if len(t.outBuf) == 0 {
		return nil
	}
	out := t.outBuf
	t.outBuf = nil
	return out
}

func (t *wsTty) push(data []byte) {
	if len(data) == 0 {
		return
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	t.inMu.Lock()
	t.inBuf = append(t.inBuf, cp...)
	t.inMu.Unlock()
	select {
	case t.inWake <- struct{}{}:
	default:
	}
}

func (t *wsTty) NotifyResize(cb func()) {
	t.sizeMu.Lock()
	t.resizeCB = cb
	t.sizeMu.Unlock()
}

func (t *wsTty) WindowSize() (tcell.WindowSize, error) {
	t.sizeMu.Lock()
	defer t.sizeMu.Unlock()
	return tcell.WindowSize{
		Width:  t.cols,
		Height: t.rows,
	}, nil
}

func (t *wsTty) setSize(cols, rows int) {
	t.sizeMu.Lock()
	t.cols = cols
	t.rows = rows
	cb := t.resizeCB
	t.sizeMu.Unlock()
	if cb != nil {
		cb()
	}
}
