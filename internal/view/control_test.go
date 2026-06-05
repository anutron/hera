package view

import (
	"context"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// fakeControlConn captures every Write call so tests can assert the exact
// frame type and JSON payload the viewControl sender emits. It satisfies the
// controlConn interface (the Write subset of pluginview.Conn / *websocket.Conn).
type fakeControlConn struct {
	mu     sync.Mutex
	writes []controlWrite
	err    error
}

type controlWrite struct {
	Type websocket.MessageType
	Data []byte
}

func (f *fakeControlConn) Write(_ context.Context, typ websocket.MessageType, p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, controlWrite{Type: typ, Data: append([]byte(nil), p...)})
	return f.err
}

func (f *fakeControlConn) Writes() []controlWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]controlWrite, len(f.writes))
	copy(out, f.writes)
	return out
}

func TestViewControl_Release_ExactJSON(t *testing.T) {
	conn := &fakeControlConn{}
	vc := newViewControl(context.Background(), conn)

	if err := vc.SendRelease(); err != nil {
		t.Fatalf("SendRelease: %v", err)
	}

	w := conn.Writes()
	if len(w) != 1 {
		t.Fatalf("want 1 frame, got %d", len(w))
	}
	if w[0].Type != websocket.MessageText {
		t.Fatalf("release must be a TEXT frame; got %v", w[0].Type)
	}
	if got := string(w[0].Data); got != `{"type":"release"}` {
		t.Fatalf("release JSON mismatch:\n got %s\nwant %s", got, `{"type":"release"}`)
	}
}

func TestViewControl_Help_ExactJSON(t *testing.T) {
	conn := &fakeControlConn{}
	vc := newViewControl(context.Background(), conn)

	if err := vc.SendHelp(); err != nil {
		t.Fatalf("SendHelp: %v", err)
	}

	w := conn.Writes()
	if len(w) != 1 {
		t.Fatalf("want 1 frame, got %d", len(w))
	}
	if w[0].Type != websocket.MessageText {
		t.Fatalf("help must be a TEXT frame; got %v", w[0].Type)
	}
	if got := string(w[0].Data); got != `{"type":"help"}` {
		t.Fatalf("help JSON mismatch:\n got %s\nwant %s", got, `{"type":"help"}`)
	}
}

func TestViewControl_Hotkeys_ExactJSON(t *testing.T) {
	conn := &fakeControlConn{}
	vc := newViewControl(context.Background(), conn)

	items := []HotkeyItem{
		{Key: "j", Label: "down", Bar: true},
		{Key: "?", Label: "help", Bar: false},
	}
	if err := vc.SendHotkeys(items); err != nil {
		t.Fatalf("SendHotkeys: %v", err)
	}

	w := conn.Writes()
	if len(w) != 1 {
		t.Fatalf("want 1 frame, got %d", len(w))
	}
	if w[0].Type != websocket.MessageText {
		t.Fatalf("hotkeys must be a TEXT frame; got %v", w[0].Type)
	}
	want := `{"type":"hotkeys","items":[{"key":"j","label":"down","bar":true},{"key":"?","label":"help","bar":false}]}`
	if got := string(w[0].Data); got != want {
		t.Fatalf("hotkeys JSON mismatch:\n got %s\nwant %s", got, want)
	}
}

// A nil viewControl must be a safe no-op so tests / daemon paths that build an
// App without a session conn don't panic.
func TestViewControl_NilSafe(t *testing.T) {
	var vc *viewControl
	if err := vc.SendRelease(); err != nil {
		t.Fatalf("nil SendRelease should be a no-op, got %v", err)
	}
	if err := vc.SendHelp(); err != nil {
		t.Fatalf("nil SendHelp should be a no-op, got %v", err)
	}
	if err := vc.SendHotkeys(nil); err != nil {
		t.Fatalf("nil SendHotkeys should be a no-op, got %v", err)
	}
}
